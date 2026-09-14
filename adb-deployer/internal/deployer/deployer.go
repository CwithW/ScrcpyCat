package deployer

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	adbclient "github.com/cwithw/ScrcpyCat/internal/adb"
)

type Config struct {
	Mode            string
	ADBServer       string
	HostAgent       string
	StateDir        string
	ADBPath         string
	ArtifactDir     string
	ScrcpyJar       string
	SignalingURL    string
	ControlPlaneURL string
	DeploymentToken string
	AdminToken      string
	EnrollmentToken string
	Interval        time.Duration
	InsecureTLS     bool
	Force           bool
}

const ModeHost = "host"
const ModeDevice = "device"

func (c *Config) ResolveDefaults() {
	if c.Mode == "" {
		c.Mode = ModeHost
	}
	if c.ADBServer == "" {
		c.ADBServer = adbclient.DefaultServer
	}
	if c.HostAgent == "" {
		c.HostAgent = "/usr/local/bin/scrcpycat-agent"
	}
	if c.StateDir == "" {
		c.StateDir = "/var/lib/scrcpycat/adb-deployer"
	}
	if c.Interval == 0 {
		c.Interval = 10 * time.Second
	}
}

func (c Config) Validate() error {
	c.ResolveDefaults()
	if c.Mode != ModeHost && c.Mode != ModeDevice {
		return errors.New("--mode must be host or device")
	}
	if c.Mode == ModeHost && c.Force {
		return errors.New("--force is only supported in device mode")
	}
	if c.Interval < 100*time.Millisecond {
		return errors.New("--interval must be at least 100ms")
	}
	if _, err := adbclient.New(c.ADBServer); err != nil {
		return err
	}
	if c.ADBPath == "" || (c.Mode == ModeDevice && c.ArtifactDir == "") || c.ScrcpyJar == "" || c.SignalingURL == "" {
		return errors.New("adb, artifact-dir, scrcpy-jar and signaling are required")
	}
	if c.ControlPlaneURL == "" && c.EnrollmentToken == "" {
		return errors.New("control-plane/admin-token or enrollment-token is required")
	}
	if c.ControlPlaneURL != "" && c.AdminToken == "" && c.DeploymentToken == "" {
		return errors.New("--deployment-token or --admin-token is required with --control-plane")
	}
	if c.AdminToken != "" && c.DeploymentToken != "" {
		return errors.New("configure only one of deployment-token and admin-token")
	}
	return nil
}

type runner struct {
	config  Config
	mu      sync.Mutex
	busy    map[string]struct{}
	client  *http.Client
	devices adbAPI
	hosts   map[string]*hostWorker
	wg      sync.WaitGroup
}

func Run(ctx context.Context, config Config) error {
	config.ResolveDefaults()
	if err := config.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: config.InsecureTLS} // #nosec G402: explicit development flag
	devices, err := adbclient.New(config.ADBServer)
	if err != nil {
		return err
	}
	r := &runner{config: config, busy: make(map[string]struct{}), hosts: make(map[string]*hostWorker), devices: devices, client: &http.Client{Timeout: 15 * time.Second, Transport: transport}}
	defer func() { cancel(); r.wg.Wait() }()
	if err := r.ensureADBServer(ctx); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "adb-deployer mode=%s server=%s\n", config.Mode, config.ADBServer)
	ticker := time.NewTicker(config.Interval)
	defer ticker.Stop()
	for {
		r.scan(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (r *runner) scan(ctx context.Context) {
	check, cancel := context.WithTimeout(ctx, 10*time.Second)
	serials, err := r.connectedSerials(check)
	cancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "adb scan: %v\n", err)
		if ctx.Err() == nil {
			if err := r.ensureADBServer(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "adb server: %v\n", err)
			}
		}
		return
	}
	if r.config.Mode == ModeHost {
		r.reconcileHosts(ctx, serials)
		return
	}
	for _, serial := range serials {
		if !r.acquire(serial) {
			continue
		}
		r.wg.Add(1)
		go func(serial string) {
			defer r.wg.Done()
			defer r.release(serial)
			if err := r.ensureAgent(ctx, serial); err != nil {
				fmt.Fprintf(os.Stderr, "deploy %s: %v\n", serial, err)
			}
		}(serial)
	}
}

func (r *runner) connectedSerials(ctx context.Context) ([]string, error) {
	devices, err := r.devices.Devices(ctx)
	if err != nil {
		return nil, err
	}
	serials := make([]string, 0)
	for _, device := range devices {
		if r.config.Mode == ModeHost && !device.USB {
			continue
		}
		serials = append(serials, device.Serial)
	}
	return serials, nil
}

