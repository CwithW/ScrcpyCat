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
	Debug       bool
	Bitrate     bitrateOptions
	Audio       bool
	Source      string
	Values      map[string]string
}

func parseStreamOptions(raw map[string]any, preview bool) (streamOptions, error) {
	if value, ok := raw["preview"].(bool); ok {
		preview = value
	}
	options := streamOptions{Preview: preview, Reconfigure: true, Source: "display", Values: map[string]string{}}
	options.Audio, _ = raw["audio"].(bool)
	options.PowerOff, _ = raw["power_off"].(bool)
	options.Debug, _ = raw["debug"].(bool)
	awake := !preview
	if value, ok := raw["stay_awake"].(bool); ok {
		awake = value
	}
	// stay_awake changes a persistent Android setting via scrcpy cleanup and
	// only works while charging. keep_active follows the capture lifecycle,
	// also works on battery, and works with local mode's cleanup=false.
	options.Values["keep_active"] = strconv.FormatBool(awake)
	options.Values["power_on"] = strconv.FormatBool(!preview || awake)
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
	numeric := map[string]int{"max_fps": 240, "max_size": 8192, "camera_fps": 240}
	for key, maximum := range numeric {
		if _, ok := raw[key]; !ok {
			continue
		}
		value, err := uint32Field(raw, key)
		if err != nil || value > uint32(maximum) {
			return options, fmt.Errorf("invalid %s", key)
		}
		name := key
		options.Values[name] = strconv.FormatUint(uint64(value), 10)
	}
	if _, explicit := raw["max_fps"]; !explicit {
		if _, ok := raw["fps"]; ok {
			value, err := uint32Field(raw, "fps")
			if err != nil || value > 240 {
				return options, errors.New("invalid fps")
			}
			options.Values["max_fps"] = strconv.FormatUint(uint64(value), 10)
		}
	}
	options.Values["video_bit_rate"] = strconv.Itoa(options.Bitrate.Initial)
	for _, key := range []string{"audio_dup", "camera_high_speed"} {
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
	if orientation := stringField(raw, "camera_orientation"); orientation != "" && orientation != "auto" {
		switch orientation {
		case "0", "90", "180", "270":
			options.Values["capture_orientation"] = orientation
		default:
			return options, errors.New("invalid camera_orientation")
		}
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
	// Let MediaCodec choose its compatible profile on the first launch. The
	// actual SPS is used for WebRTC negotiation and browser decoder setup.
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
