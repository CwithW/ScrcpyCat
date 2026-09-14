package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"strconv"
	"strings"
	"time"
)

type metricSample struct {
	at                                time.Time
	cpuTotal, cpuIdle, received, sent uint64
}

func cpuCounters(raw string) (total, idle uint64) {
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "cpu" {
			continue
		}
		// Guest time is already included in user/nice counters.
		for i, value := range fields[1:min(len(fields), 9)] {
			count, _ := strconv.ParseUint(value, 10, 64)
			total += count
			if i == 3 || i == 4 {
				idle += count
			}
		}
		break
	}
	return
}

func networkCounters(raw string) (received, sent uint64) {
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "lo" {
			continue
		}
		fields := strings.Fields(parts[1])
		if len(fields) < 9 {
			continue
		}
		rx, _ := strconv.ParseUint(fields[0], 10, 64)
		tx, _ := strconv.ParseUint(fields[8], 10, 64)
		received, sent = received+rx, sent+tx
	}
	return
}

func numericLines(raw string) map[string]float64 {
	values := map[string]float64{}
	for _, line := range strings.Split(raw, "\n") {
		pair := strings.SplitN(line, ":", 2)
		if len(pair) != 2 {
			continue
		}
		fields := strings.Fields(pair[1])
		if len(fields) == 0 {
			continue
		}
		if value, err := strconv.ParseFloat(fields[0], 64); err == nil {
			values[strings.TrimSpace(pair[0])] = value
		}
	}
	return values
}

func collectMetrics(ctx context.Context, previous metricSample, backend *deviceBackend) (map[string]any, metricSample) {
	ctx, cancelMetrics := context.WithTimeout(ctx, 4*time.Second)
	defer cancelMetrics()
	current := metricSample{at: time.Now()}
	metrics := map[string]any{"timestamp": current.at.UTC().Format(time.RFC3339)}
	if raw, err := backend.metrics.ReadMetric(ctx, "/proc/stat"); err == nil {
		current.cpuTotal, current.cpuIdle = cpuCounters(string(raw))
		if previous.cpuTotal > 0 && current.cpuTotal > previous.cpuTotal && current.cpuIdle >= previous.cpuIdle {
			total, idle := current.cpuTotal-previous.cpuTotal, current.cpuIdle-previous.cpuIdle
			metrics["cpu"] = 100 * (1 - min(float64(idle)/float64(total), 1.0))
		}
	}
	if raw, err := backend.metrics.ReadMetric(ctx, "/proc/meminfo"); err == nil {
		values := numericLines(string(raw))
		total, available := values["MemTotal"], values["MemAvailable"]
		if available == 0 {
			available = values["MemFree"] + values["Buffers"] + values["Cached"]
		}
		if total > 0 {
			metrics["memory_percent"] = 100 * (1 - available/total)
			metrics["memory_total"] = total * 1024
			metrics["memory_used"] = (total - available) * 1024
		}
	}
	if disk, err := backend.metrics.DiskUsage(ctx); err == nil && disk.blocks > 0 {
		metrics["disk_percent"] = 100 * (1 - float64(disk.available)/float64(disk.blocks))
		metrics["disk_total"] = float64(disk.blocks) * float64(disk.blockSize)
		metrics["disk_used"] = float64(disk.blocks-disk.available) * float64(disk.blockSize)
	}
	if raw, err := backend.metrics.ReadMetric(ctx, "/proc/net/dev"); err == nil {
		current.received, current.sent = networkCounters(string(raw))
		elapsed := current.at.Sub(previous.at).Seconds()
		if !previous.at.IsZero() && elapsed > 0 && current.received >= previous.received && current.sent >= previous.sent {
			metrics["download_speed"] = float64(current.received-previous.received) / elapsed
			metrics["upload_speed"] = float64(current.sent-previous.sent) / elapsed
		}
	}
	batteryCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if raw, err := executeOutput(batteryCtx, backend.commands, "dumpsys", "battery"); err == nil {
		values := numericLines(raw)
		if value, ok := values["temperature"]; ok {
			metrics["temperature"] = value / 10
		}
		if value, ok := values["level"]; ok {
			metrics["battery"] = value
		}
	}
	return metrics, current
}

func thumbnailPNG(contents []byte, maxSize int) ([]byte, error) {
	config, err := png.DecodeConfig(bytes.NewReader(contents))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > 16384 || config.Height > 16384 || config.Width*config.Height > 32_000_000 {
		return nil, fmt.Errorf("invalid screenshot dimensions")
	}
	source, err := png.Decode(bytes.NewReader(contents))
	if err != nil {
		return nil, err
	}
	width, height := config.Width, config.Height
	if width > maxSize || height > maxSize {
		ratio := float64(maxSize) / float64(max(width, height))
		width, height = max(1, int(float64(width)*ratio)), max(1, int(float64(height)*ratio))
	}
	resized := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			resized.Set(x, y, source.At(x*config.Width/width, y*config.Height/height))
		}
	}
	var out bytes.Buffer
	err = png.Encode(&out, resized)
	return out.Bytes(), err
}

func (c *client) reportTelemetry(ctx context.Context, writer *lockedWriter, media *mediaManager) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	previous := metricSample{}
	nextSnapshot, nextMetrics := time.Now(), time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if !time.Now().Before(nextMetrics) {
			metrics, sample := collectMetrics(ctx, previous, c.device())
			previous = sample
			_ = writer.writeJSON(map[string]any{"message_type": "device_metrics", "metrics": metrics})
			nextMetrics = time.Now().Add(5 * time.Second)
		}
		interval := c.snapshotInterval.Load()
		if interval < 0 || time.Now().Before(nextSnapshot) || media.HasSessions() || c.previewActive.Load() {
			continue
		}
		nextSnapshot = time.Now().Add(time.Duration(max(interval, 1)) * time.Second)
		captureCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		output := boundedOutput{limit: 32 << 20}
		err := c.device().commands.Execute(captureCtx, "screencap", []string{"-p"}, &output, nil)
		cancel()
		if err != nil {
			continue
		}
		thumbnail, err := thumbnailPNG(output.buffer.Bytes(), 360)
		if err != nil {
			continue
		}
		_ = writer.writeJSON(map[string]any{"message_type": "snapshot_update", "data": base64.StdEncoding.EncodeToString(thumbnail)})
	}
}

func (c *client) setSnapshotInterval(settings map[string]any) {
	if interval, ok := settings["snapshotInterval"].(float64); ok && interval >= -1 && interval <= 3600 {
		c.snapshotInterval.Store(int64(interval))
	}
}
