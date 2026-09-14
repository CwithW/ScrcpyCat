package agent

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	scrcpyDeviceNameSize   = 64
	scrcpyPacketHeaderSize = 12
	scrcpySessionFlag      = uint64(1) << 63
	scrcpyConfigFlag       = uint64(1) << 62
	scrcpyKeyFrameFlag     = uint64(1) << 61
	scrcpyPTSBitmask       = scrcpyKeyFrameFlag - 1
	maxScrcpyPacketSize    = 32 << 20
)

type mediaPacket struct {
	PTS           uint64
	KeyFrame      bool
	Config        bool
	Session       bool
	Width         uint32
	Height        uint32
	ClientResized bool
	Data          []byte
}

func readScrcpyCodec(reader io.Reader, dummyByte bool) (string, error) {
	if dummyByte {
		if err := readScrcpyDummy(reader); err != nil {
			return "", err
		}
	}
	var raw [4]byte
	if _, err := io.ReadFull(reader, raw[:]); err != nil {
		return "", fmt.Errorf("read scrcpy codec: %w", err)
	}
	return string(raw[:]), nil
}

func readScrcpyDummy(reader io.Reader) error {
	var dummy [1]byte
	if _, err := io.ReadFull(reader, dummy[:]); err != nil {
		return fmt.Errorf("read scrcpy dummy byte: %w", err)
	}
	return nil
}

func readScrcpyDeviceName(reader io.Reader) (string, error) {
	var raw [scrcpyDeviceNameSize]byte
	if _, err := io.ReadFull(reader, raw[:]); err != nil {
		return "", fmt.Errorf("read scrcpy device metadata: %w", err)
	}
	for index, value := range raw {
		if value == 0 {
			return string(raw[:index]), nil
		}
	}
	return string(raw[:]), nil
}

func readScrcpyPacket(reader io.Reader) (mediaPacket, error) {
	var header [scrcpyPacketHeaderSize]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return mediaPacket{}, err
	}
	ptsFlags := binary.BigEndian.Uint64(header[:8])
	if ptsFlags&scrcpySessionFlag != 0 {
		sessionFlags := binary.BigEndian.Uint32(header[:4])
		return mediaPacket{
			Session:       true,
			ClientResized: sessionFlags&1 != 0,
			Width:         binary.BigEndian.Uint32(header[4:8]),
			Height:        binary.BigEndian.Uint32(header[8:12]),
		}, nil
	}
	length := binary.BigEndian.Uint32(header[8:])
	if length == 0 || length > maxScrcpyPacketSize {
		return mediaPacket{}, fmt.Errorf("invalid scrcpy packet size %d", length)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(reader, data); err != nil {
		return mediaPacket{}, fmt.Errorf("read scrcpy packet payload: %w", err)
	}
	return mediaPacket{
		PTS:      ptsFlags & scrcpyPTSBitmask,
		KeyFrame: ptsFlags&scrcpyKeyFrameFlag != 0,
		Config:   ptsFlags&scrcpyConfigFlag != 0,
		Data:     data,
	}, nil
}

func previewFrame(deviceID string, packet mediaPacket) ([]byte, error) {
	if len(deviceID) == 0 || len(deviceID) > 32 {
		return nil, errors.New("preview device ID must be 1 to 32 bytes")
	}
	if len(packet.Data) == 0 {
		return nil, errors.New("preview packet is empty")
	}
	frame := make([]byte, 49+len(packet.Data))
	copy(frame[:4], "PREV")
	copy(frame[4:36], deviceID)
	if packet.KeyFrame {
		frame[36] = 1
	}
	binary.BigEndian.PutUint64(frame[37:45], packet.PTS)
	binary.BigEndian.PutUint32(frame[45:49], uint32(len(packet.Data)))
	copy(frame[49:], packet.Data)
	return frame, nil
}

// Audio uses the same device-bound binary envelope as video, with an OPUS
// magic and channel count in byte 36. Opus always decodes at 48 kHz here.
func audioFrame(deviceID string, packet mediaPacket) ([]byte, error) {
	frame, err := previewFrame(deviceID, packet)
	if err != nil {
		return nil, err
	}
	copy(frame[:4], "OPUS")
	frame[36] = 2
	return frame, nil
}
