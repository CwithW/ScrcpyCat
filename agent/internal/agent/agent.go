package agent

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type Config struct {
	ADBSerial       string
	ADBServer       string
	DeviceID        string
	SignalingURL    string
	EnrollmentToken string
	IdentityPath    string
	PIDPath         string
	ScrcpyJar       string
	ScrcpyVersion   string
	HealthCheck     bool
}

func (c *Config) ResolveDefaults() {
	if c.ADBServer == "" {
		c.ADBServer = "127.0.0.1:5037"
	}
	if c.ScrcpyVersion == "" {
		c.ScrcpyVersion = "4.1"
	}
	if c.ADBSerial == "" {
		if c.IdentityPath == "" {
			c.IdentityPath = "/data/local/tmp/scrcpycat-agent.identity"
		}
		if c.PIDPath == "" {
			c.PIDPath = "/data/local/tmp/scrcpycat-agent.pid"
		}
		if c.ScrcpyJar == "" {
			c.ScrcpyJar = "/data/local/tmp/scrcpy-server.jar"
		}
		return
	}
	if c.DeviceID == "" {
		c.DeviceID = c.ADBSerial
	}
	hash := sha256.Sum256([]byte(c.ADBSerial))
	state := fmt.Sprintf("/var/lib/scrcpycat/agent/%x", hash[:16])
	if c.IdentityPath == "" {
		c.IdentityPath = filepath.Join(state, "identity.json")
	}
	if c.PIDPath == "" {
		c.PIDPath = filepath.Join(state, "agent.pid")
	}
	if c.ScrcpyJar == "" {
		c.ScrcpyJar = "/opt/scrcpycat/scrcpy/scrcpy-server"
	}
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.DeviceID) == "" {
		return errors.New("--device-id is required")
	}
	if !strings.HasPrefix(c.SignalingURL, "ws://") && !strings.HasPrefix(c.SignalingURL, "wss://") {
		return errors.New("--signaling must use ws:// or wss://")
	}
	if c.IdentityPath == "" {
		return errors.New("--identity-file is required")
	}
	if c.PIDPath == "" {
		return errors.New("--pid-file is required")
	}
	return nil
}

type identity struct {
	AgentToken string `json:"agent_token"`
}

var ErrAuthentication = errors.New("agent enrollment rejected")

type client struct {
	backend          *deviceBackend
	config           Config
	info             map[string]any
	bridge           *scrcpyBridge
	previewActive    atomic.Bool
	snapshotInterval atomic.Int64
	cameraBridge     *scrcpyBridge
}

func Run(ctx context.Context, config Config) error {
	config.ResolveDefaults()
	release, err := acquireInstance(config.PIDPath)
	if err != nil {
		return err
	}
	defer release()
	startup, startupCancel := context.WithTimeout(ctx, 30*time.Second)
	backend, err := newDeviceBackend(startup, config)
	startupCancel()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if backend.adb != nil {
		go backend.adb.watchConnection(ctx, cancel)
	}
	instance := &client{config: config, backend: backend, info: deviceInfo(config, backend), bridge: newScrcpyBridge(config, backend), cameraBridge: newScrcpyBridge(config, backend)}
	instance.snapshotInterval.Store(10)
	for attempt := 0; ctx.Err() == nil; attempt++ {
		err := instance.connect(ctx)
		if errors.Is(err, ErrAuthentication) {
			return err
		}
		if errors.Is(err, errAgentQuit) {
			return nil
		}
		if err != nil && ctx.Err() == nil {
			backoff := time.Second * time.Duration(min(attempt+1, 15))
			log.Printf("signaling connection ended: %v; retrying in %s", err, backoff)
			select {
			case <-ctx.Done():
			case <-time.After(backoff):
			}
		}
	}
	return ctx.Err()
}

