package controlplane

func (s *Server) syncPreview(deviceID string) {
	message := s.hub.selectedPreviewRequest(deviceID)
	if message == nil {
		message = map[string]any{"message_type": "stop_preview", "device_id": deviceID}
	}
	s.hub.sendAgent(deviceID, message)
}

// Injection remains a routed capability even when this Agent does not support
// GPS/sensors/camera. Clipboard shares the route and must keep working.
func (s *Server) injectData(peer *browserPeer, deviceID string, message map[string]any) {
	if peer.viewOnly {
		_ = peer.socket.writeJSON(map[string]any{"message_type": "error", "error": "device control denied"})
		return
	}
	targets := []string{deviceID}
	if raw, ok := message["target_device_ids"]; ok {
		if !decodeRealtimeValue(raw, &targets) || len(targets) == 0 || len(targets) > 1000 {
			_ = peer.socket.writeJSON(map[string]any{"message_type": "error", "error": "invalid injection targets"})
			return
		}
	}
	seen := map[string]bool{}
	for _, id := range targets {
		if id == "" || !s.store.CanAccessDevice(peer.user, id) || seen[id] {
			continue
		}
		seen[id] = true
		forwarded := withClient(message, peer.id)
		forwarded["device_id"] = id
		delete(forwarded, "target_device_ids")
		s.hub.sendAgent(id, forwarded)
	}
}
