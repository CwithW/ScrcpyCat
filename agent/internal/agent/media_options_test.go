package agent

import (
	"strings"
	"testing"
)

func TestMediaOptionsReachScrcpy(t *testing.T) {
	options, err := parseStreamOptions(map[string]any{
		"max_fps": float64(30), "max_size": float64(720), "bitrate": float64(2000000),
		"audio": true, "audio_source": "output", "audio_dup": true,
		"video_source": "camera", "camera_id": "0", "camera_size": "1280x720", "camera_zoom": float64(1),
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	arguments := " " + strings.Join(options.arguments(), " ") + " "
	for _, expected := range []string{" max_fps=30 ", " max_size=720 ", " video_bit_rate=2000000 ", " audio=true ", " audio_source=output ", " video_source=camera ", " camera_id=0 ", " camera_size=1280x720 "} {
		if !strings.Contains(arguments, expected) {
			t.Fatalf("missing %s in %s", expected, arguments)
		}
	}
	if _, err := parseStreamOptions(map[string]any{"max_size": float64(-1)}, false); err == nil {
		t.Fatal("negative size accepted")
	}
}

func TestControlCoordinatesFollowCaptureSize(t *testing.T) {
	event := map[string]any{"type": "touch", "action": float64(0), "id": float64(-1), "x": float64(540), "y": float64(960), "w": float64(1080), "h": float64(1920)}
	mapped := remapControlPosition(event, 1440, 3200)
	if mapped["x"] != float64(720) || mapped["y"] != float64(1600) || mapped["w"] != float64(1440) || mapped["h"] != float64(3200) {
		t.Fatal(mapped)
	}
	if event["w"] != float64(1080) {
		t.Fatal("mutated shared control event")
	}
	if _, err := encodeControlEvent(mapped); err != nil {
		t.Fatal(err)
	}
}

func TestAudioSourceAndDuplication(t *testing.T) {
	cases := []struct {
		name, source string
		dup          bool
		wantSource   string
		wantDup      string
	}{
		{"explicit output with duplication preference", "output", true, "output", ""},
		{"explicit output", "output", false, "output", ""},
		{"microphone with duplication preference", "mic", true, "mic", ""},
		{"playback duplication", "playback", true, "playback", "true"},
		{"playback without duplication", "playback", false, "playback", "false"},
		{"implicit playback duplication", "", true, "playback", "true"},
		{"default output", "", false, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			options, err := parseStreamOptions(map[string]any{
				"audio": true, "audio_source": tc.source, "audio_dup": tc.dup,
			}, false)
			if err != nil {
				t.Fatal(err)
			}
			if options.Values["audio_source"] != tc.wantSource || options.Values["audio_dup"] != tc.wantDup {
				t.Fatalf("source=%q dup=%q; want source=%q dup=%q", options.Values["audio_source"], options.Values["audio_dup"], tc.wantSource, tc.wantDup)
			}
			if !options.Audio {
				t.Fatal("audio capture disabled")
			}
		})
	}
}

func TestWebSocketAudioKeepsDisplayCaptureAndRTCPreferencesSeparate(t *testing.T) {
	manager := &mediaManager{wsAudioOptions: map[string]string{"audio_source": "output"}}
	manager.wsAudioActive.Store(true)
	raw := map[string]any{"audio": false, "max_size": float64(720), "audio_source": "mic"}
	options, err := parseStreamOptions(raw, true)
	if err != nil {
		t.Fatal(err)
	}
	options = manager.displayOptions(options)
	if !options.Audio || options.Values["audio_source"] != "output" || options.Values["max_size"] != "720" {
		t.Fatal(options)
	}
	if raw["audio"] != false || raw["audio_source"] != "mic" {
		t.Fatal("modified RTC viewer preferences")
	}
}
