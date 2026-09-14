package controlplane

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestUserJWTLifetimeTicketScopeAndPasswordResetRevocation(t *testing.T) {
	server := newTestServer(t)
	user, err := server.store.CreateUser("ticket-user", "password", RoleUser, nil)
	if err != nil {
		t.Fatal(err)
	}
	userToken, err := issueUserToken(server.config.JWTSecret, user)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := parseUserToken(server.config.JWTSecret, userToken)
	if err != nil {
		t.Fatal(err)
	}
	if claims.ExpiresAt == nil || claims.IssuedAt == nil || claims.ExpiresAt.Time.Sub(claims.IssuedAt.Time) != userTokenTTL {
		t.Fatalf("unexpected JWT lifetime: %#v", claims)
	}

	queryRequest := httptest.NewRequest(http.MethodGet, "/api/me?token="+url.QueryEscape(userToken), nil)
	queryResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(queryResponse, queryRequest)
	if queryResponse.Code != http.StatusUnauthorized {
		t.Fatalf("query JWT was accepted: %d", queryResponse.Code)
	}

	ticket := issueUserWebSocketTicket(t, server, userToken)
	if strings.Contains(ticket, userToken) {
		t.Fatal("websocket ticket embeds the user JWT")
	}
	restRequest := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	restRequest.Header.Set("Authorization", "Bearer "+ticket)
	restResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(restResponse, restRequest)
	if restResponse.Code != http.StatusUnauthorized {
		t.Fatalf("websocket ticket accessed REST API: %d", restResponse.Code)
	}

	httpServer := newIPv4HTTPServer(t, server.Handler())
	defer httpServer.Close()
	wsBase := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	browser, _, err := websocket.DefaultDialer.Dial(wsBase+"/connect_client?ticket="+url.QueryEscape(ticket), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()

	if err := server.store.ResetPassword(user.Username, "new-password"); err != nil {
		t.Fatal(err)
	}
	oldTokenRequest := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	oldTokenRequest.Header.Set("Authorization", "Bearer "+userToken)
	oldTokenResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(oldTokenResponse, oldTokenRequest)
	if oldTokenResponse.Code != http.StatusUnauthorized {
		t.Fatalf("password reset left the old JWT valid: %d", oldTokenResponse.Code)
	}
	_ = browser.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := browser.ReadMessage(); err == nil {
		t.Fatal("password reset left the active websocket open")
	}

	updatedUser, err := server.store.Authenticate(user.Username, "new-password")
	if err != nil {
		t.Fatal(err)
	}
	newToken, err := issueUserToken(server.config.JWTSecret, updatedUser)
	if err != nil {
		t.Fatal(err)
	}
	newTokenRequest := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	newTokenRequest.Header.Set("Authorization", "Bearer "+newToken)
	newTokenResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(newTokenResponse, newTokenRequest)
	if newTokenResponse.Code != http.StatusOK {
		t.Fatalf("new JWT was rejected: %d", newTokenResponse.Code)
	}
}

func TestPersistedUserKeepsTokenVersion(t *testing.T) {
	raw, err := json.Marshal(persistedUser{User: User{ID: "user-1", TokenVersion: 9}, TokenVersion: 9})
	if err != nil {
		t.Fatal(err)
	}
	var saved persistedUser
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.TokenVersion != 9 {
		t.Fatalf("persisted token version = %d", saved.TokenVersion)
	}
}

func TestShareWebSocketTicketDoesNotExposeShareCredentials(t *testing.T) {
	server := newTestServer(t)
	share, err := server.store.CreateShare("phone", "admin", "share-password", "", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"share_token": share.Token, "share_password": "share-password"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/ws-ticket", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("share ticket request failed: %d %s", response.Code, response.Body.String())
	}
	var payload struct {
		Ticket string `json:"ticket"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Ticket == "" || strings.Contains(payload.Ticket, share.Token) || strings.Contains(payload.Ticket, "share-password") {
		t.Fatal("share ticket leaks a raw share credential")
	}
	legacyInfoRequest := httptest.NewRequest(http.MethodGet, "/api/share/info?token="+url.QueryEscape(share.Token)+"&password=share-password", nil)
	legacyInfoResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(legacyInfoResponse, legacyInfoRequest)
	if legacyInfoResponse.Code != http.StatusMethodNotAllowed {
		t.Fatalf("legacy share password query was accepted: %d", legacyInfoResponse.Code)
	}

	httpServer := newIPv4HTTPServer(t, server.Handler())
	defer httpServer.Close()
	wsBase := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	legacyURL := wsBase + "/connect_client?share_token=" + url.QueryEscape(share.Token) + "&share_pwd=share-password"
	if conn, response, err := websocket.DefaultDialer.Dial(legacyURL, nil); err == nil {
		_ = conn.Close()
		t.Fatal("legacy share query credentials were accepted")
	} else if response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("legacy share query credentials returned %#v", response)
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsBase+"/connect_client?ticket="+url.QueryEscape(payload.Ticket), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
}

func TestLoginLimiterAndTrustedProxyHandling(t *testing.T) {
	server := newTestServer(t)
	server.loginLimiter = newLoginLimiter(Config{LoginAttemptWindow: time.Hour, LoginMaxAttemptsPerAccount: 2, LoginMaxAttemptsPerIP: 10})
	login := func(username, password, remoteAddr string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"`+username+`","password":"`+password+`"}`))
		request.RemoteAddr = remoteAddr
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response
	}
	if response := login("admin", "wrong", "198.51.100.10:1000"); response.Code != http.StatusUnauthorized {
		t.Fatalf("first failed login returned %d", response.Code)
	}
	if response := login("admin", "password", "198.51.100.10:1000"); response.Code != http.StatusOK {
		t.Fatalf("successful login returned %d", response.Code)
	}
	if response := login("admin", "wrong", "198.51.100.10:1000"); response.Code != http.StatusUnauthorized {
		t.Fatalf("first post-success failure returned %d", response.Code)
	}
	if response := login("admin", "wrong", "198.51.100.10:1000"); response.Code != http.StatusUnauthorized {
		t.Fatalf("second post-success failure returned %d", response.Code)
	}
	if response := login("admin", "wrong", "198.51.100.11:1000"); response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") == "" {
		t.Fatalf("account limiter did not reject repeated failures: %d %q", response.Code, response.Header().Get("Retry-After"))
	}

	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	trustedRequest := httptest.NewRequest(http.MethodPost, "/api/login", nil)
	trustedRequest.RemoteAddr = "10.0.0.8:443"
	trustedRequest.Header.Set("X-Forwarded-For", "203.0.113.4, 10.0.0.8")
	if got := requestClientIP(trustedRequest, trusted); got != "203.0.113.4" {
		t.Fatalf("trusted proxy client IP = %q", got)
	}
	untrustedRequest := httptest.NewRequest(http.MethodPost, "/api/login", nil)
	untrustedRequest.RemoteAddr = "198.51.100.8:443"
	untrustedRequest.Header.Set("X-Forwarded-For", "203.0.113.4")
	if got := requestClientIP(untrustedRequest, trusted); got != "198.51.100.8" {
		t.Fatalf("untrusted X-Forwarded-For was accepted: %q", got)
	}
}

