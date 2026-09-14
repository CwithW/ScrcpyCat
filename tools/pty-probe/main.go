package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	baseURL := flag.String("base-url", "http://127.0.0.1:8443", "control plane HTTP URL")
	username := flag.String("username", "", "control plane username")
	password := flag.String("password", "", "control plane password")
	deviceID := flag.String("device-id", "", "target device id")
	timeout := flag.Duration("timeout", 20*time.Second, "maximum wait for PTY output")
	flag.Parse()
	if *username == "" || *password == "" || *deviceID == "" {
		fatal("--username, --password and --device-id are required")
	}

	token, err := login(strings.TrimRight(*baseURL, "/"), *username, *password)
	if err != nil {
		fatal("login: %v", err)
	}
	endpoint, err := websocketURL(strings.TrimRight(*baseURL, "/"), token)
	if err != nil {
		fatal("websocket URL: %v", err)
	}
	connection, _, err := websocket.DefaultDialer.Dial(endpoint, nil)
	if err != nil {
		fatal("connect signaling: %v", err)
	}
	defer connection.Close()

	sessionID := "pty-probe"
	defer func() {
		_ = connection.WriteJSON(map[string]any{"message_type": "pty_close", "device_id": *deviceID, "session_id": sessionID})
	}()
	if err := connection.WriteJSON(map[string]any{"message_type": "connect", "device_id": *deviceID}); err != nil {
		fatal("send connect: %v", err)
	}
	if err := connection.WriteJSON(map[string]any{"message_type": "pty_open", "device_id": *deviceID, "session_id": sessionID, "rows": 30, "cols": 100}); err != nil {
		fatal("open PTY: %v", err)
	}
	if err := connection.WriteJSON(map[string]any{"message_type": "pty_resize", "device_id": *deviceID, "session_id": sessionID, "rows": 24, "cols": 80}); err != nil {
		fatal("resize PTY: %v", err)
	}
	command := "printf 'scrcpycat-%s\\n' pty-ok; stty size\n"
	if err := connection.WriteJSON(map[string]any{
		"message_type": "pty_input",
		"device_id":    *deviceID,
		"session_id":   sessionID,
		"data":         base64.StdEncoding.EncodeToString([]byte(command)),
	}); err != nil {
		fatal("write PTY: %v", err)
	}

	_ = connection.SetReadDeadline(time.Now().Add(*timeout))
	var received bytes.Buffer
	for {
		messageType, payload, err := connection.ReadMessage()
		if err != nil {
			fatal("wait PTY output: %v", err)
		}
		if messageType != websocket.TextMessage {
			continue
		}
		var message struct {
			MessageType string `json:"message_type"`
			SessionID   string `json:"session_id"`
			Data        string `json:"data"`
			Error       string `json:"error"`
		}
		if err := json.Unmarshal(payload, &message); err != nil {
			continue
		}
		if message.Error != "" {
			fatal("control plane: %s", message.Error)
		}
		if message.MessageType != "pty_data" || message.SessionID != sessionID {
			continue
		}
		output, err := base64.RawStdEncoding.DecodeString(message.Data)
		if err != nil {
			fatal("decode PTY output: %v", err)
		}
		received.Write(output)
		if bytes.Contains(received.Bytes(), []byte("scrcpycat-pty-ok")) && bytes.Contains(received.Bytes(), []byte("24 80")) {
			fmt.Printf("PTY_WSS_OK device=%s rows=24 cols=80\n", *deviceID)
			return
		}
	}
}

func login(baseURL, username, password string) (string, error) {
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		return "", err
	}
	request, err := http.NewRequest(http.MethodPost, baseURL+"/api/login", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		contents, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return "", fmt.Errorf("%s: %s", response.Status, strings.TrimSpace(string(contents)))
	}
	var result struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil || result.Token == "" {
		return "", fmt.Errorf("invalid login response: %w", err)
	}
	return result.Token, nil
}

func websocketURL(baseURL, token string) (string, error) {
	endpoint, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	switch endpoint.Scheme {
	case "http":
		endpoint.Scheme = "ws"
	case "https":
		endpoint.Scheme = "wss"
	default:
		return "", fmt.Errorf("unsupported scheme %q", endpoint.Scheme)
	}
	endpoint.Path = "/connect_client"
	endpoint.RawQuery = url.Values{"token": []string{token}}.Encode()
	return endpoint.String(), nil
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
