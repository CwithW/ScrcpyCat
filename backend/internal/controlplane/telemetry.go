package controlplane

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

func (s *Server) snapshotPath(deviceID string) string {
	return filepath.Join(s.config.AssetDir, "snapshots", fmt.Sprintf("%x.png", sha256.Sum256([]byte(deviceID))))
}

func (s *Server) saveSnapshot(deviceID, encoded string) error {
	if len(encoded) > 1<<20 {
		return fmt.Errorf("snapshot too large")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return err
	}
	dimensions, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || dimensions.Width <= 0 || dimensions.Height <= 0 || dimensions.Width > 720 || dimensions.Height > 720 {
		return fmt.Errorf("invalid thumbnail")
	}
	path := s.snapshotPath(deviceID)
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".snapshot-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	s.hub.broadcast(map[string]any{"message_type": "snapshot_updated", "device_id": deviceID, "url": "/api/devices/" + url.PathEscape(deviceID) + "/snapshot"})
	return nil
}

func (s *Server) deviceSnapshot(w http.ResponseWriter, r *http.Request, deviceID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if !s.store.CanAccessDevice(currentUser(r), deviceID) {
		writeError(w, http.StatusForbidden, "device access denied")
		return
	}
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("Content-Type", "image/png")
	http.ServeFile(w, r, s.snapshotPath(deviceID))
}

func (s *Server) devicesFor(user User) []Device {
	devices := s.store.DevicesFor(user)
	for i := range devices {
		device := &devices[i]
		if _, err := os.Stat(s.snapshotPath(device.ID)); err == nil {
			device.SnapshotURL = "/api/devices/" + url.PathEscape(device.ID) + "/snapshot"
		}
		if device.Online {
			device.Clients = s.hub.deviceClients(device.ID)
			device.ClientCount = len(device.Clients)
			if metrics, ok := s.deviceMetrics.Load(device.ID); ok {
				device.Metrics, _ = metrics.(map[string]any)
			}
		}
	}
	return devices
}

func decodeRealtimeValue(raw any, destination any) bool {
	encoded, err := json.Marshal(raw)
	return err == nil && json.Unmarshal(encoded, destination) == nil
}

func (s *Server) canSeeTask(user User, task Task) bool {
	if user.Role == RoleAdmin || task.CreatedBy == user.Username {
		return true
	}
	if !user.Can("batch_tasks") {
		return false
	}
	for id := range task.Devices {
		if !s.store.CanAccessDevice(user, id) {
			return false
		}
	}
	return true
}

// Global events use the same authorization as REST, including Redis events.
func (s *Server) filterBroadcast(peer *browserPeer, raw any) any {
	message, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	if id := stringField(message, "device_id"); id != "" && !s.store.CanAccessDevice(peer.user, id) {
		return nil
	}
	switch stringField(message, "message_type") {
	case "device_list_update":
		s.hub.mu.RLock()
		bound := peer.deviceID != ""
		s.hub.mu.RUnlock()
		if bound {
			return nil
		}
		var devices []Device
		if !decodeRealtimeValue(message["devices"], &devices) {
			return nil
		}
		allowed := make([]Device, 0, len(devices))
		for _, device := range devices {
			if s.store.CanAccessDevice(peer.user, device.ID) {
				allowed = append(allowed, device)
			}
		}
		return map[string]any{"message_type": "device_list_update", "devices": allowed}
	case "task_status_updated":
		var task Task
		if !decodeRealtimeValue(message["task"], &task) || !s.canSeeTask(peer.user, task) {
			return nil
		}
	case "tags_update":
		var tags map[string][]string
		if decodeRealtimeValue(message["deviceTags"], &tags) {
			for id := range tags {
				if !s.store.CanAccessDevice(peer.user, id) {
					delete(tags, id)
				}
			}
			return map[string]any{"message_type": "tags_update", "tags": message["tags"], "deviceTags": tags}
		}
	}
	return message
}

func (h *realtimeHub) deviceClients(deviceID string) []map[string]any {
	h.mu.RLock()
	defer h.mu.RUnlock()
	clients := []map[string]any{}
	for _, peer := range h.browsers {
		if peer.deviceID != deviceID {
			continue
		}
		remaining := int64(-1)
		if peer.user.ExpiresAt != nil {
			remaining = max(0, int64(time.Until(*peer.user.ExpiresAt).Seconds()))
		}
		name := peer.user.Username
		if name == "share" {
			name = "分享访客"
		}
		clients = append(clients, map[string]any{"name": name, "remaining_seconds": remaining, "view_only": peer.viewOnly})
	}
	return clients
}
