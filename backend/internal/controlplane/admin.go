package controlplane

import (
	"errors"
	"net/http"
	"strings"
	"time"
)

func (s *Server) deviceByID(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/settings") {
		s.deviceSettings(w, r, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/devices/"), "/settings"))
		return
	}
	if strings.HasSuffix(r.URL.Path, "/snapshot") {
		s.deviceSnapshot(w, r, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/devices/"), "/snapshot"))
		return
	}
	if r.Method != http.MethodDelete {
		methodNotAllowed(w)
		return
	}
	if currentUser(r).Role != RoleAdmin {
		writeError(w, http.StatusForbidden, "admin permission required")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/devices/")
	if id == "" || s.store.DeleteOfflineDevice(id) != nil {
		writeError(w, http.StatusBadRequest, "device must exist and be offline")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminAssign(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var body struct {
		Username string   `json:"username"`
		Devices  []string `json:"devices"`
	}
	if decodeJSON(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "invalid assignment")
		return
	}
	user, err := s.store.UpdateUser(body.Username, func(user *User) error {
		user.AssignedDevices = append([]string(nil), body.Devices...)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) adminCreateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var body struct {
		Username      string `json:"username"`
		Password      string `json:"password"`
		Role          Role   `json:"role"`
		Note          string `json:"note"`
		ExpireSeconds int64  `json:"expire_seconds"`
	}
	if decodeJSON(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "invalid user request")
		return
	}
	if body.Role == "" {
		body.Role = RoleUser
	}
	user, err := s.store.CreateUser(body.Username, body.Password, body.Role, nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user, err = s.store.UpdateUser(user.Username, func(user *User) error {
		user.Note = body.Note
		if body.ExpireSeconds > 0 {
			expires := time.Now().UTC().Add(time.Duration(body.ExpireSeconds) * time.Second)
			user.ExpiresAt = &expires
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "save user")
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

func (s *Server) adminDeleteUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
	}
	if r.Method != http.MethodPost || decodeJSON(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "invalid user request")
		return
	}
	if err := s.store.DeleteUser(body.Username); err != nil {
		writeError(w, http.StatusBadRequest, "cannot delete user")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminResetPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if r.Method != http.MethodPost || decodeJSON(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "invalid password reset")
		return
	}
	if err := s.store.ResetPassword(body.Username, body.Password); err != nil {
		writeError(w, http.StatusBadRequest, "password reset failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) adminUpdateNote(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Note     string `json:"note"`
	}
	if r.Method != http.MethodPost || decodeJSON(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "invalid note request")
		return
	}
	user, err := s.store.UpdateUser(body.Username, func(user *User) error { user.Note = body.Note; return nil })
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) adminRenameUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OldUsername string `json:"old_username"`
		NewUsername string `json:"new_username"`
	}
	if r.Method != http.MethodPost || decodeJSON(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "invalid rename request")
		return
	}
	user, err := s.store.RenameUser(body.OldUsername, body.NewUsername)
	if err != nil {
		writeError(w, http.StatusBadRequest, "rename failed")
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) adminUpdateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username         string               `json:"username"`
		ForbidBitrate    bool                 `json:"forbid_bitrate"`
		ForbidFPS        bool                 `json:"forbid_fps"`
		ForbidResolution bool                 `json:"forbid_resolution"`
		ForbidAudio      bool                 `json:"forbid_audio"`
		Permissions      OperationPermissions `json:"permissions"`
		Settings         map[string]any       `json:"settings"`
		ExpireSeconds    int64                `json:"expire_seconds"`
	}
	if r.Method != http.MethodPost || decodeJSON(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "invalid user update")
		return
	}
	user, err := s.store.UpdateUser(body.Username, func(user *User) error {
		user.ForbidBitrate = body.ForbidBitrate
		user.ForbidFPS = body.ForbidFPS
		user.ForbidResolution = body.ForbidResolution
		user.ForbidAudio = body.ForbidAudio
		user.Permissions = body.Permissions
		user.Settings = cloneMap(body.Settings)
		if body.ExpireSeconds == 0 {
			user.ExpiresAt = nil
		} else if body.ExpireSeconds > 0 {
			expires := time.Now().UTC().Add(time.Duration(body.ExpireSeconds) * time.Second)
			user.ExpiresAt = &expires
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) adminKickUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		DeviceID string `json:"device_id"`
	}
	if r.Method != http.MethodPost || decodeJSON(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "invalid kick request")
		return
	}
	s.hub.disconnectBrowsers(body.Username, body.DeviceID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) shareCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var body struct {
		DeviceID         string `json:"device_id"`
		ExpireSeconds    int64  `json:"expire_seconds"`
		AccessMode       string `json:"access_mode"`
		Password         string `json:"password"`
		Description      string `json:"description"`
		ForbidBitrate    bool   `json:"forbid_bitrate"`
		ForbidFPS        bool   `json:"forbid_fps"`
		ForbidResolution bool   `json:"forbid_resolution"`
		ForbidAudio      bool   `json:"forbid_audio"`
	}
	if decodeJSON(r, &body) != nil || !s.store.CanAccessDevice(currentUser(r), body.DeviceID) {
		writeError(w, http.StatusBadRequest, "invalid share request")
		return
	}
	if body.AccessMode != "" && body.AccessMode != "full" && body.AccessMode != "view" && body.AccessMode != "view_only" {
		writeError(w, http.StatusBadRequest, "invalid access mode")
		return
	}
	var expiresAt *time.Time
	if body.ExpireSeconds > 0 {
		expires := time.Now().UTC().Add(time.Duration(body.ExpireSeconds) * time.Second)
		expiresAt = &expires
	}
	share, err := s.store.CreateShare(body.DeviceID, currentUser(r).Username, body.Password, body.Description, body.AccessMode == "view" || body.AccessMode == "view_only", expiresAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create share")
		return
	}
	share, err = s.store.UpdateShare(share.Token, func(value *Share) error {
		value.ForbidBitrate, value.ForbidFPS = body.ForbidBitrate, body.ForbidFPS
		value.ForbidResolution, value.ForbidAudio = body.ForbidResolution, body.ForbidAudio
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "save share settings")
		return
	}
	s.audit(currentUser(r), "share.create", share.Token, map[string]any{"device_id": share.DeviceID})
	writeJSON(w, http.StatusCreated, map[string]any{"code": 0, "data": shareJSON(share, body.Password)})
}

