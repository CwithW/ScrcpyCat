package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConnectionAndPreviewWakeHaveIndependentLifetimes(t *testing.T) {
	for _, tc := range []struct {
		name           string
		preview        bool
		raw            map[string]any
		awake, powerOn string
	}{
		{"connection default", false, nil, "true", "true"},
		{"preview default", true, nil, "false", "false"},
		{"connection disabled", false, map[string]any{"stay_awake": false}, "false", "true"},
		{"preview enabled", true, map[string]any{"stay_awake": true}, "true", "true"},
		{"websocket connection", true, map[string]any{"preview": false}, "true", "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options, err := parseStreamOptions(tc.raw, tc.preview)
			if err != nil || options.Values["keep_active"] != tc.awake || options.Values["power_on"] != tc.powerOn {
				t.Fatal(options, err)
			}
			args := strings.Join(captureArguments(Config{ScrcpyVersion: "4.1"}, "test", options, false), " ")
			if strings.Contains(args, "stay_awake=") || !strings.Contains(args, "keep_active="+tc.awake) || !strings.Contains(args, "cleanup=false") {
				t.Fatal("wake still depends on cleanup or charging", args)
			}
		})
	}
}

func TestConnectionOptionsPreserveUnlimitedAndDebug(t *testing.T) {
	options, err := parseStreamOptions(map[string]any{
		"max_fps": float64(0), "fps": float64(60), "max_size": float64(0), "debug": true,
		"power_off": true, "camera_orientation": "90", "video_codec_options": "i-frame-interval=2",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(captureArguments(Config{}, "test", options, false), " ")
	for _, expected := range []string{"max_fps=0", "max_size=0", "log_level=debug", "capture_orientation=90", "i-frame-interval=2"} {
		if !strings.Contains(args, expected) {
			t.Fatal(expected, args)
		}
	}
	if !options.PowerOff {
		t.Fatal("display power option dropped")
	}
	client := &client{}
	client.setSnapshotInterval(map[string]any{"snapshot_interval": float64(-1)})
	if client.snapshotInterval.Load() != -1 {
		t.Fatal("per-device snapshot interval ignored")
	}
	client.setSnapshotInterval(map[string]any{"snapshotInterval": float64(30)})
	client.setSnapshotInterval(map[string]any{"snapshotInterval": 1.5})
	if client.snapshotInterval.Load() != 30 {
		t.Fatal("invalid interval replaced the saved setting")
	}
}

func TestDisconnectRestoresNonWakingPreview(t *testing.T) {
	jar := filepath.Join(t.TempDir(), "scrcpy-server.jar")
	if err := os.WriteFile(jar, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	transport := &scriptedCaptureTransport{processes: []*scriptedCaptureProcess{
		{done: make(chan struct{})}, {done: make(chan struct{})}, {done: make(chan struct{})},
	}}
	backend := localBackend()
	backend.capture = transport
	bridge := newScrcpyBridge(Config{ScrcpyJar: jar}, backend)
	cameraBridge := newScrcpyBridge(Config{ScrcpyJar: jar}, backend)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newMediaManager(ctx, nil, bridge, cameraBridge, nil)
	t.Cleanup(func() { m.Close(); _ = bridge.Stop() })
	preview, _ := parseStreamOptions(nil, true)
	if err := m.StartPreview(ctx, preview); err != nil {
		t.Fatal(err)
	}
	connected, _ := parseStreamOptions(nil, false)
	m.display.sessions["viewer"] = webRTCSession{withVideo: true, captureOptions: connected, order: 1}
	m.StopIdle()
	if bridge.currentOptions().Values["keep_active"] != "true" {
		t.Fatal("connection did not take over wake policy")
	}
	delete(m.display.sessions, "viewer")
	m.StopIdle()
	if options := bridge.currentOptions(); !options.Preview || options.Values["keep_active"] != "false" {
		t.Fatal("last connection kept the preview awake", options)
	}
	if err := m.StopPreview(ctx); err != nil || bridge.process != nil {
		t.Fatal("last preview did not stop capture", err)
	}
}

func TestCompatibleEncoderUsesActualSPSProfile(t *testing.T) {
	for _, profile := range []string{"42801f", "4d0029", "64002a"} {
		var bytes []byte
		switch profile {
		case "42801f":
			bytes = []byte{0x42, 0x80, 0x1f}
		case "4d0029":
			bytes = []byte{0x4d, 0, 0x29}
		case "64002a":
			bytes = []byte{0x64, 0, 0x2a}
		}
		data := append([]byte{0, 0, 0, 1, 0x67}, bytes...)
		if actual := h264ProfileLevel(data); actual != profile {
			t.Fatal(actual, profile)
		}
		if !strings.Contains(h264Codec(profile).SDPFmtpLine, "profile-level-id="+profile) {
			t.Fatal("SDP advertises a different profile")
		}
	}
}

func TestAudioBeforeWebSocketPreviewUsesNewSessionSettings(t *testing.T) {
	jar := filepath.Join(t.TempDir(), "scrcpy-server.jar")
	if err := os.WriteFile(jar, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	transport := &scriptedCaptureTransport{processes: []*scriptedCaptureProcess{
		{done: make(chan struct{})}, {done: make(chan struct{})},
	}}
	backend := localBackend()
	backend.capture = transport
	bridge := newScrcpyBridge(Config{ScrcpyJar: jar}, backend)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newMediaManager(ctx, nil, bridge, newScrcpyBridge(Config{ScrcpyJar: jar}, backend), nil)
	t.Cleanup(func() { m.Close(); _ = bridge.Stop() })
	previous, _ := parseStreamOptions(map[string]any{"max_size": float64(720)}, false)
	if err := bridge.Start(ctx, previous); err != nil {
		t.Fatal(err)
	}
	if err := bridge.Stop(); err != nil {
		t.Fatal(err)
	}
	raw := map[string]any{"preview": false, "max_size": float64(960), "max_fps": float64(15), "audio": true, "audio_source": "playback", "stay_awake": false}
	if err := m.StartAudio(ctx, raw); err != nil {
		t.Fatal(err)
	}
	current := bridge.currentOptions()
	if current.Values["max_size"] != "960" || current.Values["max_fps"] != "15" || current.Values["keep_active"] != "false" || current.Values["audio_source"] != "playback" {
		t.Fatal("audio reused settings from a stopped capture", current)
	}
	preview, _ := parseStreamOptions(raw, true)
	if err := m.StartPreview(ctx, preview); err != nil {
		t.Fatal(err)
	}
	if transport.count() != 2 {
		t.Fatal("WebSocket video restarted the audio-first capture", transport.count())
	}
}

func TestReservedCapabilitiesDoNotMasqueradeAsClipboard(t *testing.T) {
	for channel, capability := range map[string]string{"gps": "gps_injection", "sensor": "sensor_injection", "camera": "camera_injection"} {
		if got := injectionCapability(map[string]any{"channel": channel}); got != capability {
			t.Fatal(channel, got)
		}
	}
	if got := injectionCapability(map[string]any{"channel": "clipboard", "payload": map[string]any{"type": "set_clipboard"}}); got != "" {
		t.Fatal("clipboard was disabled with sensor injection", got)
	}
}

func TestSharedCaptureRetainsOtherViewersAudioAndWake(t *testing.T) {
	m := newWebRTCManager(nil, nil, nil)
	first, _ := parseStreamOptions(map[string]any{"audio": true, "audio_source": "playback", "audio_dup": true}, false)
	second, _ := parseStreamOptions(map[string]any{"stay_awake": false}, false)
	m.sessions["first"] = webRTCSession{withVideo: true, withAudio: true, captureOptions: first, order: 1}
	m.sessions["second"] = webRTCSession{withVideo: true, captureOptions: second, order: 2}
	latest, _ := m.LatestVideoOptions()
	merged := m.mergeCaptureNeeds(latest)
	if !merged.Audio || merged.Values["audio_source"] != "playback" || merged.Values["audio_dup"] != "true" || merged.Values["keep_active"] != "true" {
		t.Fatal("new viewer disabled another viewer's requirements", merged)
	}
	delete(m.sessions, "first")
	latest, _ = m.LatestVideoOptions()
	merged = m.mergeCaptureNeeds(latest)
	if merged.Audio || merged.Values["keep_active"] != "false" {
		t.Fatal("closed viewer's requirements were retained", merged)
	}
}

func TestActiveVideoAllowsLevelChangeButRejectsProfileChange(t *testing.T) {
	m := newWebRTCManager(nil, nil, nil)
	if err := m.SetVideoCodec("42801f"); err != nil {
		t.Fatal(err)
	}
	m.sessions["viewer"] = webRTCSession{withVideo: true}
	track := m.track
	if err := m.SetVideoCodec("428029"); err != nil || m.track != track {
		t.Fatal("resolution change replaced the active track", err)
	}
	if err := m.SetVideoCodec("640029"); err == nil || m.track != track {
		t.Fatal("incompatible profile replaced the active track")
	}
}
