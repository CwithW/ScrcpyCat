package agent

import (
	"context"
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
	adbclient "github.com/cwithw/ScrcpyCat/internal/adb"
)

type terminalHandle interface {
	io.ReadWriteCloser
	Resize(uint16, uint16) error
}
type terminalOpener interface {
	OpenTerminal(context.Context, uint16, uint16) (terminalHandle, error)
}

type localTerminal struct {
	*os.File
	command *exec.Cmd
	once    sync.Once
}

func (t *localTerminal) Resize(rows, cols uint16) error {
	return pty.Setsize(t.File, &pty.Winsize{Rows: rows, Cols: cols})
}
func (t *localTerminal) Close() error {
	t.once.Do(func() { _ = t.File.Close(); _ = t.command.Process.Kill(); _ = t.command.Wait() })
	return nil
}
func (d localDevice) OpenTerminal(_ context.Context, rows, cols uint16) (terminalHandle, error) {
	command := exec.Command("sh")
	command.Env = append(os.Environ(), "TERM=xterm-256color")
	file, err := pty.StartWithSize(command, &pty.Winsize{Rows: rows, Cols: cols})
	if err != nil {
		return nil, err
	}
	return &localTerminal{File: file, command: command}, nil
}

type adbTerminal struct {
	reader  *io.PipeReader
	writer  *io.PipeWriter
	session *adbclient.Session
	cancel  context.CancelFunc
	once    sync.Once
}

func (t *adbTerminal) Read(p []byte) (int, error)     { return t.reader.Read(p) }
func (t *adbTerminal) Write(p []byte) (int, error)    { return t.session.Write(p) }
func (t *adbTerminal) Resize(rows, cols uint16) error { return t.session.Resize(rows, cols) }
func (t *adbTerminal) Close() error {
	t.once.Do(func() { t.cancel(); _ = t.session.Close(); _ = t.reader.Close(); _ = t.writer.Close() })
	return nil
}
func (d *adbDevice) OpenTerminal(parent context.Context, rows, cols uint16) (terminalHandle, error) {
	ctx, cancel := context.WithCancel(parent)
	reader, writer := io.Pipe()
	session, err := d.client.Start(ctx, d.serial, "exec sh", true, writer, writer)
	if err != nil {
		cancel()
		_ = reader.Close()
		_ = writer.Close()
		return nil, err
	}
	terminal := &adbTerminal{reader: reader, writer: writer, session: session, cancel: cancel}
	stop := context.AfterFunc(ctx, func() { _ = writer.CloseWithError(ctx.Err()) })
	go func() { err := session.Wait(); stop(); _ = writer.CloseWithError(err) }()
	if err = session.Resize(rows, cols); err != nil {
		_ = terminal.Close()
		return nil, err
	}
	return terminal, nil
}
