package controlplane

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type socketPeer struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (p *socketPeer) writeJSON(value any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	_ = p.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return p.conn.WriteJSON(value)
}

func (p *socketPeer) writeBinary(value []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	_ = p.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return p.conn.WriteMessage(websocket.BinaryMessage, value)
}

func (p *socketPeer) close() error { return p.conn.Close() }

type browserPeer struct {
	id          string
	user        User
	socket      *socketPeer
	deviceID    string
	viewOnly    bool
	audioFrames chan []byte
	audioDone   <-chan struct{}
}

type realtimeHub struct {
	mu                 sync.RWMutex
	agents             map[string]*socketPeer
	browsers           map[string]*browserPeer
	previewSubscribers map[string]map[string]*browserPeer
	audioSubscribers   map[string]map[string]*browserPeer
	filter             func(*browserPeer, any) any
}

func newRealtimeHub() *realtimeHub {
	return &realtimeHub{
		agents:             make(map[string]*socketPeer),
		browsers:           make(map[string]*browserPeer),
		previewSubscribers: make(map[string]map[string]*browserPeer),
		audioSubscribers:   make(map[string]map[string]*browserPeer),
	}
}

func (h *realtimeHub) addBrowser(peer *browserPeer) {
	h.mu.Lock()
	h.browsers[peer.id] = peer
	h.mu.Unlock()
}

func (h *realtimeHub) removeBrowser(id string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.browsers, id)
	var stopped []string
	for deviceID, subscribers := range h.previewSubscribers {
		delete(subscribers, id)
		if len(subscribers) == 0 {
			delete(h.previewSubscribers, deviceID)
			stopped = append(stopped, deviceID)
		}
	}
	return stopped
}

func (h *realtimeHub) bindBrowser(id, deviceID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if peer := h.browsers[id]; peer != nil {
		peer.deviceID = deviceID
	}
}

func (h *realtimeHub) setAgent(deviceID string, peer *socketPeer) (previous *socketPeer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	previous = h.agents[deviceID]
	h.agents[deviceID] = peer
	return previous
}

func (h *realtimeHub) removeAgent(deviceID string, peer *socketPeer) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.agents[deviceID] == peer {
		delete(h.agents, deviceID)
		return true
	}
	return false
}

func (h *realtimeHub) sendAgent(deviceID string, message any) bool {
	h.mu.RLock()
	peer := h.agents[deviceID]
	h.mu.RUnlock()
	return peer != nil && peer.writeJSON(message) == nil
}

func (h *realtimeHub) sendBrowser(id string, message any) bool {
	h.mu.RLock()
	peer := h.browsers[id]
	h.mu.RUnlock()
	return peer != nil && peer.socket.writeJSON(message) == nil
}

func (h *realtimeHub) subscribePreview(browserID, deviceID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	peer := h.browsers[browserID]
	if peer == nil {
		return
	}
	if h.previewSubscribers[deviceID] == nil {
		h.previewSubscribers[deviceID] = make(map[string]*browserPeer)
	}
	h.previewSubscribers[deviceID][browserID] = peer
}

func (h *realtimeHub) unsubscribePreview(browserID, deviceID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	subscribers := h.previewSubscribers[deviceID]
	if subscribers == nil {
		return false
	}
	delete(subscribers, browserID)
	if len(subscribers) == 0 {
		delete(h.previewSubscribers, deviceID)
		return true
	}
	return false
}

func (h *realtimeHub) publishPreview(deviceID string, frame []byte) {
	h.mu.RLock()
	subscribers := make([]*browserPeer, 0, len(h.previewSubscribers[deviceID]))
	for _, peer := range h.previewSubscribers[deviceID] {
		subscribers = append(subscribers, peer)
	}
	h.mu.RUnlock()
	for _, peer := range subscribers {
		_ = peer.socket.writeBinary(frame)
	}
}

func (h *realtimeHub) broadcast(message any) {
	h.mu.RLock()
	peers := make([]*browserPeer, 0, len(h.browsers))
	for _, peer := range h.browsers {
		peers = append(peers, peer)
	}
	h.mu.RUnlock()
	for _, peer := range peers {
		filtered := message
		if h.filter != nil {
			filtered = h.filter(peer, message)
		}
		if filtered != nil {
			_ = peer.socket.writeJSON(filtered)
		}
	}
}

func (h *realtimeHub) disconnectBrowsers(username, deviceID string) {
	h.mu.RLock()
	peers := make([]*browserPeer, 0)
	for _, peer := range h.browsers {
		if peer.user.Username == username && (deviceID == "" || peer.deviceID == deviceID) {
			peers = append(peers, peer)
		}
	}
	h.mu.RUnlock()
	for _, peer := range peers {
		_ = peer.socket.close()
	}
}

func (h *realtimeHub) publishClipboard(deviceID string, message any) {
	h.mu.RLock()
	var peers []*browserPeer
	for _, peer := range h.browsers {
		if peer.deviceID == deviceID && !peer.viewOnly {
			peers = append(peers, peer)
		}
	}
	h.mu.RUnlock()
	for _, peer := range peers {
		_ = peer.socket.writeJSON(message)
	}
}
