package agent

import (
	"context"
	"fmt"
	"time"
)

func h264ProfileLevel(data []byte) string {
	for i := 0; i+6 < len(data); i++ {
		if data[i] == 0 && data[i+1] == 0 && data[i+2] == 1 && data[i+3]&31 == 7 {
			return fmt.Sprintf("%02x%02x%02x", data[i+4], data[i+5], data[i+6])
		}
	}
	return ""
}

// Wait for the first SPS from the one running encoder, never probe by starting
// a second scrcpy process. This prevents advertising Baseline for a Main/High
// stream when an OEM encoder chooses its own default profile.
func (b *scrcpyBridge) VideoCodec(ctx context.Context) (string, error) {
	b.mu.Lock()
	ready := b.videoReady
	b.mu.Unlock()
	if ready != nil {
		timer := time.NewTimer(12 * time.Second)
		defer timer.Stop()
		select {
		case <-ready:
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timer.C:
			return "", fmt.Errorf("waiting for scrcpy H.264 configuration timed out")
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.captureErr != nil {
		return "", b.captureErr
	}
	if b.process == nil || b.videoCodec == "" {
		return "", fmt.Errorf("scrcpy has no H.264 configuration")
	}
	return b.videoCodec, nil
}
