package agent

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	adbclient "github.com/cwithw/ScrcpyCat/internal/adb"
)

type captureProcess interface {
	Wait() error
	Close() error
	Diagnostics() string
}

type captureTransport interface {
	Launch(context.Context, Config, string, streamOptions) (captureProcess, error)
	Dial(context.Context, string) (net.Conn, error)
	Probe(context.Context, Config, io.Writer) error
}

const captureDiagnosticsLimit = 64 << 10

type captureDiagnostics struct {
	mu     sync.Mutex
	buffer []byte
	limit  int
}

func (output *captureDiagnostics) Write(data []byte) (int, error) {
	length := len(data)
	output.mu.Lock()
	defer output.mu.Unlock()
	limit := output.limit
	if limit == 0 {
		limit = captureDiagnosticsLimit
	}
	if length >= limit {
		output.buffer = append(output.buffer[:0], data[length-limit:]...)
		return length, nil
	}
	if excess := len(output.buffer) + length - limit; excess > 0 {
		copy(output.buffer, output.buffer[excess:])
		output.buffer = output.buffer[:len(output.buffer)-excess]
	}
	output.buffer = append(output.buffer, data...)
	return length, nil
}

func (output *captureDiagnostics) String() string {
	output.mu.Lock()
	defer output.mu.Unlock()
	return string(output.buffer)
}

type localCapture struct {
	command     *exec.Cmd
	diagnostics *captureDiagnostics
}

func (p *localCapture) Wait() error         { return p.command.Wait() }
func (p *localCapture) Close() error        { return p.command.Process.Kill() }
func (p *localCapture) Diagnostics() string { return p.diagnostics.String() }

type adbCapture struct {
	*adbclient.Session
	removeStage func()
	diagnostics *captureDiagnostics
}

func (p *adbCapture) Wait() error {
	defer p.removeStage()
	return p.Session.Wait()
}

func (p *adbCapture) Diagnostics() string { return p.diagnostics.String() }

func captureArguments(config Config, scid string, options streamOptions, cleanup bool) []string {
	level := "info"
	if options.Debug {
		level = "debug"
	}
	args := []string{"/", "com.genymobile.scrcpy.Server", config.ScrcpyVersion,
		"scid=" + scid, "log_level=" + level, "tunnel_forward=true", fmt.Sprintf("cleanup=%t", cleanup),
		"control=true", "video_codec=h264", "audio_codec=opus",
		"send_frame_meta=true", "send_device_meta=true", "send_stream_meta=true"}
	return append(args, options.arguments()...)
}

func (d localDevice) Launch(_ context.Context, config Config, scid string, options streamOptions) (captureProcess, error) {
	command := exec.Command("app_process", captureArguments(config, scid, options, false)...)
	command.Env = append(os.Environ(), "CLASSPATH="+config.ScrcpyJar)
	diagnostics := &captureDiagnostics{limit: captureDiagnosticsLimit}
	output := io.MultiWriter(os.Stdout, diagnostics)
	command.Stdout, command.Stderr = output, output
	if err := command.Start(); err != nil {
		return nil, err
	}
	return &localCapture{command: command, diagnostics: diagnostics}, nil
}

func (d localDevice) Dial(ctx context.Context, scid string) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", "@scrcpy_"+scid)
	if err == nil {
		context.AfterFunc(ctx, func() { _ = conn.Close() })
	}
	return conn, err
}

func (d localDevice) Probe(ctx context.Context, config Config, output io.Writer) error {
	command := exec.CommandContext(ctx, "app_process", "/", "com.genymobile.scrcpy.Server", config.ScrcpyVersion,
		"cleanup=false", "list_displays=true", "list_cameras=true")
	command.Env = append(os.Environ(), "CLASSPATH="+config.ScrcpyJar)
	command.Stdout, command.Stderr = output, output
	return command.Run()
}

func (d *adbDevice) directory() string {
	hash := sha256.Sum256([]byte(d.serial))
	return fmt.Sprintf("/data/local/tmp/scrcpycat-host-%x", hash[:8])
}

func (d *adbDevice) prepareDirectory(ctx context.Context) error {
	directory := adbclient.Quote(d.directory())
	// Only this mode's private, per-device staging directory is reconciled.
	// scrcpy's cleanup has already unlinked JARs belonging to live sessions.
	return d.client.Run(ctx, d.serial, "mkdir -p -- "+directory+" && rm -f -- "+directory+"/*.jar "+directory+"/asset-*.apk "+directory+"/.scrcpycat-upload-*", io.Discard, io.Discard)
}

func (d *adbDevice) stageJar(ctx context.Context, config Config, id string) (string, error) {
	file, err := os.Open(config.ScrcpyJar)
	if err != nil {
		return "", err
	}
	defer file.Close()
	path := d.directory() + "/" + id + ".jar"
	if err := d.client.Push(ctx, d.serial, file, path, 0644); err != nil {
		d.removeJar(path)
		return "", err
	}
	return path, nil
}

func (d *adbDevice) removeJar(path string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = d.client.Run(ctx, d.serial, "rm -f -- "+adbclient.Quote(path), io.Discard, io.Discard)
}

func (d *adbDevice) Launch(ctx context.Context, config Config, scid string, options streamOptions) (captureProcess, error) {
	path, err := d.stageJar(ctx, config, scid)
	if err != nil {
		return nil, err
	}
	command, err := adbclient.Command("app_process", captureArguments(config, scid, options, true)...)
	if err != nil {
		d.removeJar(path)
		return nil, err
	}
	// exec is essential: Shell v2 must send SIGHUP to app_process itself, not
	// to an intermediate shell which could leave the camera process orphaned.
	diagnostics := &captureDiagnostics{limit: captureDiagnosticsLimit}
	output := io.MultiWriter(os.Stdout, diagnostics)
	session, err := d.client.Start(ctx, d.serial, "CLASSPATH="+adbclient.Quote(path)+" exec "+command, false, output, output)
	if err != nil {
		d.removeJar(path)
		return nil, err
	}
	return &adbCapture{Session: session, removeStage: func() { d.removeJar(path) }, diagnostics: diagnostics}, nil
}

func (d *adbDevice) Dial(ctx context.Context, scid string) (net.Conn, error) {
	return d.client.Open(ctx, d.serial, "localabstract:scrcpy_"+scid)
}

func (d *adbDevice) Probe(ctx context.Context, config Config, output io.Writer) error {
	id := fmt.Sprintf("probe-%d", time.Now().UnixNano())
	path, err := d.stageJar(ctx, config, id)
	if err != nil {
		return err
	}
	defer d.removeJar(path)
	command, _ := adbclient.Command("app_process", "/", "com.genymobile.scrcpy.Server", config.ScrcpyVersion,
		"cleanup=true", "list_displays=true", "list_cameras=true")
	return d.client.Run(ctx, d.serial, strings.Join([]string{"CLASSPATH=" + adbclient.Quote(path), "exec", command}, " "), output, output)
}
