package agent

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"time"
)

var displayDescription = regexp.MustCompile(`--display-id=(\d+)\s+\((\d+)x(\d+)\)`)
var cameraDescription = regexp.MustCompile(`--camera-id=([^\s]+)\s+\((front|back|external),\s*(\d+)x(\d+)([^\n]*)`)
var zoomDescription = regexp.MustCompile(`zoom-range=\[([\d.]+),\s*([\d.]+)\]`)

func parseMediaInfo(raw string) ([]map[string]any, []map[string]any) {
	displays, cameras := []map[string]any{}, []map[string]any{}
	for _, match := range displayDescription.FindAllStringSubmatch(raw, -1) {
		id, _ := strconv.Atoi(match[1])
		width, _ := strconv.Atoi(match[2])
		height, _ := strconv.Atoi(match[3])
		displays = append(displays, map[string]any{"id": id, "x_res": width, "y_res": height})
	}
	for _, match := range cameraDescription.FindAllStringSubmatch(raw, -1) {
		width, _ := strconv.Atoi(match[3])
		height, _ := strconv.Atoi(match[4])
		camera := map[string]any{"id": match[1], "facing": match[2], "x_res": width, "y_res": height}
		if zoom := zoomDescription.FindStringSubmatch(match[5]); len(zoom) == 3 {
			camera["zoom_min"], _ = strconv.ParseFloat(zoom[1], 64)
			camera["zoom_max"], _ = strconv.ParseFloat(zoom[2], 64)
		}
		cameras = append(cameras, camera)
	}
	return displays, cameras
}

func mediaInfo(config Config, capture captureTransport) ([]map[string]any, []map[string]any) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output := boundedOutput{limit: 1 << 20}
	if err := capture.Probe(ctx, config, &output); err != nil {
		fmt.Fprintf(os.Stderr, "list capture devices: %v\n", err)
	}
	return parseMediaInfo(output.buffer.String())
}
