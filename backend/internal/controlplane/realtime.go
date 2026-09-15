package controlplane

import (
	"sort"
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
	accessToken string
	audioFrames chan []byte
	audioDone   <-chan struct{}
}

type realtimeHub struct {
	mu                 sync.RWMutex
	agents             map[string]*socketPeer
	browsers           map[string]*browserPeer
	previewSubscribers map[string]map[string]*browserPeer
	previewRequests    map[string]map[string]previewRequest
	previewOrder       uint64
	audioSubscribers   map[string]map[string]*browserPeer
	filter             func(*browserPeer, any) any
}

func newRealtimeHub() *realtimeHub {
	return &realtimeHub{
		agents:             make(map[string]*socketPeer),
		browsers:           make(map[string]*browserPeer),
		previewSubscribers: make(map[string]map[string]*browserPeer),
		previewRequests:    make(map[string]map[string]previewRequest),
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
		if _, subscribed := subscribers[id]; !subscribed {
			continue
		}
		delete(subscribers, id)
		delete(h.previewRequests[deviceID], id)
		stopped = append(stopped, deviceID)
		if len(subscribers) == 0 {
			delete(h.previewSubscribers, deviceID)
			delete(h.previewRequests, deviceID)
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
	delete(h.previewRequests[deviceID], browserID)
	if len(subscribers) == 0 {
		delete(h.previewSubscribers, deviceID)
		delete(h.previewRequests, deviceID)
		return true
	}
	return false
}

type previewRequest struct {
	order   uint64
	message map[string]any
}

func (h *realtimeHub) setPreviewRequest(browserID, deviceID string, message map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.previewRequests[deviceID] == nil {
		h.previewRequests[deviceID] = map[string]previewRequest{}
	}
	h.previewOrder++
	h.previewRequests[deviceID][browserID] = previewRequest{order: h.previewOrder, message: withClient(message, browserID)}
}

func (h *realtimeHub) selectedPreviewRequest(deviceID string) map[string]any {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var selected previewRequest
	awake := false
	for _, request := range h.previewRequests[deviceID] {
		awake = awake || request.message["stay_awake"] == true
		foreground := request.message["preview"] == false
		selectedForeground := selected.message["preview"] == false
		if selected.message == nil || (foreground && !selectedForeground) || (foreground == selectedForeground && request.order > selected.order) {
			selected = request
		}
	}
	if selected.message == nil {
		return nil
	}
	result := cloneMap(selected.message)
	result["stay_awake"] = awake
	result["device_id"] = deviceID
	return result
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

func (h *realtimeHub) disconnectToken(digest string) {
	h.mu.RLock()
	var peers []*browserPeer
	for _, peer := range h.browsers {
		if peer.accessToken == digest {
			peers = append(peers, peer)
		}
	}
	h.mu.RUnlock()
	for _, peer := range peers {
		_ = peer.socket.close()
	}
}

type userPresence struct {
	User
	Online        bool     `json:"online"`
	ActiveDevices []string `json:"active_devices"`
}

func (h *realtimeHub) usersWithPresence(users []User) []userPresence {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]userPresence, 0, len(users))
	for _, user := range users {
		entry := userPresence{User: user, ActiveDevices: []string{}}
		devices := map[string]bool{}
		for _, peer := range h.browsers {
			if peer.user.ID != user.ID {
				continue
			}
			entry.Online = true
			if peer.deviceID != "" && !peer.viewOnly {
				devices[peer.deviceID] = true
			}
		}
		for id := range devices {
			entry.ActiveDevices = append(entry.ActiveDevices, id)
		}
		sort.Strings(entry.ActiveDevices)
		result = append(result, entry)
	}
	return result
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