func (c *client) connect(ctx context.Context) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.config.SignalingURL, http.Header{})
	if err != nil {
		return err
	}
	defer conn.Close()
	stopOnCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopOnCancel()
	defer c.bridge.Stop()
	defer c.cameraBridge.Stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c.previewActive.Store(false)

	writer := &lockedWriter{conn: conn}
	terminals := newTerminalManager(writer, c.device().terminals)
	defer terminals.CloseAll()
	files := newDeviceFileManager(func(clientID string, payload map[string]any) error {
		return writer.writeJSON(map[string]any{"message_type": "file_message", "client_id": clientID, "payload": payload})
	}, c.device())
	defer files.Close()
	webrtc := newMediaManager(ctx, writer, c.bridge, c.cameraBridge, terminals)
	defer webrtc.Close()
	go c.reportTelemetry(ctx, writer, webrtc)
	c.bridge.SetDeviceMessagePublisher(func(payload map[string]any) {
		webrtc.PublishDeviceMessage(payload)
		_ = writer.writeJSON(map[string]any{"message_type": "clipboard", "payload": payload})
	})
	c.bridge.SetPreviewPublisher(func(packet mediaPacket) {
		frame, err := previewFrame(c.config.DeviceID, packet)
		if err == nil && c.previewActive.Load() {
			_ = writer.writeBinary(frame)
		}
		webrtc.PushH264(packet)
	})
	currentIdentity, _ := loadIdentity(c.config.IdentityPath)
	if err := writer.writeJSON(map[string]any{
		"message_type":     "agent_hello",
		"device_id":        c.config.DeviceID,
		"enrollment_token": c.config.EnrollmentToken,
		"agent_token":      currentIdentity.AgentToken,
		"device_info":      c.info,
	}); err != nil {
		return err
	}

	stopHeartbeat := make(chan struct{})
	defer close(stopHeartbeat)
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopHeartbeat:
				return
			case <-ticker.C:
				_ = writer.writeJSON(map[string]any{"message_type": "heartbeat", "device_id": c.config.DeviceID})
			}
		}
	}()

	for {
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		if messageType != websocket.TextMessage {
			continue
		}
		var message map[string]any
		if err := json.Unmarshal(payload, &message); err != nil {
			continue
		}
		started := time.Now()
		err = c.handleMessage(ctx, writer, webrtc, terminals, files, message)
		if elapsed := time.Since(started); elapsed > time.Second {
			log.Printf("slow agent message: %s took %s", stringField(message, "message_type"), elapsed)
		}
		if err != nil {
			if errors.Is(err, errAgentQuit) || errors.Is(err, ErrAuthentication) {
				return err
			}
			log.Printf("agent message error: %v", err)
		}
	}
}

func (c *client) handleMessage(ctx context.Context, writer *lockedWriter, webrtc *mediaManager, terminals *terminalManager, files *deviceFileManager, message map[string]any) error {
	switch stringField(message, "message_type") {
	case "error":
		if stringField(message, "error") == "agent enrollment rejected" {
			return ErrAuthentication
		}
		return fmt.Errorf("control plane: %s", stringField(message, "error"))
	case "agent_config":
		if token := stringField(message, "agent_token"); token != "" {
			if err := saveIdentity(c.config.IdentityPath, identity{AgentToken: token}); err != nil {
				return err
			}
		}
		webrtc.SetICEServers(message["ice_servers"])
		settings, _ := message["default_settings"].(map[string]any)
		c.setSnapshotInterval(settings)
		return nil
	case "agent_settings":
		settings, _ := message["settings"].(map[string]any)
		c.setSnapshotInterval(settings)
		return nil
	case "quit_agent":
		return errAgentQuit
	case "start_audio":
		if err := webrtc.StartAudio(ctx, message); err != nil {
			return writer.writeJSON(map[string]any{"message_type": "audio_error", "client_id": stringField(message, "client_id"), "error": err.Error()})
		}
		return nil
	case "stop_audio":
		return webrtc.StopAudio(ctx)
	case "start_preview":
		log.Printf("preview requested by %s", stringField(message, "client_id"))
		options, err := parseStreamOptions(message, true)
		if err != nil {
			return c.streamError(writer, message, err)
		}
		c.setSnapshotInterval(message)
		c.previewActive.Store(true)
		if err := webrtc.StartPreview(ctx, options); err != nil {
			c.previewActive.Store(false)
			return c.streamError(writer, message, err)
		}
		return c.bridge.EnqueueControl(map[string]any{"type": "request_keyframe"})
	case "stop_preview":
		log.Print("preview stopped")
		c.previewActive.Store(false)
		return webrtc.StopPreview(ctx)
	case "client_disconnected":
		clientID := stringField(message, "client_id")
		terminals.CloseClient(clientID)
		files.CloseClient(clientID)
		webrtc.CloseSession(clientID)
		webrtc.StopIdle()
		return nil
	case "forward":
		if payload, ok := message["payload"].(map[string]any); ok && stringField(payload, "type") == "request-offer" {
			settings, _ := payload["scrcpy_options"].(map[string]any)
			c.setSnapshotInterval(settings)
		}
		if err := webrtc.HandleForward(ctx, message); err != nil {
			return c.streamError(writer, message, err)
		}
		return nil
	case "screenshot_request":
		go c.screenshot(writer, message)
		return nil
	case "command":
		go c.executeCommand(writer, message)
		return nil
	case "task_available":
		return writer.writeJSON(map[string]any{"message_type": "task_claim", "task_id": stringField(message, "task_id"), "device_id": c.config.DeviceID})
	case "task_dispatch":
		go c.executeTask(writer, message)
		return nil
	case "file_open":
		files.Open(stringField(message, "client_id"))
		return nil
	case "file_command":
		payload, _ := message["payload"].(map[string]any)
		files.Command(stringField(message, "client_id"), payload)
		return nil
	case "file_chunk":
		data, err := base64.StdEncoding.DecodeString(stringField(message, "data"))
		if err != nil {
			return err
		}
		files.Chunk(stringField(message, "client_id"), data)
		return nil
	case "pty_open":
		rows, cols := uint16(numberField(message, "rows", 24)), uint16(numberField(message, "cols", 80))
		return terminals.Open(stringField(message, "client_id"), stringField(message, "session_id"), rows, cols)
	case "pty_input":
		terminals.Input(stringField(message, "client_id"), stringField(message, "session_id"), stringField(message, "data"))
		return nil
	case "pty_resize":
		terminals.Resize(stringField(message, "client_id"), stringField(message, "session_id"), uint16(numberField(message, "rows", 24)), uint16(numberField(message, "cols", 80)))
		return nil
	case "pty_close":
		terminals.Close(stringField(message, "client_id"), stringField(message, "session_id"))
		return nil
	case "group_control_event":
		event, _ := message["event"].(map[string]any)
		if !browserControlAllowed(event) {
			return errors.New("unsupported group control event")
		}
		if err := c.bridge.Start(ctx, streamOptions{Preview: true}); err != nil {
			return err
		}
		return c.bridge.EnqueueControl(event)
	case "inject_data":
		if capability := injectionCapability(message); capability != "" {
			return writer.writeJSON(map[string]any{"message_type": "capability_error", "client_id": stringField(message, "client_id"), "device_id": c.config.DeviceID, "capability": capability, "supported": false, "error": "Agent does not support " + capability})
		}
		event, _ := message["payload"].(map[string]any)
		if event == nil {
			return nil
		}
		if !browserControlAllowed(event) {
			return c.streamError(writer, message, errors.New("unsupported injection event"))
		}
		if err := c.bridge.Start(ctx, streamOptions{Preview: true}); err != nil {
			return err
		}
		return c.bridge.EnqueueControl(event)
	}
	return nil
}

