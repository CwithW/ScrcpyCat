package agent

import (
	"context"
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
