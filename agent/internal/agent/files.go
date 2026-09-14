package agent

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxDeviceFileSize int64 = 2 << 30

type fileUpload struct {
	file           *os.File
	path           string
	size, uploaded int64
	checksum       string
	hash           hash.Hash
	install        bool
	finishing      bool
}

type deviceFileSession struct {
	ctx         context.Context
	cancel      context.CancelFunc
	upload      *fileUpload
	downloading bool
}

type deviceFileManager struct {
	files    deviceFiles
	commands commandExecutor
	closed   bool
	mu       sync.Mutex
	sessions map[string]*deviceFileSession
	send     func(string, map[string]any) error
}

func newDeviceFileManager(send func(string, map[string]any) error, backends ...*deviceBackend) *deviceFileManager {
	backend := localBackend()
	if len(backends) > 0 {
		backend = backends[0]
	}
	return &deviceFileManager{files: backend.files, commands: backend.commands, sessions: make(map[string]*deviceFileSession), send: send}
}

func (m *deviceFileManager) Open(clientID string) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	if m.sessions[clientID] == nil {
		ctx, cancel := context.WithCancel(context.Background())
		m.sessions[clientID] = &deviceFileSession{ctx: ctx, cancel: cancel}
	}
	m.mu.Unlock()
	_ = m.send(clientID, map[string]any{"type": "ready"})
}

func (m *deviceFileManager) CloseClient(clientID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if session := m.sessions[clientID]; session != nil {
		session.cancel()
		discardUpload(session.upload)
		delete(m.sessions, clientID)
	}
}

func (m *deviceFileManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	for id, session := range m.sessions {
		session.cancel()
		discardUpload(session.upload)
		delete(m.sessions, id)
	}
}

func discardUpload(upload *fileUpload) {
	if upload != nil {
		_ = upload.file.Close()
		_ = os.Remove(upload.file.Name())
	}
}

func deviceFilePath(raw string) (string, error) {
	if !filepath.IsAbs(raw) || strings.ContainsRune(raw, 0) {
		return "", errors.New("file path must be absolute")
	}
	return filepath.Clean(raw), nil
}

func (m *deviceFileManager) Command(clientID string, command map[string]any) {
	m.mu.Lock()
	session := m.sessions[clientID]
	m.mu.Unlock()
	if session == nil {
		return
	}
	operationCtx, cancel := context.WithTimeout(session.ctx, 15*time.Second)
	defer cancel()

	kind := stringField(command, "type")
	path, err := deviceFilePath(stringField(command, "path"))
	replyKind := kind + "_reply"
	switch kind {
	case "upload_start":
		replyKind = "upload_reply"
	case "download_start":
		replyKind = "download_reply"
	case "install_apk":
		replyKind = "install_status"
	}
	reply := map[string]any{"type": replyKind, "path": path, "request_id": command["request_id"], "success": false}
	if err == nil {
		switch kind {
		case "list":
			var entries []os.FileInfo
			entries, err = m.files.ListFiles(operationCtx, path)
			files := make([]map[string]any, 0, len(entries))
			if len(entries) > 20000 {
				err = errors.New("directory contains too many entries")
			}
			if err == nil {
				for _, info := range entries {
					fullPath := filepath.Join(path, info.Name())
					files = append(files, map[string]any{"name": info.Name(), "path": fullPath, "size": info.Size(), "is_dir": info.IsDir(), "mod_time": info.ModTime().Unix(), "mode": info.Mode().String()})
				}
			}
			reply["files"] = files
		case "mkdir":
			err = m.files.Mkdir(operationCtx, path, false)
		case "delete":
			switch path {
			case "/", "/sdcard", "/storage", "/storage/emulated", "/storage/emulated/0", "/data", "/data/local", "/data/local/tmp":
				err = errors.New("cannot delete a storage root")
			default:
				// RemoveAll does not traverse symlinks.
				err = m.files.Remove(operationCtx, path)
			}
		case "upload_start":
			err = m.startUpload(clientID, session, path, command)
			if err == nil {
				return
			}
		case "download_start":
			m.mu.Lock()
			busy := session.downloading
			if !busy {
				session.downloading = true
			}
			m.mu.Unlock()
			if busy {
				err = errors.New("a download is already active")
			} else {
				go m.download(clientID, session, path, command["request_id"])
				return
			}
		case "install_apk":
			go m.install(clientID, session, path, command["request_id"])
			return
		default:
			err = errors.New("unsupported file operation")
		}
	}
	reply["success"] = err == nil
	if err != nil {
		reply["error"] = err.Error()
		reply["message"] = err.Error()
		reply["status"] = "error"
	}
	_ = m.send(clientID, reply)
}

