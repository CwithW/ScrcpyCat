package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cwithw/ScrcpyCat/backend/internal/webui"
	"github.com/cwithw/ScrcpyCat/internal/buildinfo"
	"github.com/gorilla/websocket"
)

type Server struct {
	config                 Config
	store                  Store
	hub                    *realtimeHub
	redis                  *redisBus
	stopClean              chan struct{}
	upgrader               websocket.Upgrader
	deviceMetrics          sync.Map
	pendingAgentHandshakes chan struct{}
	loginLimiter           *loginLimiter
}

func NewServer(config Config) (*Server, error) {
	store, err := NewPostgresStore(context.Background(), config.PostgresDSN, config.BootstrapAdminUser, config.BootstrapAdminPass)
	if err != nil {
		return nil, fmt.Errorf("create persistent store: %w", err)
	}
	if err := os.MkdirAll(config.AssetDir, 0750); err != nil {
		store.Close()
		return nil, fmt.Errorf("create asset directory: %w", err)
	}
	return newServer(config, store, true)
}

// NewServerWithStore is reserved for deterministic tests. Production code must
// use NewServer, which requires PostgreSQL and Redis configuration.
func NewServerWithStore(config Config, store Store) (*Server, error) {
	return newServer(config, store, false)
}

func newServer(config Config, store Store, requireRedis bool) (*Server, error) {
	config = config.withSecurityDefaults()
	server := &Server{
		config:    config,
		store:     store,
		hub:       newRealtimeHub(),
		stopClean: make(chan struct{}),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  64 * 1024,
			WriteBufferSize: 64 * 1024,
		},
		pendingAgentHandshakes: make(chan struct{}, config.MaxPendingAgentHandshakes),
		loginLimiter:           newLoginLimiter(config),
	}
	server.hub.filter = server.filterBroadcast
	if requireRedis {
		bus, err := newRedisBus(context.Background(), config.RedisURL, server.hub)
		if err != nil {
			store.Close()
			return nil, err
		}
		server.redis = bus
	}
	server.startMaintenance()
	server.upgrader.CheckOrigin = func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		return origin == "" || server.originAllowed(origin)
	}
	return server, nil
}

func (s *Server) Close() {
	if s.stopClean != nil {
		close(s.stopClean)
		s.stopClean = nil
	}
	if s.redis != nil {
		s.redis.Close()
	}
	if s.store != nil {
		s.store.Close()
	}
}

