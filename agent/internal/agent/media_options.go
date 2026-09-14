package agent

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type streamOptions struct {
	Reconfigure bool
	Preview     bool
	PowerOff    bool
	// ForceH264Baseline preserves the browser-compatible default. A capture
	// session may disable it once when a legacy device rejects the profile.
	ForceH264Baseline bool
	Bitrate           bitrateOptions
	Audio             bool
	Source            string
	Values            map[string]string
}

func parseStreamOptions(raw map[string]any, preview bool) (streamOptions, error) {
	options := streamOptions{Preview: preview, Reconfigure: preview, ForceH264Baseline: true, Source: "display", Values: map[string]string{}}
	options.Audio, _ = raw["audio"].(bool)
	options.PowerOff, _ = raw["power_off"].(bool)
	var err error
	options.Bitrate, err = parseBitrateOptions(raw)
	if err != nil {
		return options, err
	}
	if source := stringField(raw, "video_source"); source != "" {
		if source != "display" && source != "camera" {
			return options, errors.New("unsupported video source")
		}
		options.Source = source
	}
	numeric := map[string]int{"max_fps": 240, "fps": 240, "max_size": 8192, "bitrate": 100000000, "camera_fps": 240}
	for key, maximum := range numeric {
		if _, ok := raw[key]; !ok {
			continue
		}
		value, err := uint32Field(raw, key)
		if err != nil || value > uint32(maximum) {
			return options, fmt.Errorf("invalid %s", key)
		}
		name := key
		if key == "bitrate" {
			name = "video_bit_rate"
		}
		if key == "fps" {
			name = "max_fps"
		}
		options.Values[name] = strconv.FormatUint(uint64(value), 10)
	}
	options.Values["video_bit_rate"] = strconv.Itoa(options.Bitrate.Initial)
	for _, key := range []string{"stay_awake", "audio_dup", "camera_high_speed"} {
		if value, ok := raw[key].(bool); ok {
			options.Values[key] = strconv.FormatBool(value)
		}
	}
	for _, key := range []string{"audio_source", "camera_facing", "camera_id", "camera_size", "camera_ar", "video_codec_options"} {
		if value := stringField(raw, key); value != "" {
			if len(value) > 1024 || strings.ContainsAny(value, "\x00\r\n") {
				return options, fmt.Errorf("invalid %s", key)
			}
			options.Values[key] = value
		}
	}
	if value := options.Values["camera_size"]; value != "" && !regexp.MustCompile("^[0-9]{1,5}x[0-9]{1,5}$").MatchString(value) {
		return options, errors.New("invalid camera_size")
	}
	if value, ok := raw["camera_zoom"].(float64); ok {
		if value < 0.1 || value > 100 {
			return options, errors.New("invalid camera_zoom")
		}
		options.Values["camera_zoom"] = strconv.FormatFloat(value, 'f', -1, 64)
	}
	if options.Values["audio_dup"] == "true" && options.Values["audio_source"] == "" {
		options.Values["audio_source"] = "playback"
	}
	// Duplication only applies to playback; never override an explicit source.
	if options.Values["audio_source"] != "playback" {
		delete(options.Values, "audio_dup")
	}
	return options, nil
}

func (options streamOptions) withoutH264Profile() streamOptions {
	options.ForceH264Baseline = false
	values := options.Values
	options.Values = make(map[string]string, len(options.Values))
	for key, value := range values {
		if key == "video_codec_options" {
			value = withoutCodecOption(value, "profile")
			if value == "" {
				continue
			}
		}
		options.Values[key] = value
	}
	return options
}

func withoutCodecOption(value, name string) string {
	parts := strings.Split(value, ",")
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		key, _, _ := strings.Cut(strings.TrimSpace(part), "=")
		if strings.EqualFold(key, name) {
			continue
		}
		if part = strings.TrimSpace(part); part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, ",")
}

func (options streamOptions) arguments() []string {
	source := options.Source
	if source == "" {
		source = "display"
	}
	args := []string{"video=true", "audio=" + strconv.FormatBool(options.Audio), "video_source=" + source}
	values := map[string]string{"video_codec_options": "i-frame-interval=1"}
	for key, value := range options.Values {
		values[key] = value
	}
	if options.ForceH264Baseline {
		// Match the Baseline SDP and the HTTP/WASM preview decoder.
		values["video_codec_options"] += ",profile=1"
	} else {
		values["video_codec_options"] = withoutCodecOption(values["video_codec_options"], "profile")
	}
	var keys []string
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, key+"="+values[key])
	}
	return args
}