func (r *runner) ensureAgent(ctx context.Context, serial string) error {
	if !r.config.Force && r.agentHealthy(ctx, serial) {
		return nil
	}
	abi, err := r.deviceABI(ctx, serial)
	if err != nil {
		return err
	}
	agentBinary := filepath.Join(r.config.ArtifactDir, abi, "scrcpycat-agent")
	if _, err := os.Stat(agentBinary); err != nil {
		return fmt.Errorf("agent artifact for %s: %w", abi, err)
	}
	if _, err := os.Stat(r.config.ScrcpyJar); err != nil {
		return fmt.Errorf("scrcpy jar: %w", err)
	}
	token, err := r.enrollmentToken(ctx, serial)
	if err != nil {
		return err
	}
	if err := r.stopAgent(ctx, serial); err != nil {
		return err
	}
	if err := r.push(ctx, serial, agentBinary, "/data/local/tmp/scrcpycat-agent", 0700); err != nil {
		return err
	}
	if err := r.push(ctx, serial, r.config.ScrcpyJar, "/data/local/tmp/scrcpy-server.jar", 0644); err != nil {
		return err
	}
	if err := r.configureLoopbackReverse(ctx, serial); err != nil {
		return err
	}
	// Legacy shell may hang up its process group as soon as it exits. Ignore
	// HUP before spawning/setsid, and wait until the detached Agent is healthy.
	args := fmt.Sprintf("/data/local/tmp/scrcpycat-agent --device-id %s --signaling %s --enrollment-token %s --scrcpy-jar /data/local/tmp/scrcpy-server.jar", shellQuote(serial), shellQuote(r.config.SignalingURL), shellQuote(token))
	command := "chmod 700 /data/local/tmp/scrcpycat-agent || exit $?; (trap '' HUP; if command -v setsid >/dev/null 2>&1; then exec setsid " + args + "; else exec " + args + "; fi) </dev/null >>/data/local/tmp/scrcpycat-agent.log 2>&1 & " +
		"for attempt in 1 2 3 4 5 6 7 8 9 10; do /data/local/tmp/scrcpycat-agent --health >/dev/null 2>&1 && exit 0; sleep 0.5; done; exit 1"
	launchContext, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	_, err = r.shell(launchContext, serial, command)
	if errors.Is(launchContext.Err(), context.DeadlineExceeded) && r.agentHealthy(ctx, serial) {
		// Some Android shell implementations retain the ADB shell service despite
		// the detached Agent having started. Releasing this client lets the next
		// health check confirm startup instead of permanently holding the lock.
		return nil
	}
	return err
}

func (r *runner) configureLoopbackReverse(ctx context.Context, serial string) error {
	port, loopback, err := loopbackSignalingPort(r.config.SignalingURL)
	if err != nil {
		return err
	}
	if !loopback {
		return nil
	}
	return r.devices.Reverse(ctx, serial, port)
}

func loopbackSignalingPort(signalingURL string) (string, bool, error) {
	endpoint, err := url.Parse(signalingURL)
	if err != nil {
		return "", false, fmt.Errorf("parse signaling URL: %w", err)
	}
	loopback := endpoint.Hostname() == "127.0.0.1" || endpoint.Hostname() == "localhost" || endpoint.Hostname() == "::1"
	if !loopback {
		return "", false, nil
	}
	port := endpoint.Port()
	if port == "" {
		if endpoint.Scheme == "wss" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return port, true, nil
}

func (r *runner) agentHealthy(ctx context.Context, serial string) bool {
	check, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err := r.shell(check, serial, "/data/local/tmp/scrcpycat-agent --health")
	return err == nil
}

func (r *runner) stopAgent(ctx context.Context, serial string) error {
	// The Agent owns an exact PID file. Prefer it because Android's pidof and
	// ps implementations are inconsistent, and the launched argv need not keep
	// the /data/local/tmp path that an older pkill fallback expected.
	// A stubborn process is killed only after a short graceful-stop window.
	command := `stop_agent() {
case "$1" in ''|*[!0-9]*) return ;; esac
[ "$1" -gt 1 ] || return
exe=$(readlink "/proc/$1/exe" 2>/dev/null)
case "$exe" in /data/local/tmp/scrcpycat-agent|'/data/local/tmp/scrcpycat-agent (deleted)') ;; *) return ;; esac
kill "$1" 2>/dev/null || true
for attempt in 1 2 3; do [ ! -d "/proc/$1" ] && return; sleep 1; done
[ "$(readlink "/proc/$1/exe" 2>/dev/null)" = "$exe" ] && kill -9 "$1" 2>/dev/null || true
}
if [ -r /data/local/tmp/scrcpycat-agent.pid ]; then stop_agent "$(cat /data/local/tmp/scrcpycat-agent.pid)"; fi
if command -v pidof >/dev/null 2>&1; then for candidate in $(pidof scrcpycat-agent 2>/dev/null); do stop_agent "$candidate"; done; fi
exit 0`
	_, err := r.shell(ctx, serial, command)
	return err
}

func (r *runner) deviceABI(ctx context.Context, serial string) (string, error) {
	output, err := r.shell(ctx, serial, "getprop ro.product.cpu.abi")
	if err != nil {
		return "", err
	}
	abi := strings.TrimSpace(string(output))
	switch abi {
	case "arm64-v8a", "armeabi-v7a", "x86", "x86_64":
		return abi, nil
	default:
		return "", fmt.Errorf("unsupported device ABI %q", abi)
	}
}

func (r *runner) enrollmentToken(ctx context.Context, deviceID string) (string, error) {
	if r.config.ControlPlaneURL == "" {
		return r.config.EnrollmentToken, nil
	}
	path, credential := "/api/agents/enrollment-tokens", r.config.AdminToken
	if r.config.DeploymentToken != "" {
		path, credential = "/api/deploy/enrollment-tokens", r.config.DeploymentToken
	}
	body, _ := json.Marshal(map[string]any{"device_id": deviceID, "ttl_seconds": 900})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(r.config.ControlPlaneURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+credential)
	request.Header.Set("Content-Type", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		contents, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return "", fmt.Errorf("issue enrollment token: %s: %s", response.Status, strings.TrimSpace(string(contents)))
	}
	var result struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil || result.Token == "" {
		return "", errors.New("control plane returned an invalid enrollment token")
	}
	return result.Token, nil
}

func (r *runner) acquire(serial string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, busy := r.busy[serial]; busy {
		return false
	}
	r.busy[serial] = struct{}{}
	return true
}

func (r *runner) release(serial string) {
	r.mu.Lock()
	delete(r.busy, serial)
	r.mu.Unlock()
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
