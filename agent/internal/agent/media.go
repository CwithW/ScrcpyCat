package agent

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
)

// Display and camera captures have separate scrcpy processes and RTP tracks.
// Multiple viewers of one source share the encoder, but never replace the
// other source (for example a camera subview alongside the phone screen).
type mediaManager struct {
	mu                          sync.Mutex
	ctx                         context.Context
	closed                      bool
	previewOptions              *streamOptions
	display, camera             *webRTCManager
	displayBridge, cameraBridge *scrcpyBridge
	wsAudioActive               atomic.Bool
	wsAudioOptions              map[string]string
}

func newMediaManager(ctx context.Context, writer *lockedWriter, displayBridge, cameraBridge *scrcpyBridge, terminals *terminalManager) *mediaManager {
	manager := &mediaManager{
		ctx:           ctx,
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
	manager.display.onSessionClosed = func() { manager.StopIdle() }
	manager.camera.onSessionClosed = func() { manager.StopIdle() }
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
func (m *mediaManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	m.display.Close()
	m.camera.Close()
}
func (m *mediaManager) PushH264(packet mediaPacket) { m.display.PushH264(packet) }
func (m *mediaManager) PublishDeviceMessage(message map[string]any) {
	m.display.PublishDeviceMessage(message)
}

func (m *mediaManager) HandleForward(ctx context.Context, message map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
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
	if rtc.HasClient(clientID) {
		return rtc.HandleForward(message)
	}
	if options.Source != "camera" {
		options = m.displayOptions(options)
	} else {
		options = rtc.mergeCaptureNeeds(options)
	}
	if raw["video"] != false {
		previous := bridge.currentOptions()
		_, active := rtc.LatestVideoOptions()
		if active && previous.Values["video_codec_options"] != options.Values["video_codec_options"] {
			return fmt.Errorf("encoder options changed; reconnect the existing viewers first")
		}
		if err := bridge.Start(ctx, options); err != nil {
			return err
		}
		profile, err := bridge.VideoCodec(ctx)
		if err != nil {
			return err
		}
		if err := rtc.SetVideoCodec(profile); err != nil {
			if active {
				_ = bridge.Start(ctx, previous)
			}
			return err
		}
	}
	return rtc.HandleForward(message)
}

func (m *mediaManager) StopIdle() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.ctx.Err() != nil {
		return
	}
	_ = m.reconcileDisplay(m.ctx)
	if options, connected := m.camera.LatestVideoOptions(); connected {
		_ = m.cameraBridge.Start(m.ctx, m.camera.mergeCaptureNeeds(options))
	} else {
		_ = m.cameraBridge.Stop()
	}
}

func (m *mediaManager) StartPreview(ctx context.Context, options streamOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.previewOptions = &options
	return m.reconcileDisplay(ctx)
}

func (m *mediaManager) StopPreview(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.previewOptions = nil
	return m.reconcileDisplay(ctx)
}

// Restore the remaining viewer's settings when a connection ends, including
// releasing keep_active when only a non-waking preview remains.
func (m *mediaManager) reconcileDisplay(ctx context.Context) error {
	options, connected := m.display.LatestVideoOptions()
	if !connected {
		if m.previewOptions != nil {
			options = *m.previewOptions
			options.Values = cloneStringMap(options.Values)
		} else if m.wsAudioActive.Load() {
			options, _ = parseStreamOptions(map[string]any{"audio": true, "stay_awake": false}, true)
		} else {
			return m.displayBridge.Stop()
		}
	}
	return m.displayBridge.Start(ctx, m.displayOptions(options))
}

// Only capture options are merged: an RTC viewer that disabled audio must not
// receive an audio track just because a separate WebSocket listener needs one.
func (m *mediaManager) displayOptions(options streamOptions) streamOptions {
	options.Values = cloneStringMap(options.Values)
	options = m.display.mergeCaptureNeeds(options)
	if m.previewOptions != nil && m.previewOptions.Values["keep_active"] == "true" {
		options.Values["keep_active"] = "true"
	}
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
	m.mu.Lock()
	defer m.mu.Unlock()
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
	options.Values = cloneStringMap(options.Values)
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

func (m *mediaManager) StopAudio(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.wsAudioActive.Store(false)
	m.wsAudioOptions = nil
	return m.reconcileDisplay(ctx)
}