func TestAgentHandshakeBudget(t *testing.T) {
	server := newTestServerWithConfig(t, Config{
		AgentHelloTimeout:         100 * time.Millisecond,
		AgentHelloMaxBytes:        512,
		MaxPendingAgentHandshakes: 1,
	})
	httpServer := newIPv4HTTPServer(t, server.Handler())
	defer httpServer.Close()
	wsBase := "ws" + strings.TrimPrefix(httpServer.URL, "http")

	first, _, err := websocket.DefaultDialer.Dial(wsBase+"/register_agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if conn, response, err := websocket.DefaultDialer.Dial(wsBase+"/register_agent", nil); err == nil {
		_ = conn.Close()
		t.Fatal("agent handshake capacity was not enforced")
	} else if response == nil || response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("agent handshake capacity response = %#v", response)
	}
	_ = first.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := first.ReadMessage(); err == nil {
		t.Fatal("idle agent handshake was not closed")
	}

	oversized, _, err := websocket.DefaultDialer.Dial(wsBase+"/register_agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := oversized.WriteMessage(websocket.TextMessage, bytes.Repeat([]byte("x"), 513)); err != nil {
		t.Fatal(err)
	}
	_ = oversized.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := oversized.ReadMessage(); err == nil {
		t.Fatal("oversized agent hello was accepted")
	}
	_ = oversized.Close()

	enrollment := server.store.CreateEnrollment("device-1", time.Minute)
	agent, _, err := websocket.DefaultDialer.Dial(wsBase+"/register_agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	if err := agent.WriteJSON(map[string]any{"message_type": "agent_hello", "device_id": "device-1", "enrollment_token": enrollment.Token}); err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := agent.ReadJSON(&config); err != nil {
		t.Fatal(err)
	}
	if config["message_type"] != "agent_config" {
		t.Fatalf("valid agent handshake failed: %#v", config)
	}
}

func issueUserWebSocketTicket(t *testing.T, server *Server, token string) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/ws-ticket", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("user ticket request failed: %d %s", response.Code, response.Body.String())
	}
	var payload struct {
		Ticket string `json:"ticket"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Ticket == "" {
		t.Fatal("user ticket response omitted ticket")
	}
	return payload.Ticket
}

func newIPv4HTTPServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server.Listener = listener
	server.Start()
	return server
}
