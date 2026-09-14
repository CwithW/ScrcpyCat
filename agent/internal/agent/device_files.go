package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type fileEntry struct {
	name     string
	size     int64
	mode     os.FileMode
	modified time.Time
}

func (f fileEntry) Name() string       { return f.name }
func (f fileEntry) Size() int64        { return f.size }
func (f fileEntry) Mode() os.FileMode  { return f.mode }
func (f fileEntry) ModTime() time.Time { return f.modified }
func (f fileEntry) IsDir() bool        { return f.mode.IsDir() }
func (f fileEntry) Sys() any           { return nil }

type deviceFiles interface {
	ListFiles(context.Context, string) ([]os.FileInfo, error)
	OpenFile(context.Context, string) (io.ReadCloser, os.FileInfo, error)
	Mkdir(context.Context, string, bool) error
	Remove(context.Context, string) error
	StageUpload(string) (*os.File, error)
	CommitUpload(context.Context, string, string) error
}

func (d localDevice) ListFiles(_ context.Context, path string) ([]os.FileInfo, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	if len(entries) > 20000 {
		return nil, errors.New("directory contains too many entries")
	}
	result := make([]os.FileInfo, 0, len(entries))
	for _, entry := range entries {
		info, err := os.Stat(filepath.Join(path, entry.Name()))
		if err != nil {
			info, err = entry.Info()
		}
		if err == nil {
			result = append(result, info)
		}
	}
	return result, nil
}

func (d localDevice) OpenFile(_ context.Context, path string) (io.ReadCloser, os.FileInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	return file, info, nil
}

func (d localDevice) Mkdir(_ context.Context, path string, all bool) error {
	if all {
		return os.MkdirAll(path, 0755)
	}
	return os.Mkdir(path, 0755)
}
func (d localDevice) Remove(_ context.Context, path string) error { return os.RemoveAll(path) }
func (d localDevice) StageUpload(path string) (*os.File, error) {
	return os.CreateTemp(filepath.Dir(path), ".scrcpycat-upload-*")
}
func (d localDevice) CommitUpload(_ context.Context, stage, destination string) error {
	err := os.Rename(stage, destination)
	if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".scrcpycat-upload-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	_ = temporary.Close()
	defer os.Remove(name)
	if err = copyFile(stage, name); err != nil {
		return err
	}
	if err = os.Chmod(name, 0644); err != nil {
		return err
	}
	return os.Rename(name, destination)
}

func androidFileMode(raw uint64) os.FileMode {
	mode := os.FileMode(raw & 0777)
	switch raw & 0170000 {
	case 0040000:
		mode |= os.ModeDir
	case 0120000:
		mode |= os.ModeSymlink
	case 0010000:
		mode |= os.ModeNamedPipe
	case 0140000:
		mode |= os.ModeSocket
	case 0020000:
		mode |= os.ModeDevice | os.ModeCharDevice
	case 0060000:
		mode |= os.ModeDevice
	}
	if raw&04000 != 0 {
		mode |= os.ModeSetuid
	}
	if raw&02000 != 0 {
		mode |= os.ModeSetgid
	}
	if raw&01000 != 0 {
		mode |= os.ModeSticky
	}
	return mode
}

func (d *adbDevice) stat(ctx context.Context, path string) (os.FileInfo, error) {
	output, err := executeOutput(ctx, d, "stat", "-L", "-c", "%f %s %Y", "--", path)
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(output)
	if len(fields) != 3 {
		return nil, errors.New("invalid Android file metadata")
	}
	mode, e1 := strconv.ParseUint(fields[0], 16, 64)
	size, e2 := strconv.ParseInt(fields[1], 10, 64)
	modified, e3 := strconv.ParseInt(fields[2], 10, 64)
	if err = errors.Join(e1, e2, e3); err != nil {
		return nil, err
	}
	return fileEntry{filepath.Base(path), size, androidFileMode(mode), time.Unix(modified, 0)}, nil
}

func (d *adbDevice) ListFiles(ctx context.Context, path string) ([]os.FileInfo, error) {
	entries, err := d.client.List(ctx, d.serial, path)
	if err != nil {
		return nil, err
	}
	if len(entries) > 20000 {
		return nil, errors.New("directory contains too many entries")
	}
	result := make([]os.FileInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.Name == "." || entry.Name == ".." {
			continue
		}
		var info os.FileInfo = fileEntry{entry.Name, int64(entry.Size), androidFileMode(uint64(entry.Mode)), entry.LastModified}
		if info.Mode()&os.ModeSymlink != 0 {
			if target, err := d.stat(ctx, filepath.Join(path, entry.Name)); err == nil {
				info = target
			}
		}
		result = append(result, info)
	}
	return result, nil
}

type cancelReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (r *cancelReadCloser) Close() error { r.cancel(); return r.ReadCloser.Close() }

func (d *adbDevice) OpenFile(parent context.Context, path string) (io.ReadCloser, os.FileInfo, error) {
	info, err := d.stat(parent, path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, errors.New("only regular files can be downloaded")
	}
	ctx, cancel := context.WithCancel(parent)
	reader, writer := io.Pipe()
	stop := context.AfterFunc(ctx, func() { _ = writer.CloseWithError(ctx.Err()) })
	go func() { err := d.client.Pull(ctx, d.serial, path, writer); stop(); _ = writer.CloseWithError(err) }()
	return &cancelReadCloser{reader, cancel}, info, nil
}

func (d *adbDevice) Mkdir(ctx context.Context, path string, all bool) error {
	args := []string{"-m", "0755"}
	if all {
		args = append(args, "-p")
	}
	args = append(args, "--", path)
	_, err := executeOutput(ctx, d, "mkdir", args...)
	return err
}
func (d *adbDevice) Remove(ctx context.Context, path string) error {
	_, err := executeOutput(ctx, d, "rm", "-rf", "--", path)
	return err
}

func (d *adbDevice) StageUpload(_ string) (*os.File, error) {
	// Browser paths always name Android files. Only this generated staging path
	// is local to the Linux Agent; it is never chosen by a browser request.
	return os.CreateTemp("", "scrcpycat-upload-*")
}

func (d *adbDevice) CommitUpload(ctx context.Context, stage, destination string) error {
	file, err := os.Open(stage)
	if err != nil {
		return err
	}
	defer file.Close()
	remote, err := executeOutput(ctx, d, "mktemp", filepath.Join(filepath.Dir(destination), ".scrcpycat-upload-XXXXXXXX"))
	if err != nil {
		return err
	}
	remote = strings.TrimSpace(remote)
	if remote == "" || filepath.Dir(remote) != filepath.Dir(destination) {
		return errors.New("invalid Android upload staging path")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = executeOutput(cleanup, d, "rm", "-f", "--", remote)
	}()
	if err = d.client.Push(ctx, d.serial, file, remote, 0644); err != nil {
		return err
	}
	if _, err = executeOutput(ctx, d, "mv", "-fT", "--", remote, destination); err != nil {
		return fmt.Errorf("commit Android upload: %w", err)
	}
	return nil
}
