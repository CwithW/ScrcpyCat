package controlplane

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *Server) requireCapability(w http.ResponseWriter, r *http.Request, capability string) bool {
	if currentUser(r).Can(capability) {
		return true
	}
	writeError(w, http.StatusForbidden, capability+" permission required")
	return false
}

func (s *Server) shortcuts(w http.ResponseWriter, r *http.Request) {
	if !s.requireCapability(w, r, "shell") {
		return
	}
	user := currentUser(r)
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.store.Shortcuts(user.ID))
	case http.MethodPost:
		var shortcuts []Shortcut
		if err := decodeJSON(r, &shortcuts); err != nil {
			writeError(w, http.StatusBadRequest, "invalid shortcuts")
			return
		}
		s.store.ReplaceShortcuts(user.ID, shortcuts)
		s.audit(user, "shortcuts.replace", "", map[string]any{"count": len(shortcuts)})
		writeJSON(w, http.StatusOK, s.store.Shortcuts(user.ID))
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) files(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if !s.requireCapability(w, r, "file_library") {
		return
	}
	writeJSON(w, http.StatusOK, s.store.Assets())
}

func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if !s.requireCapability(w, r, "file_library") {
		return
	}
	name := filepath.Base(strings.TrimSpace(r.URL.Query().Get("name")))
	if name == "." || name == "" || name == "/" {
		writeError(w, http.StatusBadRequest, "invalid file name")
		return
	}
	if r.URL.Query().Get("type") != "file" {
		writeError(w, http.StatusBadRequest, "unsupported upload type")
		return
	}
	if r.ContentLength > s.config.MaxAssetBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "file exceeds configured size limit")
		return
	}
	if err := os.MkdirAll(s.config.AssetDir, 0750); err != nil {
		writeError(w, http.StatusInternalServerError, "create asset directory")
		return
	}
	temporary, err := os.CreateTemp(s.config.AssetDir, ".upload-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create temporary upload")
		return
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(r.Body, s.config.MaxAssetBytes+1))
	closeErr := temporary.Close()
	if copyErr != nil || closeErr != nil {
		writeError(w, http.StatusBadRequest, "read upload")
		return
	}
	if n > s.config.MaxAssetBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "file exceeds configured size limit")
		return
	}
	asset := Asset{ID: randomID(), Name: name, Path: filepath.Join(s.config.AssetDir, randomID()+".blob"), SHA256: fmt.Sprintf("%x", hash.Sum(nil)), Size: n, CreatedBy: currentUser(r).Username, CreatedAt: time.Now().UTC()}
	// Use the asset id in the file path as well; regenerate only once before persistence.
	asset.Path = filepath.Join(s.config.AssetDir, asset.ID+".blob")
	if err := os.Rename(temporaryPath, asset.Path); err != nil {
		writeError(w, http.StatusInternalServerError, "finalize upload")
		return
	}
	stored, err := s.store.CreateAsset(asset)
	if err != nil {
		_ = os.Remove(asset.Path)
		if errors.Is(err, ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "file name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "store asset")
		return
	}
	s.audit(currentUser(r), "asset.upload", stored.ID, map[string]any{"name": stored.Name, "size": stored.Size, "sha256": stored.SHA256})
	writeJSON(w, http.StatusOK, stored)
}

type downloadGrant struct {
	AssetID  string `json:"asset_id"`
	TaskID   string `json:"task_id"`
	DeviceID string `json:"device_id"`
	Expires  int64  `json:"expires"`
}

func (s *Server) issueDownloadGrant(assetID, taskID, deviceID string) string {
	payload, _ := json.Marshal(downloadGrant{AssetID: assetID, TaskID: taskID, DeviceID: deviceID, Expires: time.Now().UTC().Add(15 * time.Minute).Unix()})
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, s.config.JWTSecret)
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) parseDownloadGrant(raw string) (downloadGrant, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return downloadGrant{}, ErrInvalidToken
	}
	mac := hmac.New(sha256.New, s.config.JWTSecret)
	_, _ = mac.Write([]byte(parts[0]))
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(signature, mac.Sum(nil)) {
		return downloadGrant{}, ErrInvalidToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return downloadGrant{}, ErrInvalidToken
	}
	var grant downloadGrant
	if json.Unmarshal(payload, &grant) != nil || grant.AssetID == "" || time.Now().UTC().Unix() > grant.Expires {
		return downloadGrant{}, ErrInvalidToken
	}
	return grant, nil
}