func (m *deviceFileManager) startUpload(clientID string, session *deviceFileSession, path string, command map[string]any) error {
	size, err := uint64FieldDefault(command, "size", ^uint64(0))
	checksum := strings.ToLower(stringField(command, "sha256"))
	decoded, hashErr := hex.DecodeString(checksum)
	if err != nil || size > uint64(maxDeviceFileSize) {
		return errors.New("invalid file size")
	}
	if hashErr != nil || len(decoded) != sha256.Size {
		return errors.New("SHA-256 is required")
	}
	file, err := m.files.StageUpload(path)
	if err != nil {
		return err
	}
	upload := &fileUpload{file: file, path: path, size: int64(size), checksum: checksum, hash: sha256.New()}
	upload.install, _ = command["install_on_finish"].(bool)
	m.mu.Lock()
	if m.sessions[clientID] != session || session.ctx.Err() != nil || session.upload != nil {
		m.mu.Unlock()
		discardUpload(upload)
		return errors.New("file session is busy or closed")
	}
	session.upload = upload
	m.mu.Unlock()
	_ = m.send(clientID, map[string]any{"type": "upload_reply", "path": path, "success": true})
	if size == 0 {
		m.Chunk(clientID, nil)
	}
	return nil
}

func (m *deviceFileManager) Chunk(clientID string, data []byte) {
	m.mu.Lock()
	session := m.sessions[clientID]
	if session == nil || session.upload == nil || session.upload.finishing {
		m.mu.Unlock()
		return
	}
	upload := session.upload
	var err error
	if len(data) > 64<<10 || upload.uploaded+int64(len(data)) > upload.size {
		err = errors.New("upload exceeds declared size")
	} else {
		var count int
		count, err = upload.file.Write(data)
		if err == nil && count != len(data) {
			err = io.ErrShortWrite
		}
		_, _ = upload.hash.Write(data[:count])
		upload.uploaded += int64(count)
	}
	finished := err != nil || upload.uploaded == upload.size
	if finished {
		upload.finishing = true
		if err == nil && hex.EncodeToString(upload.hash.Sum(nil)) != upload.checksum {
			err = errors.New("file checksum mismatch")
		}
		if err == nil {
			err = upload.file.Chmod(0644)
		}
		if err == nil {
			err = upload.file.Sync()
		}
		closeErr := upload.file.Close()
		if err == nil {
			err = closeErr
		}
	}
	uploaded := upload.uploaded
	m.mu.Unlock()
	if finished {
		// USB I/O must not hold the manager lock: CloseClient needs to cancel a
		// blocked transfer while other devices and media sessions keep running.
		if err == nil {
			err = m.files.CommitUpload(session.ctx, upload.file.Name(), upload.path)
		}
		_ = os.Remove(upload.file.Name())
		m.mu.Lock()
		if session.upload == upload {
			session.upload = nil
		}
		m.mu.Unlock()
	}
	reply := map[string]any{"type": "upload_ack", "path": upload.path, "uploaded": uploaded, "finished": finished, "success": err == nil}
	if err != nil {
		reply["error"] = err.Error()
	}
	_ = m.send(clientID, reply)
	if finished && err == nil && upload.install {
		go m.install(clientID, session, upload.path, nil)
	}
}

func (m *deviceFileManager) download(clientID string, session *deviceFileSession, path string, requestID any) {
	defer func() { m.mu.Lock(); session.downloading = false; m.mu.Unlock() }()
	reply := map[string]any{"type": "download_reply", "path": path, "request_id": requestID, "success": false}
	file, info, err := m.files.OpenFile(session.ctx, path)
	if err == nil {
		defer file.Close()
		if !info.Mode().IsRegular() || info.Size() > maxDeviceFileSize {
			err = errors.New("only regular files up to 2 GiB can be downloaded")
		}
		if err == nil {
			reply["size"] = info.Size()
		}
	}
	if err != nil {
		reply["error"] = err.Error()
		_ = m.send(clientID, reply)
		return
	}
	reply["success"] = true
	if m.send(clientID, reply) != nil {
		return
	}
	buffer := make([]byte, 16<<10)
	remaining := reply["size"].(int64)
	for remaining > 0 && session.ctx.Err() == nil {
		chunkSize := len(buffer)
		if remaining < int64(chunkSize) {
			chunkSize = int(remaining)
		}
		count, readErr := io.ReadFull(file, buffer[:chunkSize])
		if count > 0 {
			if m.send(clientID, map[string]any{"type": "download_chunk", "request_id": requestID, "data": base64.StdEncoding.EncodeToString(buffer[:count])}) != nil {
				return
			}
			remaining -= int64(count)
		}
		if readErr != nil {
			_ = m.send(clientID, map[string]any{"type": "download_reply", "request_id": requestID, "path": path, "success": false, "error": readErr.Error()})
			return
		}
	}
}

func (m *deviceFileManager) install(clientID string, session *deviceFileSession, path string, requestID any) {
	status := func(state, message string) {
		_ = m.send(clientID, map[string]any{"type": "install_status", "path": path, "request_id": requestID, "status": state, "message": message})
	}
	if strings.ToLower(filepath.Ext(path)) != ".apk" {
		status("error", "only APK files can be installed")
		return
	}
	status("installing", "Installing APK")
	ctx, cancel := context.WithTimeout(session.ctx, 2*time.Minute)
	defer cancel()
	output, err := executeOutput(ctx, m.commands, "pm", "install", "-r", path)
	if err != nil {
		status("error", fmt.Sprintf("%s: %v", output, err))
		return
	}
	status("success", output)
}
