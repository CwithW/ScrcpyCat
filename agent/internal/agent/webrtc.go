package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pion/rtcp"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
)

const (
	h264PayloadType = 96
	h264ClockRate   = 90000
)

type webRTCSession struct {
	clientID          string
	peer              *webrtc.PeerConnection
	clipboard         *webrtc.DataChannel
	withVideo         bool
	withAudio         bool
	bitrate           bitrateOptions
	captureOptions    streamOptions
	order             uint64
	targetBitrate     int
	pendingCandidates []webrtc.ICECandidateInit
}

type webRTCManager struct {
	mu                  sync.Mutex
	writer              *lockedWriter
	onInput             func(map[string]any) error
	terminals           *terminalManager
	iceServers          []webrtc.ICEServer
	track               *webrtc.TrackLocalStaticRTP
	audioTrack          *webrtc.TrackLocalStaticRTP
	audioPacketizer     rtp.Packetizer
	packetizer          rtp.Packetizer
	lastPTS             uint64
	lastKeyframeRequest time.Time
	lastBitrateUpdate   time.Time
	currentBitrate      int
	sessions            map[string]webRTCSession
	videoProfile        string
	nextOrder           uint64
	onSessionClosed     func()
}

func newWebRTCManager(writer *lockedWriter, onInput func(map[string]any) error, terminals *terminalManager) *webRTCManager {
	track, _ := webrtc.NewTrackLocalStaticRTP(
		h264Codec("42e01f"),
		"video",
		"scrcpycat",
	)
	audioTrack, _ := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "audio", "scrcpycat")
	return &webRTCManager{
		audioTrack:      audioTrack,
		audioPacketizer: rtp.NewPacketizer(1200, 111, 0x53434155, &codecs.OpusPayloader{}, rtp.NewRandomSequencer(), 48000),
		writer:          writer,
		onInput:         onInput,
		terminals:       terminals,
		track:           track,
		packetizer: rtp.NewPacketizer(
			1200,
			h264PayloadType,
			0x53434354,
			&codecs.H264Payloader{},
			rtp.NewRandomSequencer(),
			h264ClockRate,
		),
		sessions:     make(map[string]webRTCSession),
		videoProfile: "42e01f",
	}
}

func h264Codec(profile string) webrtc.RTPCodecCapability {
	return webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeH264, ClockRate: h264ClockRate,
		SDPFmtpLine:  "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=" + profile,
		RTCPFeedback: []webrtc.RTCPFeedback{{Type: "nack"}, {Type: "nack", Parameter: "pli"}, {Type: "goog-remb"}},
	}
}

func (m *webRTCManager) SetVideoCodec(profile string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if profile == m.videoProfile {
		return nil
	}
	for _, session := range m.sessions {
		if session.withVideo {
			// Level may change with resolution; SPS carries it in-band and the
			// negotiated H.264 capability allows asymmetric levels.
			if len(profile) == 6 && len(m.videoProfile) == 6 && profile[:4] == m.videoProfile[:4] {
				return nil
			}
			return fmt.Errorf("encoder profile changed; reconnect the existing viewers first")
		}
	}
	track, err := webrtc.NewTrackLocalStaticRTP(h264Codec(profile), "video", "scrcpycat")
	if err == nil {
		m.track, m.videoProfile = track, profile
	}
	return err
}

func (m *webRTCManager) SetICEServers(raw any) {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return
	}
	var iceServers []webrtc.ICEServer
	if json.Unmarshal(encoded, &iceServers) != nil {
		return
	}
	m.mu.Lock()
	m.iceServers = iceServers
	m.mu.Unlock()
}

func (m *webRTCManager) HandleForward(message map[string]any) error {
	clientID := stringField(message, "client_id")
	payload, _ := message["payload"].(map[string]any)
	if clientID == "" || payload == nil {
		return nil
	}
	switch stringField(payload, "type") {
	case "request-offer":
		return m.createOffer(clientID, message["permissions"], payload)
	case "answer":
		return m.setAnswer(clientID, stringField(payload, "sdp"))
	case "ice-candidate":
		return m.addCandidate(clientID, payload["candidate"])
	}
	return nil
}

