package agent

import (
	"encoding/binary"
	"github.com/pion/webrtc/v4"
	"strings"
	"testing"
)

func TestBitrateLimitsAndBrowserControl(t *testing.T) {
	options, err := parseBitrateOptions(map[string]any{"bwe": true, "bitrate": float64(4000000), "min_bitrate": float64(500000), "max_bitrate": float64(2000000)})
	if err != nil || options.Initial != 2000000 {
		t.Fatalf("%+v %v", options, err)
	}
	if _, err := parseBitrateOptions(map[string]any{"bwe": true, "min_bitrate": float64(2000000), "max_bitrate": float64(500000)}); err == nil {
		t.Fatal("inverted bitrate limits accepted")
	}
	event := map[string]any{"type": "set_video_bitrate", "bitrate": 2000000}
	if browserControlAllowed(event) {
		t.Fatal("browser can bypass encoder policy")
	}
	wire, err := encodeControlEvent(event)
	if err != nil || wire[0] != 23 || binary.BigEndian.Uint32(wire[1:]) != 2000000 {
		t.Fatalf("%v %v", wire, err)
	}
	if !browserControlAllowed(map[string]any{"type": "touch"}) {
		t.Fatal("regular control blocked")
	}
	stream, err := parseStreamOptions(map[string]any{"video_codec_options": "bitrate-mode=2,profile=8"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(stream.arguments(), " "), "video_codec_options=bitrate-mode=2,profile=8") {
		t.Fatal("explicit encoder options were overridden")
	}
}

func TestSharedEncoderUsesSlowestViewer(t *testing.T) {
	fast, slow := &webrtc.PeerConnection{}, &webrtc.PeerConnection{}
	var applied int
	manager := &webRTCManager{sessions: map[string]webRTCSession{
		"fast": {peer: fast, withVideo: true, bitrate: bitrateOptions{Enabled: true, Minimum: 500000, Maximum: 8000000}, targetBitrate: 4000000},
		"slow": {peer: slow, withVideo: true, bitrate: bitrateOptions{Initial: 1000000}, targetBitrate: 1000000},
	}, onInput: func(event map[string]any) error { applied = event["bitrate"].(int); return nil }}
	manager.updateBitrate("fast", fast, 6000000)
	if applied != 1000000 {
		t.Fatalf("applied %d, want slow viewer limit", applied)
	}
	manager.updateBitrate("fast", &webrtc.PeerConnection{}, 500000)
	if applied != 1000000 {
		t.Fatal("stale peer changed active encoder")
	}
}