func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	grant, err := s.parseDownloadGrant(r.URL.Query().Get("grant"))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid download grant")
		return
	}
	asset, err := s.store.Asset(grant.AssetID)
	if err != nil {
		writeError(w, http.StatusNotFound, "asset not found")
		return
	}
	task, err := s.store.Task(grant.TaskID)
	if err != nil || task.AssetID != asset.ID {
		writeError(w, http.StatusUnauthorized, "invalid download grant")
		return
	}
	target, ok := task.Devices[grant.DeviceID]
	if !ok || (target.Status != TaskLeased && target.Status != TaskRunning) {
		writeError(w, http.StatusUnauthorized, "invalid download grant")
		return
	}
	if filepath.Base(r.URL.Path) != asset.Name {
		writeError(w, http.StatusNotFound, "asset not found")
		return
	}
	file, err := os.Open(asset.Path)
	if err != nil {
		writeError(w, http.StatusNotFound, "asset content unavailable")
		return
	}
	defer file.Close()
	w.Header().Set("Content-Length", fmt.Sprintf("%d", asset.Size))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+strings.ReplaceAll(asset.Name, "\"", "")+"\"")
	_, _ = io.Copy(w, file)
}

func (s *Server) tasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.store.TasksFor(currentUser(r)))
	case http.MethodPost:
		s.createTask(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	if !s.requireCapability(w, r, "batch_tasks") {
		return
	}
	var request struct {
		Type     string   `json:"type"`
		Targets  []string `json:"targets"`
		Payload  string   `json:"payload"`
		DestPath string   `json:"dest_path"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid task request")
		return
	}
	allowed := map[string]bool{"shell": true, "install": true, "push_file": true, "open_app": true, "uninstall": true}
	if !allowed[request.Type] || len(request.Targets) == 0 {
		writeError(w, http.StatusBadRequest, "unsupported task")
		return
	}
	user := currentUser(r)
	if request.Type == "shell" && !user.Can("shell") {
		writeError(w, http.StatusForbidden, "shell permission required")
		return
	}
	devices := make(map[string]TaskDevice, len(request.Targets))
	for _, deviceID := range request.Targets {
		if deviceID == "" || !s.store.CanAccessDevice(user, deviceID) {
			writeError(w, http.StatusForbidden, "target device access denied")
			return
		}
		devices[deviceID] = TaskDevice{DeviceID: deviceID}
	}
	task := Task{Type: request.Type, Payload: request.Payload, DestPath: request.DestPath, CreatedBy: user.Username, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(s.config.TaskTTL), Devices: devices}
	if request.Type == "install" || request.Type == "push_file" {
		if !user.Can("file_library") {
			writeError(w, http.StatusForbidden, "file_library permission required")
			return
		}
		parsed, err := url.Parse(request.Payload)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid asset URL")
			return
		}
		asset, err := s.store.AssetByName(filepath.Base(parsed.Path))
		if err != nil {
			writeError(w, http.StatusBadRequest, "asset not found")
			return
		}
		task.AssetID, task.Payload = asset.ID, ""
	}
	stored, err := s.store.CreateTask(task)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create task")
		return
	}
	s.audit(user, "task.create", stored.ID, map[string]any{"type": stored.Type, "target_count": len(stored.Devices)})
	for deviceID := range stored.Devices {
		s.notifyTask(deviceID, stored.ID)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"status": "success", "task_id": stored.ID})
}

func (s *Server) taskDetails(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	task, err := s.store.Task(r.URL.Query().Get("task_id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	user := currentUser(r)
	if !s.canSeeTask(user, task) {
		writeError(w, http.StatusForbidden, "task access denied")
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) notifyTask(deviceID, taskID string) {
	message := map[string]any{"message_type": "task_available", "task_id": taskID, "device_id": deviceID}
	s.hub.sendAgent(deviceID, message)
	if s.redis != nil {
		s.redis.PublishAgent(deviceID, message)
	}
}

func (s *Server) audit(user User, action, target string, metadata map[string]any) {
	s.store.Audit(AuditEvent{Actor: user.Username, Action: action, Target: target, Metadata: metadata})
}