func (m *webRTCManager) createOffer(clientID string, rawPermissions any, payload map[string]any) error {
	options, _ := payload["scrcpy_options"].(map[string]any)
	withVideo := options["video"] != false
	withAudio, _ := options["audio"].(bool)
	permissions, _ := rawPermissions.(map[string]any)
	canControl, _ := permissions["control"].(bool)
	canShell, _ := permissions["shell"].(bool)
	m.mu.Lock()
	if _, ok := m.sessions[clientID]; ok {
		m.mu.Unlock()
		return nil
	}
	configuration := webrtc.Configuration{ICEServers: append([]webrtc.ICEServer(nil), m.iceServers...)}
	profile := m.videoProfile
	m.mu.Unlock()

	bitrate, err := parseBitrateOptions(options)
	if err != nil {
		return err
	}
	if !withVideo {
		bitrate.Enabled = false
	}
	capture, err := parseStreamOptions(options, false)
	if err != nil {
		return err
	}
	peer, estimator, err := newMediaPeer(configuration, bitrate, stringField(payload, "ip_preference"), profile)
	if err != nil {
		return err
	}
	if withVideo {
		sender, err := peer.AddTrack(m.track)
		if err != nil {
			_ = peer.Close()
			return err
		}
		go m.readFeedback(sender, true)
	}
	if withAudio {
		sender, err := peer.AddTrack(m.audioTrack)
		if err != nil {
			_ = peer.Close()
			return err
		}
		go m.readFeedback(sender, false)
	}
	sessionActive := func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		current, ok := m.sessions[clientID]
		return ok && current.peer == peer
	}
	inputChannel, err := peer.CreateDataChannel("input-channel", nil)
	if err == nil {
		inputChannel.OnMessage(func(message webrtc.DataChannelMessage) {
			if !canControl || !sessionActive() || m.onInput == nil || !message.IsString {
				return
			}
			var event map[string]any
			if json.Unmarshal(message.Data, &event) == nil && browserControlAllowed(event) {
				_ = m.onInput(event)
			}
		})
	}
	clipboard, _ := peer.CreateDataChannel("clipboard-channel", nil)
	if clipboard != nil {
		clipboard.OnMessage(func(message webrtc.DataChannelMessage) {
			if !canControl || !sessionActive() || !message.IsString || m.onInput == nil {
				return
			}
			var event map[string]any
			if json.Unmarshal(message.Data, &event) != nil {
				return
			}
			switch stringField(event, "type") {
			case "set_clipboard", "get_clipboard":
				_ = m.onInput(event)
			}
		})
	}

	peer.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}
		_ = m.writer.writeJSON(map[string]any{
			"message_type": "device_msg",
			"client_id":    clientID,
			"payload": map[string]any{
				"type":      "ice-candidate",
				"candidate": candidate.ToJSON(),
			},
		})
	})
	peer.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if withVideo && state == webrtc.PeerConnectionStateConnected {
			m.requestKeyframe()
			m.updateBitrate(clientID, peer, bitrate.Initial)
		}
		if state == webrtc.PeerConnectionStateClosed || state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateDisconnected {
			m.removeSession(clientID, peer)
		}
	})
	peer.OnDataChannel(func(channel *webrtc.DataChannel) {
		if !canShell || !sessionActive() || m.terminals == nil || !strings.HasPrefix(channel.Label(), "adb-channel-") {
			_ = channel.Close()
			return
		}
		sessionID := strings.TrimPrefix(channel.Label(), "adb-channel-")
		opened := false
		channel.OnMessage(func(message webrtc.DataChannelMessage) {
			if !sessionActive() {
				return
			}
			if !opened {
				rows, cols := terminalInit(message.Data)
				err := m.terminals.OpenWebRTC(clientID, sessionID, rows, cols, func(data []byte) { _ = channel.Send(data) }, func() { _ = channel.Close() })
				if err != nil {
					encoded, _ := json.Marshal(map[string]string{"type": "pty_error", "error": err.Error()})
					_ = channel.SendText(string(encoded))
					return
				}
				opened = true
				_ = channel.SendText(`{"type":"pty_opened"}`)
				return
			}
			if message.IsString {
				var control struct {
					Type string `json:"type"`
					Rows uint16 `json:"rows"`
					Cols uint16 `json:"cols"`
				}
				if json.Unmarshal(message.Data, &control) == nil && control.Type == "resize" {
					m.terminals.Resize(clientID, sessionID, control.Rows, control.Cols)
				}
				return
			}
			m.terminals.InputBytes(clientID, sessionID, message.Data)
		})
		channel.OnClose(func() { m.terminals.Close(clientID, sessionID) })
	})

	offer, err := peer.CreateOffer(nil)
	if err != nil {
		_ = peer.Close()
		return err
	}
	if err := peer.SetLocalDescription(offer); err != nil {
		_ = peer.Close()
		return err
	}
	m.mu.Lock()
	if !canControl {
		clipboard = nil
	}
	m.nextOrder++
	m.sessions[clientID] = webRTCSession{clientID: clientID, peer: peer, clipboard: clipboard, withVideo: withVideo, withAudio: withAudio, bitrate: bitrate, targetBitrate: bitrate.Initial, captureOptions: capture, order: m.nextOrder}
	m.mu.Unlock()
	if estimator != nil {
		estimator.OnTargetBitrateChange(func(target int) { m.updateBitrate(clientID, peer, target) })
	}
	return m.writer.writeJSON(map[string]any{
		"message_type": "device_msg",
		"client_id":    clientID,
		"payload": map[string]any{
			"type":           "offer",
			"sdp":            offer.SDP,
			"camera_support": false,
		},
	})
}

