package controlplane

import (
	"encoding/binary"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func testAudioFrame(deviceID string) []byte {
	frame := make([]byte, 52)
	copy(frame, "OPUS")
	copy(frame[4:36], deviceID)
	frame[36] = 2
	binary.BigEndian.PutUint32(frame[45:49], 3)
	copy(frame[49:], []byte{0xf8, 0xff, 0xfe})
	return frame
}

func TestAudioEnvelopeValidation(t *testing.T) {
	frame := testAudioFrame("phone")
	if !validAudioFrame("phone", frame) || validAudioFrame("other", frame) {
		t.Fatal("audio frame is not bound to its authenticated device")
	}
	for _, size := range []int{0, 4, 48, 49, 51} {
		if validAudioFrame("phone", frame[:size]) {
			t.Fatalf("accepted truncated frame of %d bytes", size)
		}
	}
	binary.BigEndian.PutUint32(frame[45:49], 9999)
	if validAudioFrame("phone", frame) {
		t.Fatal("accepted mismatched payload size")
	}
}

func TestAudioSubscriptionsAreExplicitAndQueuesStayBounded(t *testing.T) {
	hub := newRealtimeHub()
	listener := &browserPeer{id: "listener", audioFrames: make(chan []byte, 5)}
	preview := &browserPeer{id: "preview", audioFrames: make(chan []byte, 5)}
	other := &browserPeer{id: "other", audioFrames: make(chan []byte, 5)}
	for _, peer := range []*browserPeer{listener, preview, other} {
		hub.addBrowser(peer)
	}
	hub.subscribeAudio("listener", "phone")
	hub.subscribePreview("preview", "phone")
	hub.subscribeAudio("other", "other-phone")
	for i := byte(0); i < 50; i++ {
		hub.publishAudio("phone", []byte{i})
	}
	if len(listener.audioFrames) != 5 || len(preview.audioFrames) != 0 || len(other.audioFrames) != 0 {
		t.Fatal("audio was broadcast without subscription or device isolation")
	}
	if oldest := <-listener.audioFrames; oldest[0] != 45 {
		t.Fatal("slow listener retained stale audio")
	}
	hub.subscribeAudio("preview", "phone")
	if stopped := hub.removeAudioBrowser("listener"); len(stopped) != 0 {
		t.Fatal("one listener disconnected the other")
	}
	if stopped := hub.removeAudioBrowser("preview"); len(stopped) != 1 || stopped[0] != "phone" {
		t.Fatal("last listener did not stop capture")
	}
}

func TestWebSocketAudioAuthorizationRoutingAndRevocation(t *testing.T) {
	server := newTestServer(t)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	wsBase := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	dial := func(path, token string) *websocket.Conn {
		t.Helper()
		var conn *websocket.Conn
		var err error
		if token == "" {
			conn, _, err = websocket.DefaultDialer.Dial(wsBase+path, nil)
		} else {
			conn = dialAuthorizedWebSocket(t, wsBase+path, token)
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	readJSON := func(conn *websocket.Conn, kind string) map[string]any {
		t.Helper()
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		for {
			var message map[string]any
			if err := conn.ReadJSON(&message); err != nil {
				t.Fatal(err)
			}
			if message["message_type"] == kind {
				return message
			}
		}
	}
	agent := dial("/register_agent", "")
	enrollment := server.store.CreateEnrollment("phone", time.Minute)
	_ = agent.WriteJSON(map[string]any{"message_type": "agent_hello", "device_id": "phone", "enrollment_token": enrollment.Token})
	readJSON(agent, "agent_config")

	user, err := server.store.CreateUser("audio-listener", "password", RoleUser, []string{"phone"})
	if err != nil {
		t.Fatal(err)
	}
	token, err := issueUserToken(server.config.JWTSecret, user)
	if err != nil {
		t.Fatal(err)
	}
	browser := dial("/connect_client", token)
	_ = browser.WriteJSON(map[string]any{"message_type": "connect", "device_id": "phone"})
	readJSON(browser, "config")
	_ = browser.WriteJSON(map[string]any{"message_type": "start_audio", "device_id": "phone", "audio_source": "output"})
	request := readJSON(agent, "start_audio")
	if request["audio"] != true || request["client_id"] == "" {
		t.Fatal("audio request lost capture flag or authenticated routing")
	}
	_ = agent.WriteMessage(websocket.BinaryMessage, testAudioFrame("phone"))
	_ = browser.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		kind, data, err := browser.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if kind == websocket.BinaryMessage {
			if !validAudioFrame("phone", data) {
				t.Fatal("received an invalid audio packet")
			}
			break
		}
	}

	for _, tc := range []struct {
		name     string
		assigned []string
		locked   bool
	}{
		{"unassigned", nil, false},
		{"audio-locked", []string{"phone"}, true},
	} {
		denied, err := server.store.CreateUser(tc.name, "password", RoleUser, tc.assigned)
		if err != nil {
			t.Fatal(err)
		}
		if tc.locked {
			denied, err = server.store.UpdateUser(tc.name, func(user *User) error {
				user.ForbidAudio = true
				user.Settings = map[string]any{"audio": false}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		token, _ := issueUserToken(server.config.JWTSecret, denied)
		socket := dial("/connect_client", token)
		_ = socket.WriteJSON(map[string]any{"message_type": "start_audio", "device_id": "phone", "audio": true})
		readJSON(socket, "audio_error")
		_ = socket.Close()
	}

	if _, err := server.store.UpdateUser(user.Username, func(user *User) error {
		user.AssignedDevices = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_ = browser.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _, err = browser.ReadMessage()
	var closeError *websocket.CloseError
	if !errors.As(err, &closeError) {
		t.Fatalf("revoked listener was not closed: %v", err)
	}
	readJSON(agent, "stop_audio")
}
