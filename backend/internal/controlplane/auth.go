package controlplane

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type claims struct {
	Role         Role   `json:"role"`
	TokenVersion uint64 `json:"token_version"`
	TokenUse     string `json:"token_use"`
	jwt.RegisteredClaims
}

type contextKey string

const userContextKey contextKey = "authenticated-user"

const userTokenTTL = 7 * 24 * time.Hour

const userTokenUse = "user_access"

func issueUserToken(secret []byte, user User) (string, error) {
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims{
		Role:         user.Role,
		TokenVersion: user.TokenVersion,
		TokenUse:     userTokenUse,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID,
			IssuedAt:  jwt.NewNumericDate(time.Now().UTC()),
			ExpiresAt: jwt.NewNumericDate(time.Now().UTC().Add(userTokenTTL)),
		},
	}).SignedString(secret)
}

func parseUserToken(secret []byte, raw string) (claims, error) {
	parsed, err := jwt.ParseWithClaims(raw, &claims{}, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return secret, nil
	})
	if err != nil || !parsed.Valid {
		return claims{}, ErrInvalidAuth
	}
	result, ok := parsed.Claims.(*claims)
	if !ok || result.Subject == "" || result.TokenUse != userTokenUse {
		return claims{}, ErrInvalidAuth
	}
	return *result, nil
}

func (s *Server) requireUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, err := s.userFromRequest(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userContextKey, user)))
	}
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return s.requireUser(func(w http.ResponseWriter, r *http.Request) {
		if currentUser(r).Role != RoleAdmin {
			writeError(w, http.StatusForbidden, "admin permission required")
			return
		}
		next(w, r)
	})
}

func (s *Server) userFromRequest(r *http.Request) (User, error) {
	token := bearerToken(r.Header.Get("Authorization"))
	if token == "" {
		return User{}, ErrInvalidAuth
	}
	parsed, err := parseUserToken(s.config.JWTSecret, token)
	if err != nil {
		return User{}, err
	}
	return s.userForToken(parsed.Subject, parsed.TokenVersion)
}

func (s *Server) userForToken(userID string, tokenVersion uint64) (User, error) {
	user, err := s.store.User(userID)
	if err != nil || user.TokenVersion != tokenVersion || (user.ExpiresAt != nil && time.Now().UTC().After(*user.ExpiresAt)) {
		return User{}, ErrInvalidAuth
	}
	return user, nil
}

func currentUser(r *http.Request) User {
	user, _ := r.Context().Value(userContextKey).(User)
	return user
}

func bearerToken(header string) string {
	parts := strings.Fields(header)
	if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
		return parts[1]
	}
	return ""
}