func injectionCapability(message map[string]any) string {
	switch stringField(message, "channel") {
	case "gps":
		return "gps_injection"
	case "sensor":
		return "sensor_injection"
	case "camera":
		return "camera_injection"
	}
	payload, _ := message["payload"].(map[string]any)
	switch stringField(payload, "type") {
	case "gps":
		return "gps_injection"
	case "accel", "gyro", "light", "temp", "proximity", "hinge_angle":
		return "sensor_injection"
	}
	return ""
}

func (c *client) streamError(writer *lockedWriter, message map[string]any, err error) error {
	_ = writer.writeJSON(map[string]any{
		"message_type": "device_msg", "client_id": stringField(message, "client_id"),
		"payload": map[string]any{"type": "scrcpy_error", "message": err.Error()},
	})
	return err
}

type lockedWriter struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (w *lockedWriter) writeJSON(value any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return w.conn.WriteJSON(value)
}

func (w *lockedWriter) writeBinary(value []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return w.conn.WriteMessage(websocket.BinaryMessage, value)
}

func deviceInfo(config Config, backend *deviceBackend) map[string]any {
	displays, cameras := mediaInfo(config, backend.capture)
	sdkText := backend.property("ro.build.version.sdk")
	sdk, _ := strconv.Atoi(sdkText)
	return map[string]any{
		"model":           backend.property("ro.product.model"),
		"displays":        displays,
		"cameras":         cameras,
		"abi":             backend.property("ro.product.cpu.abi"),
		"sdk":             sdkText,
		"android_version": backend.property("ro.build.version.release"),
		"agent_version":   "0.1.0-dev",
		"capabilities": map[string]bool{
			"webrtc":           true,
			"ws_preview":       true,
			"ws_audio":         true,
			"file_manager":     true,
			"shell":            true,
			"pty":              true,
			"tasks":            true,
			"metrics":          true,
			"snapshots":        true,
			"camera_capture":   sdk >= 31,
			"audio_capture":    sdk >= 30,
			"camera_injection": false,
			"gps_injection":    false,
			"sensor_injection": false,
		},
	}
}

func property(name string) string {
	output, err := exec.Command("getprop", name).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func Healthy(identityPath, pidPath, scrcpyJar string) bool {
	if info, err := os.Stat(scrcpyJar); err != nil || !info.Mode().IsRegular() {
		return false
	}
	if _, err := loadIdentity(identityPath); err != nil {
		return false
	}
	contents, err := os.ReadFile(pidPath)
	if err != nil {
		return false
	}
	return isAgentProcess(strings.TrimSpace(string(contents)))
}

func loadIdentity(path string) (identity, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return identity{}, err
	}
	var result identity
	if err := json.Unmarshal(contents, &result); err != nil || result.AgentToken == "" {
		return identity{}, errors.New("invalid identity file")
	}
	return result, nil
}

func saveIdentity(path string, value identity) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	contents, err := json.Marshal(value)
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, contents, 0600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func stringField(message map[string]any, key string) string {
	value, _ := message[key].(string)
	return value
}

func numberField(message map[string]any, key string, fallback int) int {
	if value, ok := message[key].(float64); ok && value > 0 {
		return int(value)
	}
	return fallback
}

func releaseInstance(path string) {
	contents, err := os.ReadFile(path)
	if err == nil && strings.TrimSpace(string(contents)) == fmt.Sprintf("%d", os.Getpid()) {
		_ = os.Remove(path)
	}
}