func (s *Server) shareList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	deviceID := r.URL.Query().Get("device_id")
	shares := s.store.SharesFor(deviceID)
	result := make([]map[string]any, 0, len(shares))
	for _, share := range shares {
		if s.store.CanAccessDevice(currentUser(r), share.DeviceID) {
			result = append(result, shareJSON(share, ""))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": 0, "data": result})
}

func (s *Server) shareRevoke(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if r.Method != http.MethodPost || decodeJSON(r, &body) != nil || body.Token == "" {
		writeError(w, http.StatusBadRequest, "invalid share revoke")
		return
	}
	share, err := s.store.Share(body.Token)
	if err == nil && !s.store.CanAccessDevice(currentUser(r), share.DeviceID) {
		writeError(w, http.StatusForbidden, "share access denied")
		return
	}
	if err := s.store.RevokeShare(body.Token); err != nil && !errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusBadRequest, "revoke share")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": 0})
}

func (s *Server) shareUpdate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token            string         `json:"token"`
		ForbidBitrate    bool           `json:"forbid_bitrate"`
		ForbidFPS        bool           `json:"forbid_fps"`
		ForbidResolution bool           `json:"forbid_resolution"`
		ForbidAudio      bool           `json:"forbid_audio"`
		GuestSettings    map[string]any `json:"guest_settings"`
	}
	if r.Method != http.MethodPost || decodeJSON(r, &body) != nil || body.Token == "" {
		writeError(w, http.StatusBadRequest, "invalid share update")
		return
	}
	share, err := s.store.Share(body.Token)
	if err != nil || !s.store.CanAccessDevice(currentUser(r), share.DeviceID) {
		writeError(w, http.StatusForbidden, "share access denied")
		return
	}
	updated, err := s.store.UpdateShare(body.Token, func(share *Share) error {
		share.ForbidBitrate, share.ForbidFPS = body.ForbidBitrate, body.ForbidFPS
		share.ForbidResolution, share.ForbidAudio = body.ForbidResolution, body.ForbidAudio
		share.GuestSettings = cloneMap(body.GuestSettings)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update share")
		return
	}
	s.audit(currentUser(r), "share.update", updated.Token, nil)
	writeJSON(w, http.StatusOK, map[string]any{"code": 0, "data": shareJSON(updated, "")})
}

