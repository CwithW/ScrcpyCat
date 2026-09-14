package agent

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestReadScrcpyPacketAndBuildPreviewFrame(t *testing.T) {
	payload := []byte{0, 0, 0, 1, 0x65, 1, 2, 3}
	stream := make([]byte, 1+scrcpyDeviceNameSize+4+12+len(payload))
	stream[0] = 0
	copy(stream[1:1+scrcpyDeviceNameSize], "test-device")
	copy(stream[1+scrcpyDeviceNameSize:5+scrcpyDeviceNameSize], "h264")
	binary.BigEndian.PutUint64(stream[5+scrcpyDeviceNameSize:13+scrcpyDeviceNameSize], scrcpyKeyFrameFlag|12345)
	binary.BigEndian.PutUint32(stream[13+scrcpyDeviceNameSize:17+scrcpyDeviceNameSize], uint32(len(payload)))
	copy(stream[17+scrcpyDeviceNameSize:], payload)

	reader := bytes.NewReader(stream)
	if err := readScrcpyDummy(reader); err != nil {
		t.Fatalf("read dummy byte: %v", err)
	}
	name, err := readScrcpyDeviceName(reader)
	if err != nil || name != "test-device" {
		t.Fatalf("device name = %q, err = %v", name, err)
	}
	codec, err := readScrcpyCodec(reader, false)
	if err != nil || codec != "h264" {
		t.Fatalf("codec = %q, err = %v", codec, err)
	}
	packet, err := readScrcpyPacket(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !packet.KeyFrame || packet.PTS != 12345 || !bytes.Equal(packet.Data, payload) {
		t.Fatalf("unexpected packet: %#v", packet)
	}
	frame, err := previewFrame("device-1", packet)
	if err != nil {
		t.Fatal(err)
	}
	if string(frame[:4]) != "PREV" || frame[36] != 1 || binary.BigEndian.Uint64(frame[37:45]) != 12345 {
		t.Fatalf("invalid PREV header: %v", frame[:49])
	}
}

func TestReadScrcpySessionPacket(t *testing.T) {
	stream := make([]byte, scrcpyPacketHeaderSize)
	binary.BigEndian.PutUint32(stream[:4], 0x80000001)
	binary.BigEndian.PutUint32(stream[4:8], 1080)
	binary.BigEndian.PutUint32(stream[8:12], 1920)
	packet, err := readScrcpyPacket(bytes.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if !packet.Session || !packet.ClientResized || packet.Width != 1080 || packet.Height != 1920 {
		t.Fatalf("unexpected session packet: %#v", packet)
	}
}

func TestAudioFrameEnvelope(t *testing.T) {
	frame, err := audioFrame("device-1", mediaPacket{PTS: 12345, Data: []byte{0xf8, 0xff, 0xfe}})
	if err != nil {
		t.Fatal(err)
	}
	if string(frame[:4]) != "OPUS" || frame[36] != 2 || binary.BigEndian.Uint64(frame[37:45]) != 12345 ||
		binary.BigEndian.Uint32(frame[45:49]) != 3 || !bytes.Equal(frame[49:], []byte{0xf8, 0xff, 0xfe}) {
		t.Fatalf("invalid audio envelope %x", frame)
	}
}
