package agent

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestScrcpyServerSurvivesRepeatedSessions(t *testing.T) {
	dir := t.TempDir()
	jar := filepath.Join(dir, "scrcpy-server.jar")
	if err := os.WriteFile(jar, []byte("test jar"), 0600); err != nil {
		t.Fatal(err)
	}
	// Model scrcpy v4.1 cleanup: its helper unlinks CLASSPATH at startup.
	script := "#!/bin/sh\ncase \" $* \" in *\" cleanup=false \"*) ;; *) rm -f -- \"$CLASSPATH\" ;; esac\n: > \"$SCRCPYCAT_TEST_STARTED\"\nexec sleep 30\n"
	if err := os.WriteFile(filepath.Join(dir, "app_process"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	bridge := newScrcpyBridge(Config{ScrcpyJar: jar, ScrcpyVersion: "4.1"})
	t.Cleanup(func() { _ = bridge.Stop() })
	for round := 0; round < 2; round++ {
		started := filepath.Join(dir, "started-"+strconv.Itoa(round))
		t.Setenv("SCRCPYCAT_TEST_STARTED", started)
		if err := bridge.Start(context.Background(), streamOptions{}); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(time.Second)
		for {
			if _, err := os.Stat(started); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("scrcpy did not start")
			}
			time.Sleep(10 * time.Millisecond)
		}
		if _, err := os.Stat(jar); err != nil {
			t.Fatalf("session %d removed reusable scrcpy server: %v", round+1, err)
		}
		if err := bridge.Stop(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScrcpyUnexpectedSuccessfulExitIsReported(t *testing.T) {
	directory := t.TempDir()
	jar := filepath.Join(directory, "scrcpy-server.jar")
	if err := os.WriteFile(jar, []byte("test jar"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "app_process"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	bridge := newScrcpyBridge(Config{ScrcpyJar: jar, ScrcpyVersion: "4.1"})
	t.Cleanup(func() { _ = bridge.Stop() })
	reported := make(chan error, 1)
	bridge.SetErrorPublisher(func(err error) { reported <- err })
	if err := bridge.Start(context.Background(), streamOptions{Source: "camera"}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-reported:
		if !strings.Contains(err.Error(), "camera capture stopped") {
			t.Fatalf("unexpected capture error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("unexpected capture exit left the browser waiting for video")
	}
}

type waitingCaptureProcess struct {
	done chan struct{}
	once sync.Once
}

func (p *waitingCaptureProcess) Wait() error { <-p.done; return nil }
func (p *waitingCaptureProcess) Close() error {
	p.once.Do(func() { close(p.done) })
	return nil
}
func (p *waitingCaptureProcess) Diagnostics() string { return "" }

type brokenCaptureTransport struct{ process *waitingCaptureProcess }

func (d brokenCaptureTransport) Launch(context.Context, Config, string, streamOptions) (captureProcess, error) {
	return d.process, nil
}
func (d brokenCaptureTransport) Probe(context.Context, Config, io.Writer) error { return nil }
func (d brokenCaptureTransport) Dial(context.Context, string) (net.Conn, error) {
	reader, writer := net.Pipe()
	_ = writer.Close()
	return reader, nil
}

func TestFailedMediaHandshakeStopsForegroundCaptureAndReportsError(t *testing.T) {
	jar := filepath.Join(t.TempDir(), "scrcpy-server.jar")
	if err := os.WriteFile(jar, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	process := &waitingCaptureProcess{done: make(chan struct{})}
	backend := localBackend()
	backend.capture = brokenCaptureTransport{process: process}
	bridge := newScrcpyBridge(Config{ScrcpyJar: jar}, backend)
	t.Cleanup(func() { _ = bridge.Stop() })
	reported := make(chan error, 2)
	bridge.SetErrorPublisher(func(err error) { reported <- err })
	if err := bridge.Start(context.Background(), streamOptions{Source: "camera"}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-reported:
		if !strings.Contains(err.Error(), "dummy byte") {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("failed media handshake left the capture running")
	}
	select {
	case <-process.done:
	default:
		t.Fatal("foreground Shell session remained open after media failure")
	}
	bridge.mu.Lock()
	running := bridge.process != nil
	bridge.mu.Unlock()
	if running {
		t.Fatal("failed capture still occupies the bridge")
	}
}

type scriptedCaptureProcess struct {
	done        chan struct{}
	err         error
	diagnostics string
	once        sync.Once
}

func (p *scriptedCaptureProcess) Wait() error { <-p.done; return p.err }
func (p *scriptedCaptureProcess) Close() error {
	p.once.Do(func() { close(p.done) })
	return nil
}
func (p *scriptedCaptureProcess) Diagnostics() string { return p.diagnostics }

type scriptedCaptureTransport struct {
	processes []*scriptedCaptureProcess
	launches  []streamOptions
	mu        sync.Mutex
}

func (d *scriptedCaptureTransport) Launch(_ context.Context, _ Config, _ string, options streamOptions) (captureProcess, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.launches = append(d.launches, options)
	if len(d.launches) > len(d.processes) {
		return nil, errors.New("unexpected capture launch")
	}
	return d.processes[len(d.launches)-1], nil
}
func (d *scriptedCaptureTransport) Probe(context.Context, Config, io.Writer) error { return nil }
func (d *scriptedCaptureTransport) Dial(context.Context, string) (net.Conn, error) {
	return nil, errors.New("test media socket unavailable")
}
func (d *scriptedCaptureTransport) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.launches)
}
func (d *scriptedCaptureTransport) options(index int) streamOptions {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.launches[index]
}

func TestMediaCodecFailureRetriesOnceWithoutProfile(t *testing.T) {
	jar := filepath.Join(t.TempDir(), "scrcpy-server.jar")
	if err := os.WriteFile(jar, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	first := &scriptedCaptureProcess{done: make(chan struct{}), err: errors.New("scrcpy exited"), diagnostics: "Capture/encoding error: android.media.MediaCodec$CodecException"}
	close(first.done)
	second := &scriptedCaptureProcess{done: make(chan struct{})}
	transport := &scriptedCaptureTransport{processes: []*scriptedCaptureProcess{first, second}}
	backend := localBackend()
	backend.capture = transport
	bridge := newScrcpyBridge(Config{ScrcpyJar: jar}, backend)
	t.Cleanup(func() { _ = bridge.Stop() })
	reported := make(chan error, 1)
	bridge.SetErrorPublisher(func(err error) { reported <- err })
	if err := bridge.Start(context.Background(), streamOptions{Source: "display", ForceH264Baseline: true}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for transport.count() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("profileless retry did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !transport.options(0).ForceH264Baseline || transport.options(1).ForceH264Baseline {
		t.Fatal("retry did not remove forced H.264 profile")
	}
	select {
	case err := <-reported:
		t.Fatalf("fallback reported a premature error: %v", err)
	default:
	}
}

func TestNonCodecFailureDoesNotRetryProfile(t *testing.T) {
	jar := filepath.Join(t.TempDir(), "scrcpy-server.jar")
	if err := os.WriteFile(jar, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	process := &scriptedCaptureProcess{done: make(chan struct{}), err: errors.New("network disconnected")}
	close(process.done)
	transport := &scriptedCaptureTransport{processes: []*scriptedCaptureProcess{process}}
	backend := localBackend()
	backend.capture = transport
	bridge := newScrcpyBridge(Config{ScrcpyJar: jar}, backend)
	reported := make(chan error, 1)
	bridge.SetErrorPublisher(func(err error) { reported <- err })
	if err := bridge.Start(context.Background(), streamOptions{Source: "display", ForceH264Baseline: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reported:
	case <-time.After(time.Second):
		t.Fatal("non-codec failure was not reported")
	}
	if transport.count() != 1 {
		t.Fatal("non-codec failure retried profileless capture")
	}
}
