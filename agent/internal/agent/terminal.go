package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
)

type terminalSession struct {
	terminal terminalHandle
	onClose  func()
}

type terminalManager struct {
	opener   terminalOpener
	closed   bool
	writer   *lockedWriter
	mu       sync.Mutex
	sessions map[string]*terminalSession
}

func newTerminalManager(writer *lockedWriter, openers ...terminalOpener) *terminalManager {
	var opener terminalOpener = localDevice{}
	if len(openers) > 0 {
		opener = openers[0]
	}
	return &terminalManager{writer: writer, opener: opener, sessions: make(map[string]*terminalSession)}
}

func (m *terminalManager) event(clientID, sessionID, kind, message string) {
	if m.writer != nil {
		_ = m.writer.writeJSON(map[string]any{"message_type": kind, "client_id": clientID, "session_id": sessionID, "error": message})
	}
}

func (m *terminalManager) Open(clientID, sessionID string, rows, cols uint16) error {
	err := m.open(clientID, sessionID, rows, cols, func(data []byte) {
		_ = m.writer.writeJSON(map[string]any{"message_type": "pty_data", "client_id": clientID, "session_id": sessionID, "data": terminalData(data)})
	}, func() { m.event(clientID, sessionID, "pty_closed", "") })
	if err != nil {
		m.event(clientID, sessionID, "pty_error", err.Error())
		return err
	}
	m.event(clientID, sessionID, "pty_opened", "")
	return nil
}

func (m *terminalManager) OpenWebRTC(clientID, sessionID string, rows, cols uint16, send func([]byte), onClose func()) error {
	return m.open(clientID, sessionID, rows, cols, send, onClose)
}

func (m *terminalManager) open(clientID, sessionID string, rows, cols uint16, send func([]byte), onClose func()) error {
	if sessionID == "" {
		return nil
	}
	m.Close(clientID, sessionID)
	if rows == 0 {
		rows = 24
	}
	if cols == 0 {
		cols = 80
	}
	terminal, err := m.opener.OpenTerminal(context.Background(), rows, cols)
	if err != nil {
		return err
	}
	session := &terminalSession{terminal: terminal, onClose: onClose}
	key := clientID + ":" + sessionID
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		_ = terminal.Close()
		return errors.New("terminal manager is closed")
	}
	m.sessions[key] = session
	m.mu.Unlock()
	go m.read(key, session, send)
	return nil
}

func (m *terminalManager) read(key string, session *terminalSession, send func([]byte)) {
	buffer := make([]byte, 32*1024)
	for {
		n, err := session.terminal.Read(buffer)
		if n > 0 {
			send(append([]byte(nil), buffer[:n]...))
		}
		if err != nil {
			m.closeSession(key, session)
			return
		}
	}
}

func (m *terminalManager) Input(clientID, sessionID, raw string) {
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(raw)
	}
	if err != nil {
		return
	}
	m.InputBytes(clientID, sessionID, data)
}

func (m *terminalManager) InputBytes(clientID, sessionID string, data []byte) {
	m.mu.Lock()
	session := m.sessions[clientID+":"+sessionID]
	m.mu.Unlock()
	if session != nil {
		_, _ = session.terminal.Write(data)
	}
}

func (m *terminalManager) Resize(clientID, sessionID string, rows, cols uint16) {
	if rows == 0 || cols == 0 {
		return
	}
	m.mu.Lock()
	session := m.sessions[clientID+":"+sessionID]
	m.mu.Unlock()
	if session != nil {
		_ = session.terminal.Resize(rows, cols)
	}
}

func (m *terminalManager) Close(clientID, sessionID string) {
	m.closeSession(clientID+":"+sessionID, nil)
}

func (m *terminalManager) closeSession(key string, expected *terminalSession) {
	m.mu.Lock()
	session := m.sessions[key]
	if session == nil || (expected != nil && session != expected) {
		m.mu.Unlock()
		return
	}
	delete(m.sessions, key)
	m.mu.Unlock()
	_ = session.terminal.Close()
	if session.onClose != nil {
		session.onClose()
	}
}

func (m *terminalManager) CloseClient(clientID string) {
	m.mu.Lock()
	keys := make([]string, 0)
	for key := range m.sessions {
		if strings.HasPrefix(key, clientID+":") {
			keys = append(keys, key)
		}
	}
	m.mu.Unlock()
	for _, key := range keys {
		m.closeSession(key, nil)
	}
}

func (m *terminalManager) CloseAll() {
	m.mu.Lock()
	m.closed = true
	keys := make([]string, 0, len(m.sessions))
	for key := range m.sessions {
		keys = append(keys, key)
	}
	m.mu.Unlock()
	for _, key := range keys {
		m.closeSession(key, nil)
	}
}

func terminalInit(raw []byte) (uint16, uint16) {
	var value struct {
		Type string `json:"type"`
		Rows uint16 `json:"rows"`
		Cols uint16 `json:"cols"`
	}
	if json.Unmarshal(raw, &value) == nil && value.Type == "init" {
		return value.Rows, value.Cols
	}
	return 24, 80
}
