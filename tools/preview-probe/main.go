package main

import (
	"bytes"
	"encoding/binary"
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

const previewHeaderSize = 49

func main() {
	baseURL := flag.String("base-url", "http://127.0.0.1:8443", "control plane HTTP URL")
	username := flag.String("username", "admin", "control plane username")
	password := flag.String("password", "", "control plane password")
	deviceID := flag.String("device-id", "", "target device id")
	timeout := flag.Duration("timeout", 20*time.Second, "maximum wait for an H.264 preview frame")
	flag.Parse()
	if *password == "" || *deviceID == "" {
		fatal("--password and --device-id are required")
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

	if err := connection.WriteJSON(map[string]any{"message_type": "connect", "device_id": *deviceID}); err != nil {
		fatal("send connect: %v", err)
	}
	deadline := time.Now().Add(*timeout)
	_ = connection.SetReadDeadline(deadline)
	for {
		messageType, payload, err := connection.ReadMessage()
		if err != nil {
			fatal("wait preview: %v", err)
		}
		if messageType == websocket.BinaryMessage {
			pts, keyframe, size, err := validatePreview(payload, *deviceID)
			if err != nil {
				fatal("validate preview: %v", err)
			}
			fmt.Printf("PREVIEW_OK device=%s keyframe=%t pts_us=%d h264_bytes=%d\n", *deviceID, keyframe, pts, size)
			return
		}
		if messageType != websocket.TextMessage {
			continue
		}
		var message struct {
			MessageType string `json:"message_type"`
			Error       string `json:"error"`
		}
		if json.Unmarshal(payload, &message) != nil {
			continue
		}
		if message.Error != "" {
			fatal("control plane: %s", message.Error)
		}
		if message.MessageType == "config" {
			if err := connection.WriteJSON(map[string]any{"message_type": "start_preview", "device_id": *deviceID}); err != nil {
				fatal("start preview: %v", err)
			}
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

func validatePreview(frame []byte, deviceID string) (uint64, bool, uint32, error) {
	if len(frame) < previewHeaderSize || !bytes.Equal(frame[:4], []byte("PREV")) {
		return 0, false, 0, fmt.Errorf("invalid PREV header")
	}
	embeddedID := strings.TrimRight(string(frame[4:36]), "\x00")
	if embeddedID != deviceID {
		return 0, false, 0, fmt.Errorf("device id %q does not match %q", embeddedID, deviceID)
	}
	size := binary.BigEndian.Uint32(frame[45:49])
	if size == 0 || len(frame) != previewHeaderSize+int(size) {
		return 0, false, 0, fmt.Errorf("invalid H.264 payload size %d", size)
	}
	return binary.BigEndian.Uint64(frame[37:45]), frame[36] == 1, size, nil
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
