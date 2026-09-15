package controlplane

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"reflect"
	"time"
)

type browserAccess struct {
	kind         string
	subject      string
	tokenVersion uint64
	accessToken  string
}

const (
	browserAccessUser  = "user"
	browserAccessShare = "share"
)

func (s *Server) browserAccessFromRequest(r *http.Request) (User, bool, browserAccess, error) {
	if ticket := r.URL.Query().Get("ticket"); ticket != "" {
		return s.browserAccessFromTicket(ticket)
	}
	user, err := s.userFromRequest(r)
	if err != nil {
		return User{}, false, browserAccess{}, err
	}
	return user, false, browserAccess{kind: browserAccessUser, subject: user.ID, tokenVersion: user.TokenVersion, accessToken: tokenDigest(bearerToken(r.Header.Get("Authorization")))}, nil
}

func (s *Server) browserAccessFromTicket(ticket string) (User, bool, browserAccess, error) {
	claims, err := parseBrowserTicket(s.config.JWTSecret, ticket)
	if err != nil {
		return User{}, false, browserAccess{}, err
	}
	access := browserAccess{kind: claims.Kind, subject: claims.Subject, tokenVersion: claims.TokenVersion, accessToken: claims.AccessToken}
	user, viewOnly, err := s.validateBrowserAccess(access)
	if err != nil {
		return User{}, false, browserAccess{}, err
	}
	return user, viewOnly, access, nil
}

func (s *Server) validateBrowserAccess(access browserAccess) (User, bool, error) {
	switch access.kind {
	case browserAccessUser:
		if s.store.UserTokenRevoked(access.accessToken) {
			return User{}, false, ErrInvalidAuth
		}
		user, err := s.userForToken(access.subject, access.tokenVersion)
		return user, false, err
	case browserAccessShare:
		share, err := s.shareForTicket(access.subject)
		if err != nil {
			return User{}, false, err
		}
		return userForShare(share), share.ViewOnly, nil
	default:
		return User{}, false, ErrInvalidAuth
	}
}

func userForShare(share Share) User {
	return User{
		ID: "share:" + share.Token, Username: "share", Role: RoleUser, AssignedDevices: []string{share.DeviceID},
		ExpiresAt: share.ExpiresAt, Settings: share.GuestSettings,
		ForbidBitrate: share.ForbidBitrate, ForbidFPS: share.ForbidFPS,
		ForbidResolution: share.ForbidResolution, ForbidAudio: share.ForbidAudio,
	}
}

func (s *Server) shareForTicket(subject string) (Share, error) {
	for _, share := range s.store.SharesFor("") {
		if hmac.Equal([]byte(subject), []byte(s.shareTicketSubject(share.Token))) {
			return share, nil
		}
	}
	return Share{}, ErrInvalidAuth
}

func (s *Server) shareTicketSubject(token string) string {
	mac := hmac.New(sha256.New, s.config.JWTSecret)
	_, _ = mac.Write([]byte("scrcpycat-share-websocket-ticket:"))
	_, _ = mac.Write([]byte(token))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) watchBrowserAccess(access browserAccess, peer *browserPeer) func() {
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				user, viewOnly, err := s.validateBrowserAccess(access)
				if err != nil || viewOnly != peer.viewOnly || !reflect.DeepEqual(user, peer.user) {
					_ = peer.socket.close()
					return
				}
			}
		}
	}()
	return func() { close(stop) }
}

func (s *Server) constrainedStreamOptions(user User, deviceID string, raw map[string]any) map[string]any {
	result := cloneMap(raw)
	if result == nil {
		result = map[string]any{}
	}
	if user.Role != RoleAdmin {
		delete(result, "snapshot_interval")
		delete(result, "snapshotInterval")
	}
	settings := s.agentSettings(deviceID)
	for key, value := range user.Settings {
		settings[key] = value
	}
	setting := func(key string, fallback any) any {
		if value, ok := settings[key]; ok {
			return value
		}
		return fallback
	}
	if user.ForbidResolution {
		result["max_size"] = setting("size", float64(0))
	}
	if user.ForbidFPS {
		result["max_fps"] = setting("fps", float64(0))
		result["fps"] = result["max_fps"]
	}
	if user.ForbidBitrate {
		bitrate, ok := setting("bitrate", float64(4)).(float64)
		if !ok {
			bitrate = 4
		}
		if bitrate < 10000 {
			bitrate *= 1000000
		}
		result["bitrate"], result["min_bitrate"], result["max_bitrate"] = bitrate, bitrate, bitrate
		result["bwe"] = false
	}
	if user.ForbidAudio {
		result["audio"] = setting("audio", false)
	}
	return result
}
