package deployer

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	adbclient "github.com/cwithw/ScrcpyCat/internal/adb"
)

// Explicit opt-in: this test restores the standalone Agent on a test device.
func TestLiveDeviceDeployment(t *testing.T) {
	serial := os.Getenv("SCRCPYCAT_TEST_ADB_SERIAL")
	endpoint := os.Getenv("SCRCPYCAT_TEST_CONTROLPLANE")
	signaling := os.Getenv("SCRCPYCAT_TEST_SIGNALING")
	if serial == "" || endpoint == "" || signaling == "" {
		t.Skip("requires explicit test device, control plane, and signaling")
	}
	config := Config{Mode: ModeDevice, ADBPath: "adb", ADBServer: adbclient.DefaultServer,
		ArtifactDir: "/opt/scrcpycat/agent/bin", ScrcpyJar: "/opt/scrcpycat/scrcpy/scrcpy-server",
		ControlPlaneURL: endpoint, SignalingURL: signaling, DeploymentToken: os.Getenv("SCRCPYCAT_DEPLOYMENT_TOKEN")}
	config.ResolveDefaults()
	devices, err := adbclient.New(config.ADBServer)
	if err != nil {
		t.Fatal(err)
	}
	r := &runner{config: config, devices: devices, client: &http.Client{Timeout: 15 * time.Second}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	serials, err := r.connectedSerials(ctx)
	t.Logf("serials=%v error=%v healthy=%t", serials, err, r.agentHealthy(ctx, serial))
	abi, err := r.deviceABI(ctx, serial)
	t.Logf("ABI=%q error=%v", abi, err)
	if err := r.ensureAgent(ctx, serial); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if r.agentHealthy(ctx, serial) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("standalone Agent did not become healthy")
}