func (s *Server) startMaintenance() {
	retention := s.config.AuditRetention
	if retention <= 0 {
		retention = 90 * 24 * time.Hour
	}
	stop := s.stopClean
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			s.store.Clean(time.Now().UTC().Add(-retention))
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
		}
	}()
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/devices", s.requireUser(s.devices))
	mux.HandleFunc("/connect_client", s.connectClient)
	mux.HandleFunc("/register_agent", s.registerAgent)
	mux.HandleFunc("/api/auth-status", s.authStatus)
	mux.HandleFunc("/api/login", s.login)
	mux.HandleFunc("/api/ws-ticket", s.websocketTicket)
	mux.HandleFunc("/api/register", s.register)
	mux.HandleFunc("/api/logout", s.requireUser(s.logout))
	mux.HandleFunc("/api/me", s.requireUser(s.me))
	mux.HandleFunc("/api/version", s.requireUser(s.version))
	mux.HandleFunc("/api/server/addresses", s.requireUser(s.serverAddresses))
	mux.HandleFunc("/api/deploy/package", s.requireAdmin(s.deploymentPackage))
	mux.HandleFunc("/agent/", s.requireAdmin(s.agentArtifact))
	mux.HandleFunc("/api/ice_servers", s.requireUser(s.iceServerList))
	mux.HandleFunc("/api/default_settings", s.requireUser(s.defaultSettings))
	mux.HandleFunc("/api/tags", s.requireUser(s.tags))
	mux.HandleFunc("/api/admin/users", s.requireAdmin(s.adminUsers))
	mux.HandleFunc("/api/admin/assign", s.requireAdmin(s.adminAssign))
	mux.HandleFunc("/api/admin/users/create", s.requireAdmin(s.adminCreateUser))
	mux.HandleFunc("/api/admin/users/delete", s.requireAdmin(s.adminDeleteUser))
	mux.HandleFunc("/api/admin/users/reset_password", s.requireAdmin(s.adminResetPassword))
	mux.HandleFunc("/api/admin/users/update_note", s.requireAdmin(s.adminUpdateNote))
	mux.HandleFunc("/api/admin/users/rename", s.requireAdmin(s.adminRenameUser))
	mux.HandleFunc("/api/admin/users/update", s.requireAdmin(s.adminUpdateUser))
	mux.HandleFunc("/api/admin/users/kick", s.requireAdmin(s.adminKickUser))
	mux.HandleFunc("/api/agents/enrollment-tokens", s.requireAdmin(s.enrollmentTokens))
	mux.HandleFunc("/api/admin/deployment-credentials", s.requireAdmin(s.deploymentCredentials))
	mux.HandleFunc("/api/admin/deployment-credentials/revoke", s.requireAdmin(s.revokeDeploymentCredential))
	mux.HandleFunc("/api/deploy/enrollment-tokens", s.deploymentEnrollmentTokens)
	mux.HandleFunc("/api/tasks", s.requireUser(s.tasks))
	mux.HandleFunc("/api/tasks/details", s.requireUser(s.taskDetails))
	mux.HandleFunc("/api/shortcuts", s.requireUser(s.shortcuts))
	mux.HandleFunc("/api/files", s.requireUser(s.files))
	mux.HandleFunc("/upload", s.requireUser(s.upload))
	mux.HandleFunc("/downloads/", s.download)
	mux.HandleFunc("/api/devices/", s.requireUser(s.deviceByID))
	mux.HandleFunc("/api/share/create", s.requireUser(s.shareCreate))
	mux.HandleFunc("/api/share/list", s.requireUser(s.shareList))
	mux.HandleFunc("/api/share/revoke", s.requireUser(s.shareRevoke))
	mux.HandleFunc("/api/share/update", s.requireUser(s.shareUpdate))
	mux.HandleFunc("/api/share/extend", s.requireUser(s.shareExtend))
	mux.HandleFunc("/api/share/redeem_card", s.shareRedeemCard)
	mux.HandleFunc("/api/share/info", s.shareInfo)
	mux.Handle("/", webui.Handler())
	return s.withCORS(mux)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	if err := s.store.Healthy(); err != nil {
		writeError(w, http.StatusServiceUnavailable, "persistent store unavailable")
		return
	}
	if s.redis != nil && s.redis.Healthy() != nil {
		writeError(w, http.StatusServiceUnavailable, "redis unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": "scrcpycat-controlplane"})
}

func (s *Server) authStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"noAuth": false})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid login request")
		return
	}
	clientIP := requestClientIP(r, s.config.TrustedProxyCIDRs)
	if retryAfter, limited := s.loginLimiter.allow(body.Username, clientIP); limited {
		w.Header().Set("Retry-After", strconv.FormatInt(int64(retryAfter.Seconds())+1, 10))
		writeError(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	user, err := s.store.Authenticate(body.Username, body.Password)
	if err != nil {
		s.loginLimiter.recordFailure(body.Username, clientIP)
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	s.loginLimiter.clearAccount(body.Username)
	token, err := issueUserToken(s.config.JWTSecret, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "issue access token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":            token,
		"username":         user.Username,
		"role":             user.Role,
		"assigned_devices": user.AssignedDevices,
	})
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if !s.config.AllowRegistration {
		writeError(w, http.StatusForbidden, "registration is disabled")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid registration request")
		return
	}
	if _, err := s.store.CreateUser(body.Username, body.Password, RoleUser, nil); err != nil {
		if errors.Is(err, ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "username already exists")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid registration request")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"status": "ok"})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"username":          user.Username,
		"role":              user.Role,
		"assigned_devices":  user.AssignedDevices,
		"permissions":       user.Permissions,
		"forbid_bitrate":    user.ForbidBitrate,
		"forbid_fps":        user.ForbidFPS,
		"forbid_resolution": user.ForbidResolution,
		"forbid_audio":      user.ForbidAudio,
		"settings":          user.Settings,
		"expires_at":        user.ExpiresAt,
	})
}

func (s *Server) version(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": buildinfo.Version, "git_commit": buildinfo.Revision, "edition": "self-hosted"})
}

func (s *Server) serverAddresses(w http.ResponseWriter, _ *http.Request) {
	address := strings.TrimPrefix(strings.TrimPrefix(s.config.PublicURL, "https://"), "http://")
	writeJSON(w, http.StatusOK, map[string]any{"code": 0, "data": map[string]any{"addresses": []string{address}, "current": address}, "public_url": s.config.PublicURL, "signaling_url": s.config.PublicURL})
}

