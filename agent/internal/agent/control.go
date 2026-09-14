package agent

import (
	"encoding/binary"
	"errors"
	"math"
	"unicode/utf8"
)

const (
	controlInjectKeycode = 0
	controlInjectText    = 1
	controlInjectTouch   = 2
	controlInjectScroll  = 3
)

// Browser input cannot change encoder policy; bitrate updates belong to GCC.
func browserControlAllowed(event map[string]any) bool {
	switch stringField(event, "type") {
	case "touch", "inject_keycode", "inject_text", "set_clipboard", "get_clipboard",
		"reset_video", "rotate_device", "set_display_power", "camera_zoom_in",
		"camera_zoom_out", "camera_torch", "scroll", "inject_scroll":
		return true
	default:
		return false
	}
}

func encodeControlEvent(event map[string]any) ([]byte, error) {
	switch stringField(event, "type") {
	case "touch":
		return encodeTouch(event)
	case "inject_keycode":
		return encodeKeycode(event)
	case "inject_text":
		// Android KeyCharacterMap cannot inject arbitrary Unicode. Clipboard
		// paste supports IME text with the same shell privileges.
		return encodeClipboard(map[string]any{"text": stringField(event, "text"), "paste": true})
	case "set_clipboard":
		return encodeClipboard(event)
	case "get_clipboard":
		return []byte{8, 0}, nil
	case "reset_video":
		return []byte{17}, nil
	case "request_keyframe":
		return []byte{24}, nil
	case "set_video_bitrate":
		bitrate, err := uint32Field(event, "bitrate")
		if err != nil || bitrate < 100000 || bitrate > 100000000 {
			return nil, errors.New("invalid video bitrate")
		}
		data := make([]byte, 5)
		data[0] = 23
		binary.BigEndian.PutUint32(data[1:], bitrate)
		return data, nil
	case "rotate_device":
		return []byte{11}, nil
	case "set_display_power":
		on, _ := event["on"].(bool)
		if on {
			return []byte{10, 1}, nil
		}
		return []byte{10, 0}, nil
	case "camera_zoom_in":
		return []byte{19}, nil
	case "camera_zoom_out":
		return []byte{20}, nil
	case "camera_torch":
		on, _ := event["on"].(bool)
		if on {
			return []byte{18, 1}, nil
		}
		return []byte{18, 0}, nil
	case "scroll", "inject_scroll":
		return encodeScroll(event)
	default:
		return nil, errors.New("unsupported scrcpy control event")
	}
}

func encodeKeycode(event map[string]any) ([]byte, error) {
	action, err := uint8Field(event, "action")
	if err != nil {
		return nil, err
	}
	keycode, err := uint32Field(event, "keycode")
	if err != nil {
		return nil, err
	}
	repeat, err := uint32FieldDefault(event, "repeat", 0)
	if err != nil {
		return nil, err
	}
	meta, err := uint32FieldDefault(event, "meta", 0)
	if err != nil {
		return nil, err
	}
	data := make([]byte, 14)
	data[0] = controlInjectKeycode
	data[1] = action
	binary.BigEndian.PutUint32(data[2:6], keycode)
	binary.BigEndian.PutUint32(data[6:10], repeat)
	binary.BigEndian.PutUint32(data[10:14], meta)
	return data, nil
}

func encodeText(event map[string]any) ([]byte, error) {
	text := stringField(event, "text")
	if text == "" {
		return nil, errors.New("control text is empty")
	}
	contents := truncateUTF8(text, 300)
	data := make([]byte, 5+len(contents))
	data[0] = controlInjectText
	binary.BigEndian.PutUint32(data[1:5], uint32(len(contents)))
	copy(data[5:], contents)
	return data, nil
}

func encodeTouch(event map[string]any) ([]byte, error) {
	action, err := uint8Field(event, "action")
	if err != nil {
		return nil, err
	}
	pointerID, err := pointerIDField(event)
	if err != nil {
		return nil, err
	}
	x, y, width, height, err := positionFromEvent(event)
	if err != nil {
		return nil, err
	}
	pressure := floatFieldDefault(event, "pressure", 1)
	if action == 1 || action == 3 {
		pressure = 0
	}
	data := make([]byte, 32)
	data[0] = controlInjectTouch
	data[1] = action
	binary.BigEndian.PutUint64(data[2:10], pointerID)
	writePosition(data[10:22], x, y, width, height)
	binary.BigEndian.PutUint16(data[22:24], uint16(math.Round(clamp(pressure, 0, 1)*65535)))
	// Mouse pointer (-1) needs primary-button metadata for DOWN/MOVE/UP.
	if pointerID == math.MaxUint64 {
		binary.BigEndian.PutUint32(data[24:28], 1)
		if action != 1 && action != 3 {
			binary.BigEndian.PutUint32(data[28:32], 1)
		}
	}
	return data, nil
}

