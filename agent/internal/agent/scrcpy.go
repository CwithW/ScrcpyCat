package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

type scrcpyBridge struct {
	device          *deviceBackend
	config          Config
	mu              sync.Mutex
	process         captureProcess
	streamCancel    context.CancelFunc
	control         chan map[string]any
	controlConn     net.Conn
	onVideo         func(mediaPacket)
	onAudio         func(mediaPacket)
	options         streamOptions
	processDone     chan struct{}
	videoCodec      string
	videoReady      chan struct{}
	captureErr      error
	width, height   uint32
	onDeviceMessage func(map[string]any)
	onError         func(error)
}

func newScrcpyBridge(config Config, backends ...*deviceBackend) *scrcpyBridge {
	device := localBackend()
	if len(backends) > 0 {
		device = backends[0]
	}
	return &scrcpyBridge{config: config, device: device, control: make(chan map[string]any, 256)}
}

func (b *scrcpyBridge) Start(parent context.Context, options streamOptions) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.process != nil {
		if options.Preview && !options.Reconfigure {
			return nil
		}
		if options.PowerOff == b.options.PowerOff && options.Debug == b.options.Debug && strings.Join(options.arguments(), " ") == strings.Join(b.options.arguments(), " ") {
			b.options = options
			return nil
		}
		if err := b.stopLocked(); err != nil {
			return err
		}
	}
	return b.startLocked(parent, options)
}

func (b *scrcpyBridge) startLocked(parent context.Context, options streamOptions) error {
	if _, err := os.Stat(b.config.ScrcpyJar); err != nil {
		return errors.New("scrcpy server jar is missing")
	}
	var randomID [4]byte
	if _, err := rand.Read(randomID[:]); err != nil {
		return err
	}
	scid := fmt.Sprintf("%08x", binary.BigEndian.Uint32(randomID[:])&0x7fffffff)
	streamCtx, cancel := context.WithCancel(parent)
	cmd, err := b.device.capture.Launch(parent, b.config, scid, options)
	if err != nil {
		cancel()
		return err
	}
	log.Printf("capture started: source=%s preview=%t audio=%t", options.Source, options.Preview, options.Audio)
	done := make(chan struct{})
	b.process, b.streamCancel, b.processDone, b.options = cmd, cancel, done, options
	b.videoCodec, b.videoReady, b.captureErr = "", make(chan struct{}), nil
	go func() {
		if err := b.attachVideo(streamCtx, cmd, scid, options); err != nil && streamCtx.Err() == nil {
			b.captureFailed(cmd, err, false)
		}
	}()
	go func() {
		err := cmd.Wait()
		close(done)
		if streamCtx.Err() == nil {
			if err == nil {
				// scrcpy may exit with status 0 after a camera configuration error.
				err = errors.New("capture ended unexpectedly; check the selected camera or resolution")
			}
			b.captureFailed(cmd, err, true)
		}
	}()
	return nil
}

func (b *scrcpyBridge) Stop() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stopLocked()
}

func (b *scrcpyBridge) stopLocked() error {
	process, done := b.process, b.processDone
	b.process, b.processDone = nil, nil
	if b.videoReady != nil {
		close(b.videoReady)
		b.videoReady = nil
	}
	if b.controlConn != nil {
		if b.device.adb == nil && b.options.PowerOff && b.options.Source != "camera" {
			_ = b.controlConn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
			_, _ = b.controlConn.Write([]byte{10, 1})
		}
		_ = b.controlConn.Close()
		b.controlConn = nil
	}
	if b.streamCancel != nil {
		b.streamCancel()
		b.streamCancel = nil
	}

	if process == nil {
		return nil
	}
	if done != nil {
		grace := 200 * time.Millisecond
		if b.device.adb != nil {
			grace = time.Second
		}
		select {
		case <-done:
			return nil
		case <-time.After(grace):
		}
	}
	err := process.Close()
	if errors.Is(err, os.ErrProcessDone) {
		err = nil
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			return errors.New("scrcpy did not stop")
		}
	}
	return err
}

func (b *scrcpyBridge) EnqueueControl(message map[string]any) error {
	b.mu.Lock()
	width, height := b.width, b.height
	b.mu.Unlock()
	if width > 0 && height > 0 {
		message = remapControlPosition(message, width, height)
	}
	select {
	case b.control <- message:
		return nil
	default:
		return errors.New("control queue is full")
	}
}

