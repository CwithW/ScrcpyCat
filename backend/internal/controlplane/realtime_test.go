package controlplane

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestAgentAndBrowserForwarding(t *testing.T) {
	server := newTestServer(t)
	testServer := httptest.NewUnstartedServer(server.Handler())
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	testServer.Listener = listener
	testServer.Start()
	defer testServer.Close()
	wsBase := "ws" + strings.TrimPrefix(testServer.URL, "http")

	enrollment := server.store.CreateEnrollment("device-1", time.Minute)
	agent, _, err := websocket.DefaultDialer.Dial(wsBase+"/register_agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	if err := agent.WriteJSON(map[string]any{
		"message_type":     "agent_hello",
		"device_id":        "device-1",
		"enrollment_token": enrollment.Token,
		"device_info":      map[string]any{"model": "test-device"},
	}); err != nil {
		t.Fatal(err)
	}
	var agentConfig map[string]any
	if err := agent.ReadJSON(&agentConfig); err != nil {
		t.Fatal(err)
	}
	if agentConfig["message_type"] != "agent_config" {
		t.Fatalf("unexpected agent config: %#v", agentConfig)
	}

	loginResponse, err := testServer.Client().Post(testServer.URL+"/api/login", "application/json", bytes.NewBufferString(`{"username":"admin","password":"password"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer loginResponse.Body.Close()
	var login map[string]any
	if err := json.NewDecoder(loginResponse.Body).Decode(&login); err != nil {
		t.Fatal(err)
	}
	token, _ := login["token"].(string)

	browser := dialAuthorizedWebSocket(t, wsBase+"/connect_client", token)
	defer browser.Close()
	if err := browser.WriteJSON(map[string]any{"message_type": "connect", "device_id": "device-1"}); err != nil {
		t.Fatal(err)
	}
	var browserConfig map[string]any
	if err := browser.ReadJSON(&browserConfig); err != nil {
		t.Fatal(err)
	}
	if browserConfig["message_type"] != "config" {
		t.Fatalf("unexpected browser config: %#v", browserConfig)
	}

	if err := browser.WriteJSON(map[string]any{
		"message_type": "forward",
		"device_id":    "device-1",
		"payload":      map[string]any{"type": "request-offer"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := agent.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var forwarded map[string]any
	for range 2 {
		if err := agent.ReadJSON(&forwarded); err != nil {
			t.Fatal(err)
		}
		if forwarded["message_type"] == "forward" {
			break
		}
	}
	if forwarded["message_type"] != "forward" || forwarded["client_id"] == "" {
		t.Fatalf("unexpected forwarded message: %#v", forwarded)
	}
}

func TestFileGatewayAndTrustedSessionPermissions(t *testing.T) {
	server := newTestServer(t)
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()
	base := "ws" + strings.TrimPrefix(testServer.URL, "http")
	enrollment := server.store.CreateEnrollment("device-1", time.Minute)
	agent, _, err := websocket.DefaultDialer.Dial(base+"/register_agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	agent.WriteJSON(map[string]any{"message_type": "agent_hello", "device_id": "device-1", "enrollment_token": enrollment.Token})
	var config map[string]any
	if err := agent.ReadJSON(&config); err != nil {
		t.Fatal(err)
	}
	user, err := server.store.CreateUser("restricted", "password", RoleUser, []string{"device-1"})
	if err != nil {
		t.Fatal(err)
	}
	token, _ := issueUserToken(server.config.JWTSecret, user)
	browser := dialAuthorizedWebSocket(t, base+"/connect_client", token)
	defer browser.Close()
	browser.WriteJSON(map[string]any{"message_type": "connect", "device_id": "device-1"})
	if err := browser.ReadJSON(&config); err != nil {
		t.Fatal(err)
	}
	browser.WriteJSON(map[string]any{"message_type": "file_open", "device_id": "device-1"})
	if err := browser.ReadJSON(&config); err != nil {
		t.Fatal(err)
	}
	if config["message_type"] != "file_error" {
		t.Fatalf("missing file permission accepted: %v", config)
	}
	browser.WriteJSON(map[string]any{"message_type": "forward", "permissions": map[string]bool{"shell": true}, "payload": map[string]any{"type": "request-offer"}})
	agent.SetReadDeadline(time.Now().Add(time.Second))
	for {
		if err := agent.ReadJSON(&config); err != nil {
			t.Fatal(err)
		}
		if config["message_type"] == "forward" {
			break
		}
	}
	permissions := config["permissions"].(map[string]any)
	if permissions["shell"] != false || permissions["control"] != true {
		t.Fatalf("untrusted permissions forwarded: %v", permissions)
	}

	admin, _ := server.store.Authenticate("admin", "password")
	token, _ = issueUserToken(server.config.JWTSecret, admin)
	authorized := dialAuthorizedWebSocket(t, base+"/connect_client", token)
	defer authorized.Close()
	authorized.WriteJSON(map[string]any{"message_type": "connect", "device_id": "device-1"})
	authorized.ReadJSON(&config)
	authorized.WriteJSON(map[string]any{"message_type": "file_command", "payload": map[string]any{"type": "list", "path": "/sdcard"}})
	for {
		if err := agent.ReadJSON(&config); err != nil {
			t.Fatal(err)
		}
		if config["message_type"] == "file_command" {
			break
		}
	}
	if config["client_id"] == "" {
		t.Fatal("authorized file request lost routing")
	}
}

func TestRemovedPreviewSubscriberReconcilesRemainingSubscribers(t *testing.T) {
	hub := newRealtimeHub()
	hub.addBrowser(&browserPeer{id: "first"})
	hub.addBrowser(&browserPeer{id: "second"})
	hub.subscribePreview("first", "device")
	hub.subscribePreview("second", "device")
	hub.setPreviewRequest("first", "device", map[string]any{"preview": false, "stay_awake": true})
	hub.setPreviewRequest("second", "device", map[string]any{"preview": true, "stay_awake": false})
	if stopped := hub.removeBrowser("first"); len(stopped) != 1 || stopped[0] != "device" {
		t.Fatal(stopped)
	}
	if remaining := hub.selectedPreviewRequest("device"); remaining["preview"] != true || remaining["stay_awake"] != false {
		t.Fatal("foreground settings leaked into remaining preview", remaining)
	}
	if stopped := hub.removeBrowser("second"); len(stopped) != 1 || stopped[0] != "device" {
		t.Fatal(stopped)
	}
}