func (s *Server) iceServerList(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.iceServers())
}

func (s *Server) devices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, s.devicesFor(currentUser(r)))
}

func (s *Server) defaultSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.store.DefaultSettings())
	case http.MethodPut, http.MethodPost:
		if currentUser(r).Role != RoleAdmin {
			writeError(w, http.StatusForbidden, "admin permission required")
			return
		}
		settings := map[string]any{}
		if err := decodeJSON(r, &settings); err != nil {
			writeError(w, http.StatusBadRequest, "invalid settings")
			return
		}
		s.store.SetDefaultSettings(settings)
		for _, device := range s.store.DevicesFor(User{Role: RoleAdmin}) {
			s.hub.sendAgent(device.ID, map[string]any{"message_type": "agent_settings", "settings": settings})
		}
		s.hub.broadcast(map[string]any{"message_type": "global_settings_updated", "settings": settings})
		writeJSON(w, http.StatusOK, settings)
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) tags(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		tags, assignments := s.store.Tags()
		writeJSON(w, http.StatusOK, map[string]any{"tags": tags, "device_tags": assignments})
	case http.MethodPut, http.MethodPost:
		if currentUser(r).Role != RoleAdmin {
			writeError(w, http.StatusForbidden, "admin permission required")
			return
		}
		var body struct {
			Tags       []Tag               `json:"tags"`
			DeviceTags map[string][]string `json:"device_tags"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid tags request")
			return
		}
		s.store.ReplaceTags(body.Tags, body.DeviceTags)
		tags, assignments := s.store.Tags()
		s.hub.broadcast(map[string]any{"message_type": "tags_update", "tags": tags, "deviceTags": assignments})
		writeJSON(w, http.StatusOK, map[string]any{"tags": tags, "device_tags": assignments})
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.store.Users())
	case http.MethodPost:
		var body struct {
			Username        string   `json:"username"`
			Password        string   `json:"password"`
			Role            Role     `json:"role"`
			AssignedDevices []string `json:"assigned_devices"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid user request")
			return
		}
		if body.Role == "" {
			body.Role = RoleUser
		}
		user, err := s.store.CreateUser(body.Username, body.Password, body.Role, body.AssignedDevices)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, user)
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) enrollmentTokens(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var body struct {
		DeviceID string `json:"device_id"`
		TTL      int    `json:"ttl_seconds"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid enrollment request")
		return
	}
	ttl := time.Duration(body.TTL) * time.Second
	if ttl <= 0 || ttl > 24*time.Hour {
		ttl = 15 * time.Minute
	}
	enrollment := s.store.CreateEnrollment(body.DeviceID, ttl)
	writeJSON(w, http.StatusCreated, map[string]any{"token": enrollment.Token, "expires_at": enrollment.ExpiresAt})
}

func (s *Server) connectClient(w http.ResponseWriter, r *http.Request) {
	user, viewOnly, access, err := s.browserAccessFromRequest(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	audioDone := make(chan struct{})
	defer close(audioDone)
	peer := &browserPeer{id: randomID(), user: user, viewOnly: viewOnly, socket: &socketPeer{conn: conn}, audioFrames: make(chan []byte, 5), audioDone: audioDone}
	go peer.writeAudio()
	conn.SetReadLimit(2 << 20)
	stopAccessWatch := s.watchBrowserAccess(access, peer)
	defer stopAccessWatch()
	s.hub.addBrowser(peer)
	defer func() {
		s.hub.sendAgent(peer.deviceID, map[string]any{"message_type": "client_disconnected", "client_id": peer.id})
		for _, deviceID := range s.hub.removeAudioBrowser(peer.id) {
			s.hub.sendAgent(deviceID, map[string]any{"message_type": "stop_audio", "device_id": deviceID})
		}
		for _, deviceID := range s.hub.removeBrowser(peer.id) {
			s.hub.sendAgent(deviceID, map[string]any{"message_type": "stop_preview", "device_id": deviceID})
		}
		_ = peer.socket.close()
		s.broadcastDeviceList()
	}()

	for {
		messageType, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if messageType != websocket.TextMessage {
			continue
		}
		var message map[string]any
		if err := json.Unmarshal(raw, &message); err != nil {
			continue
		}
		s.handleBrowserMessage(peer, message)
	}
}

func (s *Server) handleBrowserMessage(peer *browserPeer, message map[string]any) {
	messageType := stringField(message, "message_type")
	deviceID := stringField(message, "device_id")
	if deviceID == "" {
		deviceID = peer.deviceID
	}

	switch messageType {
	case "connect":
		deviceID = stringField(message, "device_id")
		if deviceID == "" || !s.store.CanAccessDevice(peer.user, deviceID) {
			_ = peer.socket.writeJSON(map[string]any{"message_type": "error", "error": "device access denied"})
			return
		}
		s.hub.bindBrowser(peer.id, deviceID)
		config := map[string]any{"message_type": "config", "ice_servers": s.iceServers()}
		for _, device := range s.store.DevicesFor(peer.user) {
			if device.ID == deviceID {
				config["device_info"] = device.Info
				break
			}
		}
		_ = peer.socket.writeJSON(config)
		s.hub.sendAgent(deviceID, map[string]any{"message_type": "client_connected", "client_id": peer.id, "user_id": peer.user.ID})
		s.broadcastDeviceList()
	case "start_audio":
		s.startBrowserAudio(peer, deviceID, message)
	case "stop_audio":
		if deviceID != "" && s.store.CanAccessDevice(peer.user, deviceID) && s.hub.unsubscribeAudio(peer.id, deviceID) {
			s.hub.sendAgent(deviceID, withClient(message, peer.id))
		}
	case "start_preview":
		if deviceID == "" || !s.store.CanAccessDevice(peer.user, deviceID) {
			return
		}
		s.hub.subscribePreview(peer.id, deviceID)
		message = s.constrainedStreamOptions(peer.user, message)
		s.hub.sendAgent(deviceID, withClient(message, peer.id))
	case "stop_preview":
		if deviceID == "" || !s.store.CanAccessDevice(peer.user, deviceID) {
			return
		}
		if s.hub.unsubscribePreview(peer.id, deviceID) {
			s.hub.sendAgent(deviceID, withClient(message, peer.id))
		}
	case "forward", "inject_data":
		if deviceID == "" || !s.store.CanAccessDevice(peer.user, deviceID) || (messageType == "inject_data" && peer.viewOnly) {
			return
		}
		forwarded := withClient(message, peer.id)
		// These flags come exclusively from the authenticated server session.
		forwarded["permissions"] = map[string]bool{"control": !peer.viewOnly, "shell": peer.user.Can("shell")}
		if payload, ok := forwarded["payload"].(map[string]any); ok && stringField(payload, "type") == "request-offer" {
			raw, _ := payload["scrcpy_options"].(map[string]any)
			payload["scrcpy_options"] = s.constrainedStreamOptions(peer.user, raw)
		}
		s.hub.sendAgent(deviceID, forwarded)
	case "quit_agent":
		if deviceID == "" || peer.user.Role != RoleAdmin || !s.store.CanAccessDevice(peer.user, deviceID) {
			_ = peer.socket.writeJSON(map[string]any{"message_type": "error", "error": "admin permission required"})
			return
		}
		s.audit(peer.user, "agent.stop", deviceID, nil)
		s.hub.sendAgent(deviceID, withClient(message, peer.id))
	case "screenshot_request":
		if deviceID != "" && s.store.CanAccessDevice(peer.user, deviceID) {
			s.hub.sendAgent(deviceID, withClient(message, peer.id))
		}
	case "file_open", "file_command", "file_chunk":
		if deviceID == "" || peer.deviceID != deviceID || !s.store.CanAccessDevice(peer.user, deviceID) || !peer.user.Can("file_library") {
			_ = peer.socket.writeJSON(map[string]any{"message_type": "file_error", "error": "file access denied"})
			return
		}
		if messageType == "file_chunk" && len(stringField(message, "data")) > 88<<10 {
			return
		}
		if messageType == "file_command" {
			payload, _ := message["payload"].(map[string]any)
			s.store.Audit(AuditEvent{Actor: peer.user.ID, Action: "device_file." + stringField(payload, "type"), Target: deviceID, Metadata: map[string]any{"path": stringField(payload, "path")}})
		}
		if !s.hub.sendAgent(deviceID, withClient(message, peer.id)) {
			_ = peer.socket.writeJSON(map[string]any{"message_type": "file_error", "error": "device is offline"})
		}
	case "command", "pty_open", "pty_input", "pty_resize", "pty_close":
		if deviceID == "" || !s.store.CanAccessDevice(peer.user, deviceID) || !peer.user.Can("shell") {
			return
		}
		s.hub.sendAgent(deviceID, withClient(message, peer.id))
	case "group_control_event":
		if peer.viewOnly {
			return
		}
		targets, _ := message["target_device_ids"].([]any)
		for _, target := range targets {
			id, _ := target.(string)
			if id != "" && s.store.CanAccessDevice(peer.user, id) {
				s.hub.sendAgent(id, withClient(message, peer.id))
			}
		}
	}
}

func (s *Server) registerAgent(w http.ResponseWriter, r *http.Request) {
	select {
	case s.pendingAgentHandshakes <- struct{}{}:
	default:
		writeError(w, http.StatusServiceUnavailable, "too many pending agent handshakes")
		return
	}
	reserved := true
	defer func() {
		if reserved {
			<-s.pendingAgentHandshakes
		}
	}()
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	peer := &socketPeer{conn: conn}
	defer func() { _ = peer.close() }()

	conn.SetReadLimit(s.config.AgentHelloMaxBytes)
	_ = conn.SetReadDeadline(time.Now().Add(s.config.AgentHelloTimeout))
	messageType, raw, err := conn.ReadMessage()
	if err != nil || messageType != websocket.TextMessage {
		return
	}
	var hello struct {
		MessageType     string     `json:"message_type"`
		DeviceID        string     `json:"device_id"`
		EnrollmentToken string     `json:"enrollment_token"`
		AgentToken      string     `json:"agent_token"`
		DeviceInfo      DeviceInfo `json:"device_info"`
	}
	if err := json.Unmarshal(raw, &hello); err != nil || hello.MessageType != "agent_hello" || strings.TrimSpace(hello.DeviceID) == "" {
		return
	}

	agentToken := hello.AgentToken
	if !s.store.ValidateAgentToken(agentToken, hello.DeviceID) {
		credential, err := s.store.ExchangeEnrollment(hello.EnrollmentToken, hello.DeviceID)
		if err != nil {
			_ = peer.writeJSON(map[string]any{"message_type": "error", "error": "agent enrollment rejected"})
			return
		}
		agentToken = credential.Token
	}
	_ = conn.SetReadDeadline(time.Time{})
	conn.SetReadLimit(1 << 62)
	<-s.pendingAgentHandshakes
	reserved = false
	device := s.store.UpsertAgentDevice(hello.DeviceID, hello.DeviceInfo)
	previous := s.hub.setAgent(hello.DeviceID, peer)
	if previous != nil && previous != peer {
		_ = previous.close()
	}
	defer func() {
		if s.hub.removeAgent(hello.DeviceID, peer) {
			s.store.MarkOffline(hello.DeviceID)
			s.store.MarkLeasedUnknown(hello.DeviceID)
			s.broadcastDeviceList()
		}
	}()

	_ = peer.writeJSON(map[string]any{"message_type": "agent_config", "agent_token": agentToken, "ice_servers": s.iceServers(), "default_settings": s.store.DefaultSettings()})
	for _, taskID := range s.store.PendingTasks(hello.DeviceID) {
		_ = peer.writeJSON(map[string]any{"message_type": "task_available", "task_id": taskID, "device_id": hello.DeviceID})
	}
	s.broadcastDeviceList()
	log.Printf("agent connected: %s (%s)", device.ID, device.Info.Model)

	for {
		messageType, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if messageType == websocket.BinaryMessage {
			if validPreviewFrame(hello.DeviceID, raw) {
				s.hub.publishPreview(hello.DeviceID, raw)
			} else if validAudioFrame(hello.DeviceID, raw) {
				s.hub.publishAudio(hello.DeviceID, raw)
			}
			continue
		}
		if messageType != websocket.TextMessage {
			continue
		}
		var message map[string]any
		if json.Unmarshal(raw, &message) != nil {
			continue
		}
		s.handleAgentMessage(hello.DeviceID, message)
	}
}

func (s *Server) handleAgentMessage(deviceID string, message map[string]any) {
	switch stringField(message, "message_type") {
	case "heartbeat":
		s.store.TouchDevice(deviceID)
	case "clipboard":
		s.hub.publishClipboard(deviceID, message)
	case "device_info":
		if info, ok := message["device_info"].(map[string]any); ok {
			encoded, _ := json.Marshal(info)
			var typed DeviceInfo
			if json.Unmarshal(encoded, &typed) == nil {
				s.store.UpsertAgentDevice(deviceID, typed)
				s.broadcastDeviceList()
			}
		}
	case "device_msg", "command_result", "screenshot_response", "file_message", "pty_data", "pty_opened", "pty_closed", "pty_error", "audio_error":
		if clientID := stringField(message, "client_id"); clientID != "" {
			delete(message, "client_id")
			s.hub.sendBrowser(clientID, message)
		}
	case "device_metrics":
		if metrics, ok := message["metrics"].(map[string]any); ok {
			s.deviceMetrics.Store(deviceID, metrics)
			s.hub.broadcast(map[string]any{"type": "device_metrics", "device_id": deviceID, "metrics": metrics})
		}
	case "snapshot_update":
		if err := s.saveSnapshot(deviceID, stringField(message, "data")); err != nil {
			log.Printf("snapshot %s: %v", deviceID, err)
		}
	case "task_claim":
		taskID := stringField(message, "task_id")
		task, err := s.store.ClaimTask(taskID, deviceID)
		if err != nil {
			return
		}
		dispatch := map[string]any{"message_type": "task_dispatch", "task_id": task.ID, "type": task.Type, "payload": task.Payload, "dest_path": task.DestPath}
		if task.AssetID != "" {
			asset, assetErr := s.store.Asset(task.AssetID)
			if assetErr != nil {
				_, _ = s.store.SetTaskStatus(task.ID, deviceID, TaskFailed, "asset unavailable")
				return
			}
			dispatch["asset_sha256"] = asset.SHA256
			dispatch["download_url"] = strings.TrimRight(s.config.PublicURL, "/") + "/downloads/" + url.PathEscape(asset.Name) + "?grant=" + url.QueryEscape(s.issueDownloadGrant(asset.ID, task.ID, deviceID))
		}
		s.hub.sendAgent(deviceID, dispatch)
		s.broadcastTask(task)
	case "task_started":
		if task, err := s.store.SetTaskStatus(stringField(message, "task_id"), deviceID, TaskRunning, ""); err == nil {
			s.broadcastTask(task)
		}
	case "task_result":
		status := TaskFailed
		if stringField(message, "status") == string(TaskSuccess) {
			status = TaskSuccess
		}
		if task, err := s.store.SetTaskStatus(stringField(message, "task_id"), deviceID, status, stringField(message, "result")); err == nil {
			s.broadcastTask(task)
		}
	}
}

func (s *Server) broadcastTask(task Task) {
	message := map[string]any{"message_type": "task_status_updated", "task": task}
	s.hub.broadcast(message)
	if s.redis != nil {
		s.redis.PublishBroadcast(message)
	}
}

func (s *Server) broadcastDeviceList() {
	devices := s.devicesFor(User{Role: RoleAdmin})
	s.hub.broadcast(map[string]any{"message_type": "device_list_update", "devices": devices})
}

func (s *Server) iceServers() []map[string]any {
	if len(s.config.TurnURLs) == 0 {
		return []map[string]any{{"urls": "stun:stun.l.google.com:19302"}}
	}
	server := map[string]any{"urls": s.config.TurnURLs}
	if s.config.TurnUsername != "" && s.config.TurnCredential != "" {
		server["username"] = s.config.TurnUsername
		server["credential"] = s.config.TurnCredential
	}
	return []map[string]any{server}
}

func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && s.originAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) originAllowed(origin string) bool {
	for _, allowed := range s.config.CORSOrigins {
		if allowed == origin {
			return true
		}
	}
	publicURL, err := url.Parse(s.config.PublicURL)
	if err == nil && publicURL.Scheme != "" && publicURL.Host != "" && origin == publicURL.Scheme+"://"+publicURL.Host {
		return true
	}
	if len(s.config.CORSOrigins) == 0 {
		return strings.HasPrefix(origin, "http://localhost:") || strings.HasPrefix(origin, "http://127.0.0.1:")
	}
	return false
}

func validPreviewFrame(deviceID string, frame []byte) bool {
	if len(frame) < 49 || string(frame[:4]) != "PREV" {
		return false
	}
	embedded := strings.TrimRight(string(frame[4:36]), "\x00")
	return embedded == deviceID
}

func stringField(message map[string]any, key string) string {
	value, _ := message[key].(string)
	return value
}

func withClient(message map[string]any, clientID string) map[string]any {
	copy := make(map[string]any, len(message)+1)
	for key, value := range message {
		copy[key] = value
	}
	copy["client_id"] = clientID
	return copy
}

func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}
