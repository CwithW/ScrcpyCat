package deployer

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type hostWorker struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (r *runner) reconcileHosts(ctx context.Context, serials []string) {
	present := make(map[string]bool, len(serials))
	for _, serial := range serials {
		present[serial] = true
	}
	for serial, worker := range r.hosts {
		select {
		case <-worker.done:
			worker.cancel()
			delete(r.hosts, serial)
		default:
			if !present[serial] {
				worker.cancel()
			}
		}
	}
	for _, serial := range serials {
		if _, exists := r.hosts[serial]; exists {
			continue
		}
		workerCtx, cancel := context.WithCancel(ctx)
		worker := &hostWorker{cancel: cancel, done: make(chan struct{})}
		r.hosts[serial] = worker
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			defer close(worker.done)
			r.hostLoop(workerCtx, serial)
		}()
	}
}

func (r *runner) hostLoop(ctx context.Context, serial string) {
	renewIdentity := false
	for attempt := 0; ctx.Err() == nil; attempt++ {
		started := time.Now()
		err := r.runHostAgent(ctx, serial, renewIdentity)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "host agent %s: %v\n", serial, err)
		}
		var exit *exec.ExitError
		renewIdentity = errors.As(err, &exit) && exit.ExitCode() == 3
		if time.Since(started) > time.Minute {
			attempt = 0
		}
		delay := time.Duration(min(attempt+1, 15)) * time.Second
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

func hostStateDirectory(root, serial string) string {
	hash := sha256.Sum256([]byte(serial))
	return filepath.Join(root, fmt.Sprintf("%x", hash[:16]))
}

func hasHostIdentity(path string) bool {
	contents, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var identity struct {
		AgentToken string `json:"agent_token"`
	}
	return json.Unmarshal(contents, &identity) == nil && identity.AgentToken != ""
}

func (r *runner) runHostAgent(ctx context.Context, serial string, renewIdentity bool) error {
	for _, path := range []string{r.config.HostAgent, r.config.ScrcpyJar} {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("not a regular artifact: %s", path)
		}
	}
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := r.devices.RequireShellV2(startup, serial); err != nil {
		return err
	}
	state := hostStateDirectory(r.config.StateDir, serial)
	identityPath := filepath.Join(state, "identity.json")
	token := ""
	if renewIdentity || !hasHostIdentity(identityPath) {
		var err error
		token, err = r.enrollmentToken(startup, serial)
		if err != nil {
			return err
		}
	}
	// Artifacts and credentials are checked before stopping the old mode.
	if err := os.MkdirAll(state, 0700); err != nil {
		return err
	}
	if err := r.stopAgent(startup, serial); err != nil {
		return err
	}
	logPath := filepath.Join(state, "agent.log")
	if info, err := os.Stat(logPath); err == nil && info.Size() > 10<<20 {
		_ = os.Rename(logPath, logPath+".1")
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	args := []string{"--adb-serial", serial, "--adb-server", r.config.ADBServer, "--device-id", serial,
		"--identity-file", identityPath, "--pid-file", filepath.Join(state, "agent.pid"), "--scrcpy-jar", r.config.ScrcpyJar, "--signaling", r.config.SignalingURL}
	command := exec.CommandContext(ctx, r.config.HostAgent, args...)
	command.Env = agentEnvironment(os.Environ(), token)
	command.Stdout, command.Stderr = io.MultiWriter(logFile, os.Stdout), io.MultiWriter(logFile, os.Stderr)
	command.Cancel = func() error { return command.Process.Signal(syscall.SIGTERM) }
	command.WaitDelay = 5 * time.Second
	fmt.Fprintf(os.Stderr, "starting Linux Agent for %s\n", serial)
	return command.Run()
}

func agentEnvironment(environment []string, token string) []string {
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "SCRCPYCAT_ENROLLMENT_TOKEN", "SCRCPYCAT_DEPLOYMENT_TOKEN", "SCRCPYCAT_ADMIN_TOKEN":
			continue
		}
		result = append(result, entry)
	}
	return append(result, "SCRCPYCAT_ENROLLMENT_TOKEN="+token)
}
