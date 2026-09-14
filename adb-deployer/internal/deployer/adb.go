package deployer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	adbclient "github.com/cwithw/ScrcpyCat/internal/adb"
)

type adbAPI interface {
	Ping(context.Context) error
	Devices(context.Context) ([]adbclient.Device, error)
	RequireShellV2(context.Context, string) error
	RunLegacy(context.Context, string, string, io.Writer) error
	Push(context.Context, string, io.Reader, string, os.FileMode) error
	Reverse(context.Context, string, string) error
}

func (r *runner) ensureADBServer(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	if err := r.devices.Ping(ctx); err == nil {
		return nil
	}
	host, port, err := net.SplitHostPort(r.config.ADBServer)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("external ADB server %s is unavailable", r.config.ADBServer)
	}
	// Only the server lifecycle uses the executable; device services use Go.
	// ADB treats an explicit TCP host as remote, even 127.0.0.1, and refuses
	// to start its daemon. The port-only form binds the local loopback socket.
	command := exec.CommandContext(ctx, r.config.ADBPath, "-L", "tcp:"+port, "start-server")
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("start ADB server: %w", err)
	}
	return r.devices.Ping(ctx)
}

func (r *runner) shell(ctx context.Context, serial, command string) ([]byte, error) {
	var output bytes.Buffer
	err := r.devices.RunLegacy(ctx, serial, command, &output)
	if err != nil {
		text := strings.TrimSpace(output.String())
		if len(text) > 4096 {
			text = text[len(text)-4096:]
		}
		return nil, fmt.Errorf("ADB device %s: %w: %s", serial, err, text)
	}
	return output.Bytes(), nil
}

func (r *runner) push(ctx context.Context, serial, source, destination string, mode os.FileMode) error {
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	return r.devices.Push(ctx, serial, file, destination, mode)
}