func (s *Server) shareExtend(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token         string `json:"token"`
		ExtendSeconds int64  `json:"extend_seconds"`
	}
	if r.Method != http.MethodPost || decodeJSON(r, &body) != nil || body.Token == "" || body.ExtendSeconds <= 0 {
		writeError(w, http.StatusBadRequest, "invalid share extension")
		return
	}
	share, err := s.store.Share(body.Token)
	if err != nil || !s.store.CanAccessDevice(currentUser(r), share.DeviceID) {
		writeError(w, http.StatusForbidden, "share access denied")
		return
	}
	updated, err := s.store.UpdateShare(body.Token, func(share *Share) error {
		base := time.Now().UTC()
		if share.ExpiresAt != nil && share.ExpiresAt.After(base) {
			base = *share.ExpiresAt
		}
		next := base.Add(time.Duration(body.ExtendSeconds) * time.Second)
		share.ExpiresAt = &next
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "extend share")
		return
	}
	s.audit(currentUser(r), "share.extend", updated.Token, map[string]any{"seconds": body.ExtendSeconds})
	writeJSON(w, http.StatusOK, map[string]any{"code": 0, "data": shareJSON(updated, "")})
}

func (s *Server) shareRedeemCard(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CardCode string `json:"card_code"`
		Password string `json:"password"`
	}
	if r.Method != http.MethodPost || decodeJSON(r, &body) != nil || body.CardCode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": 400, "msg": "invalid card"})
		return
	}
	share, err := s.store.ShareByCard(body.CardCode)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"code": 404, "msg": "card not found"})
		return
	}
	if _, err := s.store.AuthorizeShare(share.Token, body.Password); errors.Is(err, ErrInvalidAuth) {
		writeJSON(w, http.StatusOK, map[string]any{"code": 401, "msg": "password required or incorrect"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"code": 404, "msg": "card not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": 0, "data": map[string]any{"token": share.Token}})
}

func (s *Server) shareInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if decodeJSON(r, &body) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": 400, "msg": "invalid share request"})
		return
	}
	share, err := s.store.AuthorizeShare(body.Token, body.Password)
	if errors.Is(err, ErrInvalidAuth) {
		writeJSON(w, http.StatusOK, map[string]any{"code": 401, "msg": "password required or incorrect"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"code": 404, "msg": "share not found"})
		return
	}
	remaining := int64(-1)
	if share.ExpiresAt != nil {
		remaining = int64(time.Until(*share.ExpiresAt).Seconds())
		if remaining < 0 {
			remaining = 0
		}
	}
	data := shareJSON(share, "")
	data["remaining_seconds"] = remaining
	writeJSON(w, http.StatusOK, map[string]any{"code": 0, "data": data})
}

func shareJSON(share Share, password string) map[string]any {
	card := share.CardCode
	if password != "" {
		card = share.CardCode
	}
	return map[string]any{"token": share.Token, "token_id": share.Token, "card_code": card, "device_id": share.DeviceID, "view_only": share.ViewOnly, "access_mode": map[bool]string{true: "view_only", false: "full"}[share.ViewOnly], "description": share.Description, "expires_at": share.ExpiresAt, "created_at": share.CreatedAt, "created_by": share.CreatedBy, "has_password": len(share.PasswordHash) > 0, "forbid_bitrate": share.ForbidBitrate, "forbid_fps": share.ForbidFPS, "forbid_resolution": share.ForbidResolution, "forbid_audio": share.ForbidAudio, "guest_settings": share.GuestSettings}
}
