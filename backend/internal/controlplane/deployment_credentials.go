package controlplane

import (
	"errors"
	"net/http"
	"time"
)

func (s *Server) deploymentCredentials(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.store.Healthy() != nil {
		writeError(w, http.StatusServiceUnavailable, "deployment credential store unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.store.DeploymentCredentials())
	case http.MethodPost:
		var body struct {
			Name       string `json:"name"`
			TTLSeconds int64  `json:"ttl_seconds"`
		}
		// Guard duration multiplication; omitted/zero TTL means no expiration.
		if decodeJSON(r, &body) != nil || body.TTLSeconds < 0 || body.TTLSeconds > int64((1<<63-1)/time.Second) {
			writeError(w, http.StatusBadRequest, "name and a nonnegative ttl_seconds are required")
			return
		}
		credential, token, err := s.store.CreateDeploymentCredential(body.Name, currentUser(r).Username, time.Duration(body.TTLSeconds)*time.Second)
		if errors.Is(err, errInvalidDeploymentRequest) {
			writeError(w, http.StatusBadRequest, "name must contain 1 to 80 characters")
			return
		}
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "deployment credential store unavailable")
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"credential": credential, "token": token})
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) revokeDeploymentCredential(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if decodeJSON(r, &body) != nil || body.ID == "" {
		writeError(w, http.StatusBadRequest, "credential id is required")
		return
	}
	credential, err := s.store.RevokeDeploymentCredential(body.ID, currentUser(r).Username)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "deployment credential not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "deployment credential store unavailable")
		return
	}
	writeJSON(w, http.StatusOK, credential)
}

// This endpoint accepts only the dedicated credential in the Authorization header.
// It never converts that credential to a User or grants access to other handlers.
func (s *Server) deploymentEnrollmentTokens(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	token := bearerToken(r.Header.Get("Authorization"))
	if token == "" {
		writeError(w, http.StatusUnauthorized, "deployment credential required")
		return
	}
	var body struct {
		DeviceID   string `json:"device_id"`
		TTLSeconds int64  `json:"ttl_seconds"`
	}
	if decodeJSON(r, &body) != nil || body.TTLSeconds < 0 || body.TTLSeconds > int64(maxDeploymentEnrollmentTTL/time.Second) {
		writeError(w, http.StatusBadRequest, "device_id and ttl_seconds from 1 to 900 are required")
		return
	}
	if body.TTLSeconds == 0 {
		body.TTLSeconds = int64(maxDeploymentEnrollmentTTL / time.Second)
	}
	enrollment, err := s.store.IssueDeploymentEnrollment(token, body.DeviceID, time.Duration(body.TTLSeconds)*time.Second)
	switch {
	case errors.Is(err, ErrInvalidAuth):
		writeError(w, http.StatusUnauthorized, "deployment credential is invalid, expired or revoked")
	case errors.Is(err, errInvalidDeploymentRequest):
		writeError(w, http.StatusBadRequest, "a valid device_id is required")
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, "deployment credential store unavailable")
	default:
		writeJSON(w, http.StatusCreated, map[string]any{"token": enrollment.Token, "expires_at": enrollment.ExpiresAt})
	}
}
