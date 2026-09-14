package adb

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
)

const maxShellPacket = 1 << 20

type ExitError struct{ Code int }

func (e *ExitError) Error() string {
	return fmt.Sprintf("Android command exited with status %d", e.Code)
}

// Session owns a Shell v2 transport. It must remain open for the lifetime of
// app_process: adbd sends SIGHUP to the foreground process when it disconnects.
type Session struct {
	conn      net.Conn
	ctx       context.Context
	writeMu   sync.Mutex
	closeOnce sync.Once
	done      chan struct{}
	err       error
}

func (c *Client) Start(ctx context.Context, serial, command string, pty bool, stdout, stderr io.Writer) (*Session, error) {
	service := "shell,v2,raw:"
	if pty {
		service = "shell,v2,TERM=xterm-256color,pty:"
	}
	conn, err := c.Open(ctx, serial, service+command)
	if err != nil {
		return nil, err
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	s := &Session{conn: conn, ctx: ctx, done: make(chan struct{})}
	go func() {
		s.err = s.read(stdout, stderr)
		if ctx.Err() != nil {
			s.err = ctx.Err()
		}
		_ = s.Close()
		close(s.done)
	}()
	return s, nil
}

func (s *Session) read(stdout, stderr io.Writer) error {
	for {
		var header [5]byte
		if _, err := io.ReadFull(s.conn, header[:]); err != nil {
			return fmt.Errorf("ADB shell ended without exit status: %w", err)
		}
		size := binary.LittleEndian.Uint32(header[1:])
		if size > maxShellPacket {
			return errors.New("ADB shell packet exceeds limit")
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(s.conn, data); err != nil {
			return err
		}
		switch header[0] {
		case 1, 2:
			writer := stdout
			if header[0] == 2 {
				writer = stderr
			}
			if n, err := writer.Write(data); err != nil {
				return err
			} else if n != len(data) {
				return io.ErrShortWrite
			}
		case 3:
			if len(data) != 1 && len(data) != 4 {
				return errors.New("invalid ADB shell exit status")
			}
			code := int(data[0])
			if len(data) == 4 {
				code = int(binary.LittleEndian.Uint32(data))
			}
			if code != 0 {
				return &ExitError{Code: code}
			}
			return nil
		default:
			return fmt.Errorf("unexpected ADB shell packet %d", header[0])
		}
	}
}

func (s *Session) send(kind byte, data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.ctx.Err() != nil {
		return s.ctx.Err()
	}
	var header [5]byte
	header[0] = kind
	binary.LittleEndian.PutUint32(header[1:], uint32(len(data)))
	if _, err := io.Copy(s.conn, io.MultiReader(bytes.NewReader(header[:]), bytes.NewReader(data))); err != nil {
		return err
	}
	return nil
}

func (s *Session) Write(data []byte) (int, error) {
	written := 0
	for len(data) > 0 {
		n := min(len(data), 64<<10)
		if err := s.send(0, data[:n]); err != nil {
			return written, err
		}
		written += n
		data = data[n:]
	}
	return written, nil
}

func (s *Session) Resize(rows, cols uint16) error {
	if rows == 0 || cols == 0 {
		return errors.New("terminal dimensions must be positive")
	}
	return s.send(5, []byte(fmt.Sprintf("%dx%d,0x0\x00", rows, cols)))
}

func (s *Session) CloseStdin() error     { return s.send(4, nil) }
func (s *Session) Done() <-chan struct{} { return s.done }
func (s *Session) Wait() error           { <-s.done; return s.err }
func (s *Session) Close() error {
	var err error
	s.closeOnce.Do(func() { err = s.conn.Close() })
	return err
}

func (c *Client) Run(ctx context.Context, serial, command string, stdout, stderr io.Writer) error {
	session, err := c.Start(ctx, serial, command, false, stdout, stderr)
	if err != nil {
		return err
	}
	defer session.Close()
	if err := session.CloseStdin(); err != nil {
		return err
	}
	return session.Wait()
}