func (b *scrcpyBridge) SetPreviewPublisher(publisher func(mediaPacket)) {
	b.mu.Lock()
	b.onVideo = publisher
	b.mu.Unlock()
}

func (b *scrcpyBridge) captureFailed(process captureProcess, err error, stopped bool) {
	b.mu.Lock()
	if b.process != process {
		b.mu.Unlock()
		return
	}
	source, publisher := b.options.Source, b.onError
	b.captureErr = err
	_ = b.stopLocked()
	b.mu.Unlock()
	state := "failed"
	if stopped {
		state = "stopped"
	}
	err = fmt.Errorf("scrcpy %s capture %s: %w", source, state, err)
	fmt.Fprintln(os.Stderr, err)
	if publisher != nil {
		publisher(err)
	}
}

func (b *scrcpyBridge) attachVideo(ctx context.Context, process captureProcess, scid string, options streamOptions) error {
	connection, err := b.dialSocket(ctx, scid, "video")
	if err != nil {
		return err
	}
	defer connection.Close()
	_ = connection.SetReadDeadline(time.Now().Add(10 * time.Second))

	// The dummy byte arrives as soon as the video socket is accepted. Only then
	// may we attach the control socket; otherwise the server treats it as video.
	if err := readScrcpyDummy(connection); err != nil {
		return fmt.Errorf("read scrcpy video dummy byte: %w", err)
	}
	// Socket acceptance order is video, optional audio, then control.
	var audio net.Conn
	if options.Audio {
		audio, err = b.dialSocket(ctx, scid, "audio")
		if err != nil {
			return err
		}
		defer audio.Close()
	}
	control, err := b.dialSocket(ctx, scid, "control")
	if err != nil {
		return err
	}
	defer control.Close()
	if !b.setControlConn(ctx, control) {
		return ctx.Err()
	}
	defer b.clearControlConn(control)
	if options.PowerOff && options.Source != "camera" {
		_, _ = control.Write([]byte{10, 0})
	}
	go b.writeControls(ctx, control)
	go b.readDeviceMessages(control)
	if audio != nil {
		go b.readAudio(audio)
	}

	deviceName, err := readScrcpyDeviceName(connection)
	if err != nil {
		return fmt.Errorf("read scrcpy device metadata: %w", err)
	}
	if deviceName != "" {
		fmt.Fprintf(os.Stderr, "scrcpy attached to %s\n", deviceName)
	}
	codec, err := readScrcpyCodec(connection, false)
	if err != nil {
		return fmt.Errorf("read scrcpy video metadata: %w", err)
	}
	if codec != "h264" {
		return fmt.Errorf("unsupported scrcpy video codec %q", codec)
	}
	_ = connection.SetReadDeadline(time.Time{})

	var codecConfig []byte
	for {
		packet, err := readScrcpyPacket(connection)
		if err != nil {
			return fmt.Errorf("read scrcpy H.264 packet: %w", err)
		}
		if packet.Config {
			codecConfig = append(codecConfig[:0], packet.Data...)
			b.mu.Lock()
			if b.process == process {
				b.videoCodec = h264ProfileLevel(codecConfig)
				if b.videoCodec != "" && b.videoReady != nil {
					close(b.videoReady)
					b.videoReady = nil
				}
			}
			b.mu.Unlock()
			continue
		}
		if packet.Session {
			b.mu.Lock()
			b.width, b.height = packet.Width, packet.Height
			b.mu.Unlock()
			continue
		}
		if packet.KeyFrame && len(codecConfig) > 0 && !bytes.HasPrefix(packet.Data, codecConfig) {
			packet.Data = append(append([]byte(nil), codecConfig...), packet.Data...)
		}
		b.mu.Lock()
		publisher := b.onVideo
		b.mu.Unlock()
		if publisher != nil {
			publisher(packet)
		}
	}
}

type captureSocket struct {
	net.Conn
	cancel context.CancelFunc
}

func (c *captureSocket) Close() error {
	c.cancel()
	return c.Conn.Close()
}

func (b *scrcpyBridge) dialSocket(ctx context.Context, scid, kind string) (net.Conn, error) {
	dialCtx, cancel := context.WithCancel(ctx)
	deadline := time.AfterFunc(10*time.Second, cancel)
	defer deadline.Stop()
	for {
		connection, err := b.device.capture.Dial(dialCtx, scid)
		if err == nil {
			return &captureSocket{Conn: connection, cancel: cancel}, nil
		}
		select {
		case <-dialCtx.Done():
			cancel()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("connect scrcpy %s socket: %w", kind, err)
		case <-time.After(100 * time.Millisecond):
		}
	}

}