func (m *webRTCManager) setAnswer(clientID, sdp string) error {
	if sdp == "" {
		return nil
	}
	m.mu.Lock()
	session, ok := m.sessions[clientID]
	m.mu.Unlock()
	if !ok {
		return nil
	}
	if err := session.peer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp}); err != nil {
		return err
	}
	m.mu.Lock()
	session = m.sessions[clientID]
	candidates := session.pendingCandidates
	session.pendingCandidates = nil
	m.sessions[clientID] = session
	m.mu.Unlock()
	for _, candidate := range candidates {
		if err := session.peer.AddICECandidate(candidate); err != nil {
			return err
		}
	}
	return nil
}

func (m *webRTCManager) addCandidate(clientID string, raw any) error {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	var candidate webrtc.ICECandidateInit
	if err := json.Unmarshal(encoded, &candidate); err != nil {
		return err
	}
	m.mu.Lock()
	session, ok := m.sessions[clientID]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	if session.peer.RemoteDescription() == nil {
		session.pendingCandidates = append(session.pendingCandidates, candidate)
		m.sessions[clientID] = session
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()
	return session.peer.AddICECandidate(candidate)
}

func (m *webRTCManager) PushH264(packet mediaPacket) {
	m.mu.Lock()
	if len(m.sessions) == 0 || m.track == nil {
		m.mu.Unlock()
		return
	}
	duration := uint32(3000)
	if m.lastPTS > 0 && packet.PTS > m.lastPTS {
		delta := (packet.PTS - m.lastPTS) * 90 / 1000
		if delta > 0 && delta < 90000 {
			duration = uint32(delta)
		}
	}
	m.lastPTS = packet.PTS
	packets := m.packetizer.Packetize(packet.Data, duration)
	track := m.track
	m.mu.Unlock()
	for _, rtpPacket := range packets {
		_ = track.WriteRTP(rtpPacket)
	}
}

func (m *webRTCManager) removeSession(clientID string, peer *webrtc.PeerConnection) {
	m.mu.Lock()
	session, ok := m.sessions[clientID]
	if ok && session.peer == peer {
		delete(m.sessions, clientID)
	}
	m.mu.Unlock()
	if ok && session.peer == peer {
		if m.terminals != nil {
			m.terminals.CloseClient(clientID)
		}
		go peer.Close()
		if m.onSessionClosed != nil {
			m.onSessionClosed()
		}
	}
}

func (m *webRTCManager) LatestVideoOptions() (streamOptions, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest webRTCSession
	for _, session := range m.sessions {
		if session.withVideo && session.order > latest.order {
			latest = session
		}
	}
	options := latest.captureOptions
	options.Values = cloneStringMap(options.Values)
	return options, latest.withVideo
}

// Viewers share one encoder. A new viewer must not turn off the audio or
// activity refresh still requested by an existing video session.
func (m *webRTCManager) mergeCaptureNeeds(options streamOptions) streamOptions {
	m.mu.Lock()
	defer m.mu.Unlock()
	var audio webRTCSession
	for _, session := range m.sessions {
		if !session.withVideo {
			continue
		}
		if session.captureOptions.Values["keep_active"] == "true" {
			options.Values["keep_active"] = "true"
		}
		if session.withAudio && session.order > audio.order {
			audio = session
		}
	}
	if !options.Audio && audio.withAudio {
		options.Audio = true
		for _, key := range []string{"audio_source", "audio_dup"} {
			delete(options.Values, key)
			if value := audio.captureOptions.Values[key]; value != "" {
				options.Values[key] = value
			}
		}
	}
	return options
}

func cloneStringMap(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func (m *webRTCManager) requestKeyframe() {
	m.mu.Lock()
	if time.Since(m.lastKeyframeRequest) < 2*time.Second {
		m.mu.Unlock()
		return
	}
	m.lastKeyframeRequest = time.Now()
	m.mu.Unlock()
	if m.onInput != nil {
		_ = m.onInput(map[string]any{"type": "request_keyframe"})
	}
}

func (m *webRTCManager) HasSessions() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions) > 0
}