func encodeScroll(event map[string]any) ([]byte, error) {
	x, y, width, height, err := positionFromEvent(event)
	if err != nil {
		return nil, err
	}
	horizontal := floatFieldDefault(event, "scroll_h", floatFieldDefault(event, "scrollH", 0))
	vertical := floatFieldDefault(event, "scroll_v", floatFieldDefault(event, "scrollV", 0))
	data := make([]byte, 21)
	data[0] = controlInjectScroll
	writePosition(data[1:13], x, y, width, height)
	binary.BigEndian.PutUint16(data[13:15], uint16(int16(math.Round(clamp(horizontal/16, -1, 1)*32767))))
	binary.BigEndian.PutUint16(data[15:17], uint16(int16(math.Round(clamp(vertical/16, -1, 1)*32767))))
	return data, nil
}

func writePosition(target []byte, x, y uint32, width, height uint16) {
	binary.BigEndian.PutUint32(target[0:4], x)
	binary.BigEndian.PutUint32(target[4:8], y)
	binary.BigEndian.PutUint16(target[8:10], width)
	binary.BigEndian.PutUint16(target[10:12], height)
}

func positionFromEvent(event map[string]any) (uint32, uint32, uint16, uint16, error) {
	x, err := uint32Field(event, "x")
	if err != nil {
		return 0, 0, 0, 0, err
	}
	y, err := uint32Field(event, "y")
	if err != nil {
		return 0, 0, 0, 0, err
	}
	width, err := uint16Field(event, "w")
	if err != nil || width == 0 {
		return 0, 0, 0, 0, errors.New("invalid control width")
	}
	height, err := uint16Field(event, "h")
	if err != nil || height == 0 {
		return 0, 0, 0, 0, errors.New("invalid control height")
	}
	return x, y, width, height, nil
}

func uint8Field(event map[string]any, key string) (uint8, error) {
	value, err := uint64FieldDefault(event, key, math.MaxUint64)
	if err != nil || value > math.MaxUint8 {
		return 0, errors.New("invalid control " + key)
	}
	return uint8(value), nil
}

func uint16Field(event map[string]any, key string) (uint16, error) {
	value, err := uint64FieldDefault(event, key, math.MaxUint64)
	if err != nil || value > math.MaxUint16 {
		return 0, errors.New("invalid control " + key)
	}
	return uint16(value), nil
}

func uint32Field(event map[string]any, key string) (uint32, error) {
	value, err := uint64FieldDefault(event, key, math.MaxUint64)
	if err != nil || value > math.MaxUint32 {
		return 0, errors.New("invalid control " + key)
	}
	return uint32(value), nil
}

func uint32FieldDefault(event map[string]any, key string, fallback uint32) (uint32, error) {
	value, err := uint64FieldDefault(event, key, uint64(fallback))
	if err != nil || value > math.MaxUint32 {
		return 0, errors.New("invalid control " + key)
	}
	return uint32(value), nil
}

func uint64FieldDefault(event map[string]any, key string, fallback uint64) (uint64, error) {
	value, ok := event[key]
	if !ok {
		return fallback, nil
	}
	switch typed := value.(type) {
	case float64:
		if typed < 0 || typed != math.Trunc(typed) || typed > math.MaxUint64 {
			return 0, errors.New("invalid control " + key)
		}
		return uint64(typed), nil
	case int:
		if typed < 0 {
			return 0, errors.New("invalid control " + key)
		}
		return uint64(typed), nil
	case uint64:
		return typed, nil
	case uint32:
		return uint64(typed), nil
	default:
		return 0, errors.New("invalid control " + key)
	}
}

func floatFieldDefault(event map[string]any, key string, fallback float64) float64 {
	value, ok := event[key]
	if !ok {
		return fallback
	}
	if typed, ok := value.(float64); ok && !math.IsNaN(typed) && !math.IsInf(typed, 0) {
		return typed
	}
	return fallback
}

func truncateUTF8(value string, max int) []byte {
	contents := []byte(value)
	if len(contents) > max {
		contents = contents[:max]
	}
	for len(contents) > 0 && !utf8.Valid(contents) {
		contents = contents[:len(contents)-1]
	}
	return contents
}

func pointerIDField(event map[string]any) (uint64, error) {
	// scrcpy uses signed IDs: -1 for mouse, -2 for a virtual finger.
	switch id := event["id"].(type) {
	case float64:
		if id == -1 {
			return math.MaxUint64, nil
		}
		if id == -2 {
			return math.MaxUint64 - 1, nil
		}
	case int:
		if id == -1 {
			return math.MaxUint64, nil
		}
		if id == -2 {
			return math.MaxUint64 - 1, nil
		}
	}
	return uint64FieldDefault(event, "id", 0)
}

func encodeClipboard(event map[string]any) ([]byte, error) {
	contents := truncateUTF8(stringField(event, "text"), (1<<18)-14)
	data := make([]byte, 14+len(contents))
	data[0] = 9
	sequence, err := uint64FieldDefault(event, "sequence", 0)
	if err != nil {
		return nil, err
	}
	binary.BigEndian.PutUint64(data[1:9], sequence)
	if paste, _ := event["paste"].(bool); paste {
		data[9] = 1
	}
	binary.BigEndian.PutUint32(data[10:14], uint32(len(contents)))
	copy(data[14:], contents)
	return data, nil
}

func clamp(value, min, max float64) float64 {
	return math.Max(min, math.Min(max, value))
}
