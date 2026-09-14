package controlplane

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	browserTicketAudience = "connect_client"
	browserTicketTTL      = time.Minute
)

type browserTicketClaims struct {
	Kind         string `json:"kind"`
	TokenVersion uint64 `json:"token_version,omitempty"`
	jwt.RegisteredClaims
}

func (s *Server) websocketTicket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var body struct {
		ShareToken    string `json:"share_token"`
		SharePassword string `json:"share_password"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid websocket ticket request")
			return
		}
	}
	if strings.TrimSpace(body.ShareToken) != "" {
		share, err := s.store.AuthorizeShare(body.ShareToken, body.SharePassword)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		ticket, expiresAt, err := issueBrowserTicket(s.config.JWTSecret, browserAccessShare, s.shareTicketSubject(share.Token), 0)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "issue websocket ticket")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ticket": ticket, "expires_at": expiresAt})
		return
	}
	user, err := s.userFromRequest(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	ticket, expiresAt, err := issueBrowserTicket(s.config.JWTSecret, browserAccessUser, user.ID, user.TokenVersion)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "issue websocket ticket")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ticket": ticket, "expires_at": expiresAt})
}

func issueBrowserTicket(secret []byte, kind, subject string, tokenVersion uint64) (string, time.Time, error) {
	now := time.Now().UTC()
	expiresAt := now.Add(browserTicketTTL)
	ticket, err := jwt.NewWithClaims(jwt.SigningMethodHS256, browserTicketClaims{
		Kind:         kind,
		TokenVersion: tokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subject,
			Audience:  jwt.ClaimStrings{browserTicketAudience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}).SignedString(secret)
	return ticket, expiresAt, err
}

func parseBrowserTicket(secret []byte, raw string) (browserTicketClaims, error) {
	parsed, err := jwt.ParseWithClaims(raw, &browserTicketClaims{}, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return secret, nil
	})
	if err != nil || !parsed.Valid {
		return browserTicketClaims{}, ErrInvalidAuth
	}
	claims, ok := parsed.Claims.(*browserTicketClaims)
	if !ok || claims.Subject == "" || (claims.Kind != browserAccessUser && claims.Kind != browserAccessShare) || !containsAudience(claims.Audience, browserTicketAudience) {
		return browserTicketClaims{}, ErrInvalidAuth
	}
	return *claims, nil
}

func containsAudience(audiences jwt.ClaimStrings, expected string) bool {
	for _, audience := range audiences {
		if audience == expected {
			return true
		}
	}
	return false
}