func (m *webRTCManager) CloseSession(clientID string) {
	m.mu.Lock()
	session, ok := m.sessions[clientID]
	delete(m.sessions, clientID)
	m.mu.Unlock()
	if ok {
		// An incomplete SCTP handshake can delay Close by 30 seconds. Revoke
		// input above immediately, but never block the signaling reader on it.
		go session.peer.Close()
	}
}

func (m *webRTCManager) Close() {
	m.mu.Lock()
	sessions := make([]webRTCSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.sessions = make(map[string]webRTCSession)
	m.mu.Unlock()
	for _, session := range sessions {
		go session.peer.Close()
	}
}

func (m *webRTCManager) PublishDeviceMessage(message map[string]any) {
	encoded, err := json.Marshal(message)
	if err != nil {
		return
	}
	m.mu.Lock()
	channels := make([]*webrtc.DataChannel, 0, len(m.sessions))
	for _, session := range m.sessions {
		if session.clipboard != nil {
			channels = append(channels, session.clipboard)
		}
	}
	m.mu.Unlock()
	for _, channel := range channels {
		if channel.ReadyState() == webrtc.DataChannelStateOpen {
			_ = channel.SendText(string(encoded))
		}
	}
}

func (m *webRTCManager) readFeedback(sender *webrtc.RTPSender, video bool) {
	for {
		packets, _, err := sender.ReadRTCP()
		if err != nil {
			return
		}
		if !video {
			continue
		}
		for _, packet := range packets {
			switch packet.(type) {
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
				m.requestKeyframe()
			}
		}
	}
}

func (m *webRTCManager) PushOpus(packet mediaPacket) {
	samples := opusPacketSamples(packet.Data)
	if samples == 0 {
		return
	}
	m.mu.Lock()
	packets := m.audioPacketizer.Packetize(packet.Data, samples)
	track := m.audioTrack
	m.mu.Unlock()
	for _, packet := range packets {
		_ = track.WriteRTP(packet)
	}
}

func (m *webRTCManager) HasClient(clientID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.sessions[clientID]
	return ok
}

func (m *webRTCManager) PublishError(err error) {
	m.mu.Lock()
	clients := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		clients = append(clients, id)
	}
	m.mu.Unlock()
	for _, id := range clients {
		_ = m.writer.writeJSON(map[string]any{"message_type": "device_msg", "client_id": id, "payload": map[string]any{"type": "scrcpy_error", "message": err.Error()}})
	}
}

func (m *webRTCManager) HasAudioSessions() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, session := range m.sessions {
		if session.withAudio {
			return true
		}
	}
	return false
}
