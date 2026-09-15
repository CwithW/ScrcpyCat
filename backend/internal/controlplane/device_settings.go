package controlplane

import (
	"fmt"
	"math"
	"net/http"
)

func (s *MemoryStore) DeviceSettings(id string) map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneMap(s.deviceSettings[id])
}

func (s *MemoryStore) SetDeviceSettings(id string, settings map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.devices[id]; !ok {
		return ErrNotFound
	}
	if len(settings) == 0 {
		delete(s.deviceSettings, id)
	} else {
		s.deviceSettings[id] = cloneMap(settings)
	}
	return nil
}

func (s *PostgresStore) DeviceSettings(id string) map[string]any {
	return s.inner.DeviceSettings(id)
}

func (s *PostgresStore) SetDeviceSettings(id string, settings map[string]any) error {
	if err := s.inner.SetDeviceSettings(id, settings); err != nil {
		return err
	}
	return s.persist()
}

func validateDeviceSettings(settings map[string]any) error {
	if value, ok := settings["snapshotInterval"]; ok {
		interval, ok := value.(float64)
		if !ok || math.IsNaN(interval) || interval != math.Trunc(interval) || interval < -1 || interval > 3600 {
			return fmt.Errorf("snapshotInterval must be an integer between -1 and 3600")
		}
	}
	return nil
}

func (s *Server) agentSettings(id string) map[string]any {
	settings := s.store.DefaultSettings()
	if settings == nil {
		settings = map[string]any{}
	}
	if _, ok := settings["snapshotInterval"]; !ok {
		settings["snapshotInterval"] = float64(10)
	}
	for key, value := range s.store.DeviceSettings(id) {
		settings[key] = value
	}
	return settings
}

func (s *Server) allDeviceSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	result := map[string]map[string]any{}
	for _, device := range s.store.DevicesFor(currentUser(r)) {
		if settings := s.store.DeviceSettings(device.ID); len(settings) > 0 {
			result[device.ID] = settings
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) deviceSettings(w http.ResponseWriter, r *http.Request, id string) {
	if !s.store.CanAccessDevice(currentUser(r), id) {
		writeError(w, http.StatusForbidden, "device access denied")
		return
	}
	if _, err := s.store.Device(id); err != nil {
		writeError(w, http.StatusNotFound, "device not found")
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, s.store.DeviceSettings(id))
		return
	}
	if currentUser(r).Role != RoleAdmin {
		writeError(w, http.StatusForbidden, "admin permission required")
		return
	}
	settings := map[string]any{}
	switch r.Method {
	case http.MethodPost, http.MethodPut:
		if err := decodeJSON(r, &settings); err != nil {
			writeError(w, http.StatusBadRequest, "invalid settings")
			return
		}
		if err := validateDeviceSettings(settings); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	case http.MethodDelete:
	default:
		methodNotAllowed(w)
		return
	}
	if err := s.store.SetDeviceSettings(id, settings); err != nil {
		writeError(w, http.StatusServiceUnavailable, "save device settings")
		return
	}
	s.hub.sendAgent(id, map[string]any{"message_type": "agent_settings", "settings": s.agentSettings(id)})
	s.hub.broadcast(map[string]any{"message_type": "device_settings_updated", "device_id": id, "settings": settings})
	writeJSON(w, http.StatusOK, settings)
}
