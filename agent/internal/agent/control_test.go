package agent

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestEncodeControlEvents(t *testing.T) {
	key, err := encodeControlEvent(map[string]any{"type": "inject_keycode", "action": float64(0), "keycode": float64(4)})
	if err != nil || len(key) != 14 || key[0] != controlInjectKeycode || binary.BigEndian.Uint32(key[2:6]) != 4 {
		t.Fatalf("invalid key event: %x, %v", key, err)
	}
	touch, err := encodeControlEvent(map[string]any{"type": "touch", "action": float64(0), "x": float64(10), "y": float64(20), "w": float64(1080), "h": float64(1920), "id": float64(7)})
	if err != nil || len(touch) != 32 || touch[0] != controlInjectTouch || binary.BigEndian.Uint64(touch[2:10]) != 7 {
		t.Fatalf("invalid touch event: %x, %v", touch, err)
	}
	text, err := encodeControlEvent(map[string]any{"type": "inject_text", "text": "hello"})
	if err != nil || text[0] != 9 || text[9] != 1 || string(text[14:]) != "hello" {
		t.Fatalf("invalid text event: %x, %v", text, err)
	}
}

func TestMouseTouchAndUnicodePaste(t *testing.T) {
	data, err := encodeControlEvent(map[string]any{"type": "touch", "action": float64(0), "id": float64(-1), "x": float64(10), "y": float64(20), "w": float64(1080), "h": float64(1920)})
	if err != nil || int64(binary.BigEndian.Uint64(data[2:10])) != -1 || binary.BigEndian.Uint32(data[28:32]) != 1 {
		t.Fatalf("invalid mouse event: %x, %v", data, err)
	}
	data, err = encodeControlEvent(map[string]any{"type": "inject_text", "text": "中文输入"})
	if err != nil || data[0] != 9 || data[9] != 1 || string(data[14:]) != "中文输入" {
		t.Fatalf("invalid Unicode paste: %x, %v", data, err)
	}
	if got := string(truncateUTF8("a中b", 4)); got != "a中" {
		t.Fatal(got)
	}
	if got := string(truncateUTF8("a中b", 3)); got != "a" {
		t.Fatal(got)
	}
}

func TestDeviceClipboardProtocol(t *testing.T) {
	var wire bytes.Buffer
	wire.WriteByte(0)
	binary.Write(&wire, binary.BigEndian, uint32(len("clipboard中文")))
	wire.WriteString("clipboard中文")
	wire.WriteByte(1)
	binary.Write(&wire, binary.BigEndian, uint64(42))
	message, err := readDeviceMessage(&wire)
	if err != nil || message["text"] != "clipboard中文" {
		t.Fatalf("%v: %v", message, err)
	}
	message, err = readDeviceMessage(&wire)
	if err != nil || message != nil || wire.Len() != 0 {
		t.Fatalf("%v: %v", message, err)
	}
}
