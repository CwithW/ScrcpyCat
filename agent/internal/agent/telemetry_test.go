package agent

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestTelemetryParsers(t *testing.T) {
	total, idle := cpuCounters("cpu 100 20 30 400 50 6 7 8 99 88\ncpu0 1 2 3 4\n")
	if total != 621 || idle != 450 {
		t.Fatalf("cpu counters: %d %d", total, idle)
	}
	rx, tx := networkCounters("lo: 999 0 0 0 0 0 0 0 999\nwlan0: 1000 0 0 0 0 0 0 0 2000\nrmnet0: 10 0 0 0 0 0 0 0 20\n")
	if rx != 1010 || tx != 2020 {
		t.Fatalf("network counters: %d %d", rx, tx)
	}
	if got := numericLines("  temperature: 321\n  MemTotal: 1024 kB\n  AC powered: true\n"); got["temperature"] != 321 || got["MemTotal"] != 1024 {
		t.Fatal(got)
	}
}

func TestSnapshotKeepsAspectRatio(t *testing.T) {
	var source bytes.Buffer
	if err := png.Encode(&source, image.NewNRGBA(image.Rect(0, 0, 100, 300))); err != nil {
		t.Fatal(err)
	}
	small, err := thumbnailPNG(source.Bytes(), 100)
	if err != nil {
		t.Fatal(err)
	}
	dimensions, err := png.DecodeConfig(bytes.NewReader(small))
	if err != nil || dimensions.Width != 33 || dimensions.Height != 100 {
		t.Fatalf("%+v, %v", dimensions, err)
	}
	if _, err := thumbnailPNG([]byte("not png"), 100); err == nil {
		t.Fatal("invalid screenshot accepted")
	}
}

func TestInstanceLockAndPIDIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.pid")
	release, err := acquireInstance(path)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if second, err := acquireInstance(path); err == nil {
		second()
		t.Fatal("duplicate instance acquired lock")
	}
	if !isAgentProcess(strconv.Itoa(os.Getpid())) {
		t.Fatal("self process not recognized")
	}
	for _, pid := range []string{"1", "1 2", "-1", "not-a-pid"} {
		if isAgentProcess(pid) {
			t.Fatalf("invalid PID recognized: %q", pid)
		}
	}
}

func TestMediaEnumeration(t *testing.T) {
	displays, cameras := parseMediaInfo("    --display-id=0    (1440x3200)\n    --camera-id=2    (back, 3280x2464, fps={15, 30}, zoom-range=[0.6, 10])\n")
	if len(displays) != 1 || displays[0]["x_res"] != 1440 || displays[0]["y_res"] != 3200 {
		t.Fatal(displays)
	}
	if len(cameras) != 1 || cameras[0]["id"] != "2" || cameras[0]["zoom_min"] != 0.6 {
		t.Fatal(cameras)
	}
}
