package controlplane

import (
	"encoding/binary"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

func validAudioFrame(deviceID string, frame []byte) bool {
	return len(frame) > 49 && len(frame) <= 49+65536 &&
		string(frame[:4]) == "OPUS" &&
		strings.TrimRight(string(frame[4:36]), "\x00") == deviceID &&
		(frame[36] == 1 || frame[36] == 2) &&
		int(binary.BigEndian.Uint32(frame[45:49])) == len(frame)-49
}

func (s *Server) startBrowserAudio(peer *browserPeer, deviceID string, message map[string]any) {
	fail := func(reason string) {
		_ = peer.socket.writeJSON(map[string]any{"message_type": "audio_error", "error": reason})
	}
	if deviceID == "" || !s.store.CanAccessDevice(peer.user, deviceID) {
		fail("无权访问设备音频")
		return
	}
	message["audio"] = true
	options := s.constrainedStreamOptions(peer.user, message)
	if options["audio"] != true {
		fail("管理员已关闭音频并锁定设置")
		return
	}
	s.hub.subscribeAudio(peer.id, deviceID)
	if !s.hub.sendAgent(deviceID, withClient(options, peer.id)) {
		s.hub.unsubscribeAudio(peer.id, deviceID)
		fail("设备离线，无法开启音频")
	}
}

func (h *realtimeHub) subscribeAudio(browserID, deviceID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	peer := h.browsers[browserID]
	if peer == nil {
		return
	}
	if h.audioSubscribers[deviceID] == nil {
		h.audioSubscribers[deviceID] = make(map[string]*browserPeer)
	}
	h.audioSubscribers[deviceID][browserID] = peer
}

func (h *realtimeHub) unsubscribeAudio(browserID, deviceID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	subscribers := h.audioSubscribers[deviceID]
	if _, subscribed := subscribers[browserID]; !subscribed {
		return false
	}
	delete(subscribers, browserID)
	if len(subscribers) == 0 {
		delete(h.audioSubscribers, deviceID)
		return true
	}
	return false
}

func (h *realtimeHub) removeAudioBrowser(browserID string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var stopped []string
	for deviceID, subscribers := range h.audioSubscribers {
		delete(subscribers, browserID)
		if len(subscribers) == 0 {
			delete(h.audioSubscribers, deviceID)
			stopped = append(stopped, deviceID)
		}
	}
	return stopped
}

func (h *realtimeHub) publishAudio(deviceID string, frame []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, peer := range h.audioSubscribers[deviceID] {
		if peer.audioFrames == nil {
			continue
		}
		select {
		case peer.audioFrames <- frame:
		default:
			// A slow listener must not block the Agent or other viewers.
			// Discard the oldest packet instead of accumulating audio delay.
			select {
			case <-peer.audioFrames:
			default:
			}
			select {
			case peer.audioFrames <- frame:
			default:
			}
		}
	}
}

func (p *browserPeer) writeAudio() {
	for {
		select {
		case <-p.audioDone:
			return
		case frame := <-p.audioFrames:
			p.socket.mu.Lock()
			_ = p.socket.conn.SetWriteDeadline(time.Now().Add(250 * time.Millisecond))
			err := p.socket.conn.WriteMessage(websocket.BinaryMessage, frame)
			p.socket.mu.Unlock()
			if err != nil {
				_ = p.socket.close()
				return
			}
		}
	}
}