func (b *scrcpyBridge) setControlConn(ctx context.Context, connection net.Conn) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ctx.Err() != nil {
		return false
	}
	b.controlConn = connection
	return true
}

func (b *scrcpyBridge) clearControlConn(connection net.Conn) {
	b.mu.Lock()
	if b.controlConn == connection {
		b.controlConn = nil
	}
	b.mu.Unlock()
}

func (b *scrcpyBridge) writeControls(ctx context.Context, connection net.Conn) {
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-b.control:
			payload, err := encodeControlEvent(event)
			if err != nil {
				fmt.Fprintf(os.Stderr, "encode scrcpy control event: %v\n", err)
				continue
			}
			if _, err := connection.Write(payload); err != nil {
				return
			}
		}
	}
}

func (b *scrcpyBridge) SetDeviceMessagePublisher(publisher func(map[string]any)) {
	b.mu.Lock()
	b.onDeviceMessage = publisher
	b.mu.Unlock()
}

func (b *scrcpyBridge) readDeviceMessages(reader io.Reader) {
	for {
		message, err := readDeviceMessage(reader)
		if err != nil {
			return
		}
		b.mu.Lock()
		publisher := b.onDeviceMessage
		b.mu.Unlock()
		if publisher != nil && message != nil {
			publisher(message)
		}
	}
}

func readDeviceMessage(reader io.Reader) (map[string]any, error) {
	var kind [1]byte
	if _, err := io.ReadFull(reader, kind[:]); err != nil {
		return nil, err
	}
	switch kind[0] {
	case 0:
		var size uint32
		if err := binary.Read(reader, binary.BigEndian, &size); err != nil {
			return nil, err
		}
		if size > (1<<18)-5 {
			return nil, errors.New("clipboard message is too large")
		}
		contents := make([]byte, size)
		if _, err := io.ReadFull(reader, contents); err != nil {
			return nil, err
		}
		return map[string]any{"type": "clipboard", "text": string(contents), "source": "device"}, nil
	case 1:
		var sequence uint64
		err := binary.Read(reader, binary.BigEndian, &sequence)
		return nil, err
	case 2:
		var header [4]byte
		if _, err := io.ReadFull(reader, header[:]); err != nil {
			return nil, err
		}
		_, err := io.CopyN(io.Discard, reader, int64(binary.BigEndian.Uint16(header[2:])))
		return nil, err
	default:
		return nil, errors.New("unknown scrcpy device message")
	}
}

func (b *scrcpyBridge) SetAudioPublisher(publisher func(mediaPacket)) {
	b.mu.Lock()
	b.onAudio = publisher
	b.mu.Unlock()
}

func (b *scrcpyBridge) readAudio(connection net.Conn) {
	codec, err := readScrcpyCodec(connection, false)
	if err != nil || codec != "opus" {
		fmt.Fprintf(os.Stderr, "scrcpy audio unavailable: codec %q, %v\n", codec, err)
		return
	}
	for {
		packet, err := readScrcpyPacket(connection)
		if err != nil {
			return
		}
		if packet.Config || packet.Session {
			continue
		}
		b.mu.Lock()
		publisher := b.onAudio
		b.mu.Unlock()
		if publisher != nil {
			publisher(packet)
		}
	}
}

func remapControlPosition(event map[string]any, width, height uint32) map[string]any {
	switch stringField(event, "type") {
	case "touch", "scroll", "inject_scroll":
	default:
		return event
	}
	x, y, w, h, err := positionFromEvent(event)
	if err != nil || w == 0 || h == 0 {
		return event
	}
	result := make(map[string]any, len(event))
	for key, value := range event {
		result[key] = value
	}
	result["x"] = float64(x) * float64(width) / float64(w)
	result["y"] = float64(y) * float64(height) / float64(h)
	result["x"] = float64(uint32(result["x"].(float64)))
	result["y"] = float64(uint32(result["y"].(float64)))
	result["w"], result["h"] = float64(width), float64(height)
	return result
}

func (b *scrcpyBridge) SetErrorPublisher(publisher func(error)) {
	b.mu.Lock()
	b.onError = publisher
	b.mu.Unlock()
}

func (b *scrcpyBridge) currentOptions() streamOptions {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.process == nil {
		return streamOptions{}
	}
	options := b.options
	options.Values = make(map[string]string, len(b.options.Values))
	for key, value := range b.options.Values {
		options.Values[key] = value
	}
	return options
}
