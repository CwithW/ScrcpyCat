package controlplane

import (
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func newTestServer(t *testing.T) *Server {
	return newTestServerWithConfig(t, Config{})
}

func newTestServerWithConfig(t *testing.T, config Config) *Server {
	t.Helper()
	store, err := NewMemoryStore("admin", "password")
	if err != nil {
		t.Fatal(err)
	}
	if len(config.JWTSecret) == 0 {
		config.JWTSecret = []byte("test-secret")
	}
	if config.BootstrapAdminUser == "" {
		config.BootstrapAdminUser = "admin"
	}
	if config.BootstrapAdminPass == "" {
		config.BootstrapAdminPass = "password"
	}
	if config.TaskTTL == 0 {
		config.TaskTTL = time.Hour
	}
	if config.AuditRetention == 0 {
		config.AuditRetention = time.Hour
	}
	if config.MaxAssetBytes == 0 {
		config.MaxAssetBytes = 1 << 20
	}
	if config.AssetDir == "" {
		config.AssetDir = t.TempDir()
	}
	server, err := NewServerWithStore(config, store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	return server
}

func dialAuthorizedWebSocket(t *testing.T, url, token string) *websocket.Conn {
	t.Helper()
	header := make(http.Header)
	header.Set("Authorization", "Bearer "+token)
	conn, response, err := websocket.DefaultDialer.Dial(url, header)
	if err != nil {
		if response != nil {
			defer response.Body.Close()
		}
		t.Fatal(err)
	}
	return conn
}
