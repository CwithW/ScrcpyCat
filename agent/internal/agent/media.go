package agent

import (
	"context"
	"fmt"
	"strconv"
	"sync/atomic"
)

// Display and camera captures have separate scrcpy processes and RTP tracks.
// Multiple viewers of one source share the encoder, but never replace the
// other source (for example a camera subview alongside the phone screen).
type mediaManager struct {
	display, camera             *webRTCManager
	displayBridge, cameraBridge *scrcpyBridge
	wsAudioActive               atomic.Bool
	wsAudioOptions              map[string]string
}

func newMediaManager(writer *lockedWriter, displayBridge, cameraBridge *scrcpyBridge, terminals *terminalManager) *mediaManager {
	manager := &mediaManager{
		display:       newWebRTCManager(writer, displayBridge.EnqueueControl, terminals),
		camera:        newWebRTCManager(writer, cameraBridge.EnqueueControl, terminals),
		displayBridge: displayBridge, cameraBridge: cameraBridge,
	}
	displayBridge.SetAudioPublisher(func(packet mediaPacket) {
		manager.display.PushOpus(packet)
		if manager.wsAudioActive.Load() {
			if frame, err := audioFrame(displayBridge.config.DeviceID, packet); err == nil {
				_ = writer.writeBinary(frame)
			}
		}
	})
	cameraBridge.SetPreviewPublisher(manager.camera.PushH264)
	cameraBridge.SetAudioPublisher(manager.camera.PushOpus)
	cameraBridge.SetDeviceMessagePublisher(manager.camera.PublishDeviceMessage)
	displayBridge.SetErrorPublisher(manager.display.PublishError)
	cameraBridge.SetErrorPublisher(manager.camera.PublishError)
	return manager
}

func (m *mediaManager) SetICEServers(raw any) {
	m.display.SetICEServers(raw)
	m.camera.SetICEServers(raw)
}
func (m *mediaManager) HasSessions() bool { return m.display.HasSessions() || m.camera.HasSessions() }
func (m *mediaManager) CloseSession(clientID string) {
	m.display.CloseSession(clientID)
	m.camera.CloseSession(clientID)
}
func (m *mediaManager) Close()                      { m.display.Close(); m.camera.Close() }
func (m *mediaManager) PushH264(packet mediaPacket) { m.display.PushH264(packet) }
func (m *mediaManager) PublishDeviceMessage(message map[string]any) {
	m.display.PublishDeviceMessage(message)
}

func (m *mediaManager) HandleForward(ctx context.Context, message map[string]any) error {
	clientID := stringField(message, "client_id")
	payload, _ := message["payload"].(map[string]any)
	if stringField(payload, "type") != "request-offer" {
		if m.camera.HasClient(clientID) {
			return m.camera.HandleForward(message)
		}
		return m.display.HandleForward(message)
	}
	raw, _ := payload["scrcpy_options"].(map[string]any)
	options, err := parseStreamOptions(raw, false)
	if err != nil {
		return err
	}
	bridge, rtc := m.displayBridge, m.display
	sdk, _ := strconv.Atoi(m.displayBridge.device.property("ro.build.version.sdk"))
	if options.Source == "camera" {
		if sdk > 0 && sdk < 31 {
			return fmt.Errorf("camera capture requires Android 12 or newer")
		}
		bridge, rtc = m.cameraBridge, m.camera
	}
	if options.Audio && sdk > 0 && sdk < 30 {
		return fmt.Errorf("audio capture requires Android 11 or newer")
	}
	if options.Source != "camera" {
		options = m.displayOptions(options)
	}
	if raw["video"] != false {
		if err := bridge.Start(ctx, options); err != nil {
			return err
		}
	}
	return rtc.HandleForward(message)
}

func (m *mediaManager) StopIdle(preview bool) {
	if !preview && !m.display.HasSessions() && !m.wsAudioActive.Load() {
		_ = m.displayBridge.Stop()
	}
	if !m.camera.HasSessions() {
		_ = m.cameraBridge.Stop()
	}
}

// Only capture options are merged: an RTC viewer that disabled audio must not
// receive an audio track just because a separate WebSocket listener needs one.
func (m *mediaManager) displayOptions(options streamOptions) streamOptions {
	if m.wsAudioActive.Load() && !options.Audio {
		options.Audio = true
		if options.Values == nil {
			options.Values = make(map[string]string)
		}
		for _, key := range []string{"audio_source", "audio_dup"} {
			delete(options.Values, key)
			if value := m.wsAudioOptions[key]; value != "" {
				options.Values[key] = value
			}
		}
	}
	return options
}

func (m *mediaManager) StartAudio(ctx context.Context, raw map[string]any) error {
	requested, err := parseStreamOptions(raw, true)
	if err != nil {
		return err
	}
	sdk, _ := strconv.Atoi(m.displayBridge.device.property("ro.build.version.sdk"))
	if sdk > 0 && sdk < 30 {
		return fmt.Errorf("audio capture requires Android 11 or newer")
	}
	options := m.displayBridge.currentOptions()
	if options.Source == "" {
		options = requested
	}
	options.Audio = true
	options.Reconfigure = true
	for _, key := range []string{"audio_source", "audio_dup"} {
		delete(options.Values, key)
		if value := requested.Values[key]; value != "" {
			options.Values[key] = value
		}
	}
	if err := m.displayBridge.Start(ctx, options); err != nil {
		return err
	}
	m.wsAudioOptions = requested.Values
	m.wsAudioActive.Store(true)
	return nil
}

func (m *mediaManager) StopAudio(ctx context.Context, preview bool) error {
	m.wsAudioActive.Store(false)
	m.wsAudioOptions = nil
	if !preview && !m.display.HasSessions() {
		return m.displayBridge.Stop()
	}
	if !m.display.HasAudioSessions() {
		options := m.displayBridge.currentOptions()
		if options.Audio {
			options.Audio = false
			options.Reconfigure = true
			return m.displayBridge.Start(ctx, options)
		}
	}
	return nil
}
