package deployer

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	adbclient "github.com/cwithw/ScrcpyCat/internal/adb"
)

type fakeADB struct {
	mu       sync.Mutex
	commands []string
	pushes   int
	online   []adbclient.Device
}

func (a *fakeADB) Ping(context.Context) error                          { return nil }
func (a *fakeADB) Devices(context.Context) ([]adbclient.Device, error) { return a.online, nil }
func (a *fakeADB) RequireShellV2(context.Context, string) error        { return nil }
func (a *fakeADB) Reverse(context.Context, string, string) error       { return nil }
func (a *fakeADB) Push(context.Context, string, io.Reader, string, os.FileMode) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pushes++
	return nil
}
func (a *fakeADB) RunLegacy(_ context.Context, _ string, command string, out io.Writer) error {
	a.mu.Lock()
	a.commands = append(a.commands, command)
	a.mu.Unlock()
	if strings.Contains(command, "--health") {
		return errors.New("not running")
	}
	if strings.Contains(command, "ro.product.cpu.abi") {
		_, _ = io.WriteString(out, "arm64-v8a\n")
	}
	return nil
}
func (a *fakeADB) mutated() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pushes > 0 {
		return true
	}
	for _, command := range a.commands {
		if !strings.Contains(command, "--health") && !strings.Contains(command, "ro.product.cpu.abi") {
			return true
		}
	}
	return false
}

func TestHostDefaultsAndModeValidation(t *testing.T) {
	c := Config{ADBPath: "adb", ScrcpyJar: "/jar", SignalingURL: "ws://control/register_agent", EnrollmentToken: "fixture"}
	c.ResolveDefaults()
	if c.Mode != ModeHost || c.ADBServer != adbclient.DefaultServer || c.StateDir == "" || c.HostAgent == "" {
		t.Fatalf("bad defaults: %+v", c)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Force = true
	if c.Validate() == nil {
		t.Fatal("host mode accepted --force")
	}
	c.Mode = ModeDevice
	c.ArtifactDir = "/artifacts"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Mode = "invalid"
	if c.Validate() == nil {
		t.Fatal("invalid mode accepted")
	}
}

func TestHostCredentialRejectionDoesNotStopDeviceAgent(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"agent", "jar"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("fixture"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer server.Close()
	devices := &fakeADB{}
	r := &runner{config: Config{HostAgent: filepath.Join(directory, "agent"), ScrcpyJar: filepath.Join(directory, "jar"), StateDir: directory, ControlPlaneURL: server.URL, DeploymentToken: "revoked"}, devices: devices, client: server.Client()}
	if err := r.runHostAgent(context.Background(), "phone", false); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected rejection, got %v", err)
	}
	if devices.mutated() {
		t.Fatal("credential rejection stopped or modified the device")
	}
}

func TestHostRestartsReuseIdentityAndIsolateState(t *testing.T) {
	directory := t.TempDir()
	marker := filepath.Join(directory, "arguments")
	agentPath := filepath.Join(directory, "agent")
	jar := filepath.Join(directory, "jar")
	script := "#!/bin/sh\ntest -z \"$SCRCPYCAT_DEPLOYMENT_TOKEN\" || exit 20\ntest -z \"$SCRCPYCAT_ENROLLMENT_TOKEN\" || exit 21\nprintf '%s\\n' \"$@\" > \"$SC_HOST_ARGUMENTS\"\n"
	if err := os.WriteFile(agentPath, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jar, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SC_HOST_ARGUMENTS", marker)
	t.Setenv("SCRCPYCAT_DEPLOYMENT_TOKEN", "must-not-reach-child")
	state := hostStateDirectory(directory, "phone")
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "identity.json"), []byte(`{"agent_token":"existing"}`), 0600); err != nil {
		t.Fatal(err)
	}
	r := &runner{config: Config{HostAgent: agentPath, ScrcpyJar: jar, StateDir: directory, ADBServer: adbclient.DefaultServer, SignalingURL: "ws://control/register_agent", ControlPlaneURL: "http://unreachable.invalid"}, devices: &fakeADB{}, client: &http.Client{}}
	for i := 0; i < 2; i++ {
		if err := r.runHostAgent(context.Background(), "phone", false); err != nil {
			t.Fatal(err)
		}
	}
	contents, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "--adb-serial\nphone\n") || !strings.Contains(string(contents), state) {
		t.Fatalf("unexpected arguments: %s", contents)
	}
	other := hostStateDirectory(directory, "../phone")
	if other == state || filepath.Dir(other) != directory {
		t.Fatal("device state directories overlap or escape their root")
	}
}

func TestHostDiscoveryUsesUSBWhileDeviceModeRetainsOtherTransports(t *testing.T) {
	r := &runner{config: Config{Mode: ModeHost}, devices: &fakeADB{online: []adbclient.Device{{Serial: "usb-phone", USB: true}, {Serial: "192.0.2.1:5555"}}}}
	serials, err := r.connectedSerials(context.Background())
	if err != nil || len(serials) != 1 || serials[0] != "usb-phone" {
		t.Fatalf("%v %v", serials, err)
	}
	r.config.Mode = ModeDevice
	serials, err = r.connectedSerials(context.Background())
	if err != nil || len(serials) != 2 {
		t.Fatalf("%v %v", serials, err)
	}
}

func TestHostReconnectWaitsForPreviousWorkerAndKeepsOtherDevices(t *testing.T) {
	first, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	second, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	worker := &hostWorker{cancel: cancelFirst, done: make(chan struct{})}
	r := &runner{hosts: map[string]*hostWorker{
		"first":  worker,
		"second": {cancel: cancelSecond, done: make(chan struct{})},
	}}
	r.reconcileHosts(context.Background(), []string{"second"})
	if first.Err() == nil || second.Err() != nil {
		t.Fatal("disconnect did not cancel only the missing device")
	}
	r.reconcileHosts(context.Background(), []string{"first", "second"})
	if r.hosts["first"] != worker {
		t.Fatal("reconnect started a second worker before the previous one exited")
	}
	close(worker.done)
	r.reconcileHosts(context.Background(), []string{"second"})
	if _, exists := r.hosts["first"]; exists {
		t.Fatal("finished worker was not released")
	}
}
