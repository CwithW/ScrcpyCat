package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestDeviceFilesUploadDownloadAndCleanup(t *testing.T) {
	messages := make(chan map[string]any, 128)
	manager := newDeviceFileManager(func(_ string, data map[string]any) error { messages <- data; return nil })
	defer manager.Close()
	wait := func(kind string) map[string]any {
		t.Helper()
		for {
			select {
			case message := <-messages:
				if message["type"] == kind {
					return message
				}
			case <-time.After(time.Second):
				t.Fatalf("missing %s", kind)
				return nil
			}
		}
	}
	manager.Open("client")
	dir := t.TempDir()
	path := filepath.Join(dir, "roundtrip.txt")
	content := bytes.Repeat([]byte("中文 file data\n"), 4096)
	manager.Command("client", map[string]any{"type": "upload_start", "path": path, "size": len(content), "sha256": fmt.Sprintf("%x", sha256.Sum256(content))})
	if reply := wait("upload_reply"); reply["success"] != true {
		t.Fatal(reply)
	}
	for offset := 0; offset < len(content); offset += 16384 {
		end := offset + 16384
		if end > len(content) {
			end = len(content)
		}
		manager.Chunk("client", content[offset:end])
	}
	stored, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(stored, content) {
		t.Fatalf("upload: %v", err)
	}
	manager.Command("client", map[string]any{"type": "download_start", "path": path, "request_id": "request"})
	if reply := wait("download_reply"); reply["success"] != true || reply["size"] != int64(len(content)) {
		t.Fatal(reply)
	}
	var downloaded []byte
	for len(downloaded) < len(content) {
		chunk := wait("download_chunk")
		data, err := base64.StdEncoding.DecodeString(chunk["data"].(string))
		if err != nil {
			t.Fatal(err)
		}
		downloaded = append(downloaded, data...)
	}
	if !bytes.Equal(downloaded, content) {
		t.Fatal("download mismatch")
	}

	manager.Command("client", map[string]any{"type": "upload_start", "path": path, "size": 3, "sha256": fmt.Sprintf("%x", sha256.Sum256([]byte("bad")))})
	wait("upload_reply")
	manager.Chunk("client", []byte("new"))
	for reply := wait("upload_ack"); ; reply = wait("upload_ack") {
		if reply["finished"] == true {
			if reply["success"] != false {
				t.Fatal(reply)
			}
			break
		}
	}
	stored, _ = os.ReadFile(path)
	if !bytes.Equal(stored, content) {
		t.Fatal("checksum failure replaced destination")
	}

	manager.Command("client", map[string]any{"type": "upload_start", "path": path, "size": 3, "sha256": fmt.Sprintf("%x", sha256.Sum256([]byte("new")))})
	wait("upload_reply")
	manager.Chunk("client", []byte("n"))
	manager.CloseClient("client")
	staging, _ := filepath.Glob(filepath.Join(dir, ".scrcpycat-upload-*"))
	if len(staging) != 0 {
		t.Fatal("staging files remain after disconnect")
	}
}

func TestEmptyDeviceFileAndStorageRoot(t *testing.T) {
	var last map[string]any
	manager := newDeviceFileManager(func(_ string, data map[string]any) error { last = data; return nil })
	defer manager.Close()
	manager.Open("client")
	path := filepath.Join(t.TempDir(), "empty")
	manager.Command("client", map[string]any{"type": "upload_start", "path": path, "size": 0, "sha256": fmt.Sprintf("%x", sha256.Sum256(nil))})
	if last["type"] != "upload_ack" || last["finished"] != true || last["success"] != true {
		t.Fatal(last)
	}
	if info, err := os.Stat(path); err != nil || info.Size() != 0 {
		t.Fatal(err)
	}
	manager.Command("client", map[string]any{"type": "delete", "path": "/"})
	if last["success"] != false {
		t.Fatal("storage root deletion allowed")
	}
}

type blockedUploadFiles struct {
	localDevice
	started chan struct{}
}

func (f blockedUploadFiles) CommitUpload(ctx context.Context, _, _ string) error {
	close(f.started)
	<-ctx.Done()
	return ctx.Err()
}

func TestClosingFileSessionCancelsBlockedUSBCommit(t *testing.T) {
	backend := localBackend()
	started := make(chan struct{})
	backend.files = blockedUploadFiles{started: started}
	manager := newDeviceFileManager(func(string, map[string]any) error { return nil }, backend)
	defer manager.Close()
	manager.Open("client")
	directory := t.TempDir()
	path := filepath.Join(directory, "file")
	manager.Command("client", map[string]any{"type": "upload_start", "path": path, "size": 1, "sha256": fmt.Sprintf("%x", sha256.Sum256([]byte("x")))})
	done := make(chan struct{})
	go func() { defer close(done); manager.Chunk("client", []byte("x")) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("commit did not start")
	}
	closed := make(chan struct{})
	go func() { defer close(closed); manager.CloseClient("client") }()
	for _, signal := range []chan struct{}{closed, done} {
		select {
		case <-signal:
		case <-time.After(time.Second):
			t.Fatal("USB commit prevented session cancellation")
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("canceled upload left a staging file: %v %v", entries, err)
	}
}

func TestConcurrentUploadAcknowledgementsUseTheirOwnProgress(t *testing.T) {
	const count = 100
	acks := make(chan int64, count)
	manager := newDeviceFileManager(func(_ string, reply map[string]any) error {
		if reply["type"] == "upload_ack" {
			acks <- reply["uploaded"].(int64)
		}
		return nil
	})
	defer manager.Close()
	manager.Open("client")
	path := filepath.Join(t.TempDir(), "file")
	data := bytes.Repeat([]byte("x"), count)
	manager.Command("client", map[string]any{"type": "upload_start", "path": path, "size": count, "sha256": fmt.Sprintf("%x", sha256.Sum256(data))})
	var workers sync.WaitGroup
	for i := 0; i < count; i++ {
		workers.Add(1)
		go func() { defer workers.Done(); manager.Chunk("client", []byte("x")) }()
	}
	workers.Wait()
	close(acks)
	seen := make(map[int64]bool)
	for uploaded := range acks {
		if seen[uploaded] {
			t.Fatalf("two chunks acknowledged the same progress: %d", uploaded)
		}
		seen[uploaded] = true
	}
	stored, err := os.ReadFile(path)
	if len(seen) != count || err != nil || !bytes.Equal(stored, data) {
		t.Fatalf("acks=%d stored=%d error=%v", len(seen), len(stored), err)
	}
}
