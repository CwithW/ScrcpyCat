package agent

import (
	"bytes"
	"encoding/base64"
	"testing"
	"time"
)

func TestTerminalInputExecutesBrowserBase64(t *testing.T) {
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding} {
		name := "padded-browser"
		if encoding == base64.RawStdEncoding {
			name = "unpadded-legacy"
		}
		t.Run(name, func(t *testing.T) {
			manager := newTerminalManager(nil)
			t.Cleanup(manager.CloseAll)
			output := make(chan []byte, 64)
			if err := manager.OpenWebRTC("client", "session", 24, 80, func(data []byte) { output <- data }, nil); err != nil {
				t.Fatal(err)
			}
			command := []byte("printf 'scrcpycat_%s\\n' pty_ok\r")
			if len(command)%3 == 0 {
				command = append([]byte(" "), command...)
			}
			manager.Input("client", "session", encoding.EncodeToString(command))
			waitTerminalOutput(t, output, []byte("scrcpycat_pty_ok"))
			manager.Resize("client", "session", 33, 99)
			manager.Input("client", "session", encoding.EncodeToString([]byte("stty size\r")))
			waitTerminalOutput(t, output, []byte("33 99"))
		})
	}
}

func waitTerminalOutput(t *testing.T, output <-chan []byte, expected []byte) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	var received []byte
	for {
		select {
		case data := <-output:
			received = append(received, data...)
			if bytes.Contains(received, expected) {
				return
			}
		case <-deadline.C:
			t.Fatalf("terminal never returned %q; output: %q", expected, received)
		}
	}
}
