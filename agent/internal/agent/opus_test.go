package agent

import (
	"testing"

	"github.com/pion/rtp"
)

type recordingOpusPacketizer struct {
	rtp.Packetizer
	timestamps []uint32
}

func (p *recordingOpusPacketizer) Packetize(payload []byte, samples uint32) []*rtp.Packet {
	packets := p.Packetizer.Packetize(payload, samples)
	for _, packet := range packets {
		p.timestamps = append(p.timestamps, packet.Timestamp)
	}
	return packets
}

func TestOpusRTPClockIgnoresCaptureSchedulingJitter(t *testing.T) {
	manager := newWebRTCManager(nil, nil, nil)
	recorder := &recordingOpusPacketizer{Packetizer: manager.audioPacketizer}
	manager.audioPacketizer = recorder
	// RFC 6716: this TOC describes one 20 ms frame. scrcpy may recreate PTS
	// from wall time, including encoder scheduling jitter and capture restarts.
	for _, pts := range []uint64{1000000, 1012000, 1041000, 1060000, 50000, 70000} {
		manager.PushOpus(mediaPacket{PTS: pts, Data: []byte{0xf8, 0xff, 0xfe}})
	}
	if len(recorder.timestamps) != 6 {
		t.Fatalf("got %d packets", len(recorder.timestamps))
	}
	for i := 1; i < len(recorder.timestamps); i++ {
		if delta := recorder.timestamps[i] - recorder.timestamps[i-1]; delta != 960 {
			t.Fatalf("packet %d advances RTP clock by %d samples, want 960", i, delta)
		}
	}
}

func TestOpusPacketDurations(t *testing.T) {
	for _, tc := range []struct {
		packet []byte
		want   uint32
	}{
		{[]byte{0x80}, 120}, {[]byte{0x88}, 240},
		{[]byte{0x90}, 480}, {[]byte{0xf8, 0xff, 0xfe}, 960},
		{[]byte{0x10}, 1920}, {[]byte{0x18}, 2880},
		{[]byte{0x60}, 480}, {[]byte{0x68}, 960},
		{[]byte{0x99}, 1920}, {[]byte{0x9a}, 1920},
		{[]byte{0x9b, 3}, 2880}, {[]byte{0x9b, 6}, 5760},
		{nil, 0}, {[]byte{0x9b}, 0}, {[]byte{0x9b, 0}, 0},
		{[]byte{0x9b, 7}, 0}, {[]byte{0x83, 49}, 0},
	} {
		if got := opusPacketSamples(tc.packet); got != tc.want {
			t.Errorf("packet %x: got %d samples, want %d", tc.packet, got, tc.want)
		}
	}
}
