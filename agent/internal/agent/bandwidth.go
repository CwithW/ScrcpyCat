package agent

import (
	"fmt"
	"log"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/cc"
	"github.com/pion/interceptor/pkg/gcc"
	"github.com/pion/webrtc/v4"
)

type bitrateOptions struct {
	Enabled                   bool
	Initial, Minimum, Maximum int
}

func parseBitrateOptions(raw map[string]any) (bitrateOptions, error) {
	options := bitrateOptions{Initial: 4000000, Minimum: 100000, Maximum: 20000000}
	options.Enabled, _ = raw["bwe"].(bool)
	for key, target := range map[string]*int{"bitrate": &options.Initial, "min_bitrate": &options.Minimum, "max_bitrate": &options.Maximum} {
		if _, ok := raw[key]; !ok {
			continue
		}
		value, err := uint32Field(raw, key)
		if err != nil || value < 100000 || value > 100000000 {
			return options, fmt.Errorf("invalid %s", key)
		}
		*target = int(value)
	}
	if options.Enabled {
		if options.Minimum > options.Maximum {
			return options, fmt.Errorf("minimum bitrate exceeds maximum")
		}
		options.Initial = max(options.Minimum, min(options.Initial, options.Maximum))
	}
	return options, nil
}

func newMediaPeer(configuration webrtc.Configuration, options bitrateOptions, preference string) (*webrtc.PeerConnection, cc.BandwidthEstimator, error) {
	engine := &webrtc.MediaEngine{}
	if err := engine.RegisterDefaultCodecs(); err != nil {
		return nil, nil, err
	}
	registry := &interceptor.Registry{}
	var estimator cc.BandwidthEstimator
	if options.Enabled {
		controller, err := cc.NewInterceptor(func() (cc.BandwidthEstimator, error) {
			return gcc.NewSendSideBWE(gcc.SendSideBWEInitialBitrate(options.Initial), gcc.SendSideBWEMinBitrate(options.Minimum), gcc.SendSideBWEMaxBitrate(options.Maximum))
		})
		if err != nil {
			return nil, nil, err
		}
		controller.OnNewPeerConnection(func(_ string, value cc.BandwidthEstimator) { estimator = value })
		registry.Add(controller)
		if err := webrtc.ConfigureTWCCHeaderExtensionSender(engine, registry); err != nil {
			return nil, nil, err
		}
	}
	if err := webrtc.RegisterDefaultInterceptors(engine, registry); err != nil {
		return nil, nil, err
	}
	settings := webrtc.SettingEngine{}
	switch preference {
	case "ipv4":
		settings.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4, webrtc.NetworkTypeTCP4})
	case "ipv6":
		settings.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP6, webrtc.NetworkTypeTCP6})
	}
	peer, err := webrtc.NewAPI(webrtc.WithMediaEngine(engine), webrtc.WithInterceptorRegistry(registry), webrtc.WithSettingEngine(settings)).NewPeerConnection(configuration)
	return peer, estimator, err
}

// One encoder serves all viewers of a source. Honor the slowest viewer's
// estimate (including fixed-rate sessions) without exceeding their limits.
func (m *webRTCManager) updateBitrate(clientID string, peer *webrtc.PeerConnection, bitrate int) {
	m.mu.Lock()
	session, ok := m.sessions[clientID]
	if !ok || session.peer != peer || !session.withVideo {
		m.mu.Unlock()
		return
	}
	if session.bitrate.Enabled {
		bitrate = max(session.bitrate.Minimum, min(bitrate, session.bitrate.Maximum))
	} else {
		bitrate = session.bitrate.Initial
	}
	session.targetBitrate = bitrate
	m.sessions[clientID] = session
	target := bitrate
	for _, other := range m.sessions {
		if other.withVideo && other.targetBitrate > 0 {
			target = min(target, other.targetBitrate)
		}
	}
	delta := target - m.currentBitrate
	if delta < 0 {
		delta = -delta
	}
	if m.currentBitrate > 0 && (delta < m.currentBitrate/10 || time.Since(m.lastBitrateUpdate) < time.Second) {
		m.mu.Unlock()
		return
	}
	m.currentBitrate, m.lastBitrateUpdate = target, time.Now()
	m.mu.Unlock()
	if m.onInput != nil {
		if err := m.onInput(map[string]any{"type": "set_video_bitrate", "bitrate": target}); err == nil {
			log.Printf("video bitrate updated: %d bps", target)
		}
	}
}
