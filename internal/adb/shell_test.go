package adb

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

func fakeServer(t *testing.T, serve func(net.Conn, string)) *Client {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				readService := func() (string, error) {
					var h [4]byte
					if _, err := io.ReadFull(conn, h[:]); err != nil {
						return "", err
					}
					n, err := strconv.ParseUint(string(h[:]), 16, 16)
					if err != nil {
						return "", err
					}
					v := make([]byte, n)
					_, err = io.ReadFull(conn, v)
					return string(v), err
				}
				request, err := readService()
				if err != nil {
					return
				}
				if request != "host:transport:phone" {
					t.Errorf("unexpected device selection: %q", request)
					return
				}
				_, _ = io.WriteString(conn, "OKAY")
				service, err := readService()
				if err != nil {
					return
				}
				_, _ = io.WriteString(conn, "OKAY")
				serve(conn, service)
			}()
		}
	}()
	c, err := New(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func shellFrame(kind byte, data []byte) []byte {
	frame := make([]byte, 5+len(data))
	frame[0] = kind
	binary.LittleEndian.PutUint32(frame[1:5], uint32(len(data)))
	copy(frame[5:], data)
	return frame
}

func TestShellV2PreservesOutputAndExitStatus(t *testing.T) {
	c := fakeServer(t, func(conn net.Conn, service string) {
		if service != "shell,v2,raw:example" {
			t.Errorf("service=%q", service)
			return
		}
		data := append(shellFrame(1, []byte("stdout\x00")), shellFrame(2, []byte("stderr"))...)
		data = append(data, shellFrame(3, []byte{7})...)
		// Split every header and body across reads.
		for _, b := range data {
			if _, err := conn.Write([]byte{b}); err != nil {
				return
			}
		}
		_, _ = io.Copy(io.Discard, conn)
	})
	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := c.Run(ctx, "phone", "example", &stdout, &stderr)
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != 7 || stdout.String() != "stdout\x00" || stderr.String() != "stderr" {
		t.Fatalf("stdout=%q stderr=%q error=%v", stdout.String(), stderr.String(), err)
	}
}

func TestPTYResizeAndCancellationCloseTheADBShell(t *testing.T) {
	received := make(chan string, 1)
	closed := make(chan struct{})
	c := fakeServer(t, func(conn net.Conn, service string) {
		if !strings.HasPrefix(service, "shell,v2,TERM=xterm-256color,pty:") {
			t.Errorf("service=%q", service)
			return
		}
		var h [5]byte
		if _, err := io.ReadFull(conn, h[:]); err != nil {
			return
		}
		data := make([]byte, binary.LittleEndian.Uint32(h[1:]))
		if _, err := io.ReadFull(conn, data); err != nil {
			return
		}
		received <- fmt.Sprintf("%d:%s", h[0], data)
		_, _ = io.Copy(io.Discard, conn)
		close(closed)
	})
	ctx, cancel := context.WithCancel(context.Background())
	s, err := c.Start(ctx, "phone", "exec sh", true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Resize(42, 120); err != nil {
		t.Fatal(err)
	}
	if got := <-received; got != "5:42x120,0x0\x00" {
		t.Fatal(got)
	}
	cancel()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("ADB transport survived cancellation")
	}
	if err = s.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestSyncPullCanBeCancelledWhileDeviceIsSilent(t *testing.T) {
	started := make(chan struct{})
	closed := make(chan struct{})
	c := fakeServer(t, func(conn net.Conn, service string) {
		if service != "sync:" {
			t.Errorf("service=%q", service)
			return
		}
		var h [8]byte
		if _, err := io.ReadFull(conn, h[:]); err != nil {
			return
		}
		if string(h[:4]) != "RECV" {
			t.Error("expected RECV")
			return
		}
		body := make([]byte, binary.LittleEndian.Uint32(h[4:]))
		if _, err := io.ReadFull(conn, body); err != nil {
			return
		}
		close(started)
		_, _ = io.Copy(io.Discard, conn)
		close(closed)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Pull(ctx, "phone", "/sdcard/test", io.Discard) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("pull did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("pull did not cancel")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Sync socket leaked")
	}
}

func TestCommandQuotesArguments(t *testing.T) {
	command, err := Command("pm", "install", "/sdcard/a 'quoted' $(literal).apk")
	if err != nil || command != "'pm' 'install' '/sdcard/a '\\''quoted'\\'' $(literal).apk'" {
		t.Fatalf("%q %v", command, err)
	}
	if _, err = Command("sh", "bad\x00argument"); err == nil {
		t.Fatal("accepted NUL")
	}
}
