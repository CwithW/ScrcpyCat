package agent

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	adbclient "github.com/cwithw/ScrcpyCat/internal/adb"
	"golang.org/x/sys/unix"
)

type commandExecutor interface {
	Execute(context.Context, string, []string, io.Writer, io.Writer) error
}

type diskUsage struct{ blocks, available, blockSize uint64 }
type metricReader interface {
	ReadMetric(context.Context, string) ([]byte, error)
	DiskUsage(context.Context) (diskUsage, error)
}

type deviceBackend struct {
	commands  commandExecutor
	files     deviceFiles
	terminals terminalOpener
	capture   captureTransport
	metrics   metricReader
	adb       *adbDevice
}

type localDevice struct{}
type adbDevice struct {
	client *adbclient.Client
	serial string
}

func localBackend() *deviceBackend {
	d := localDevice{}
	return &deviceBackend{commands: d, files: d, terminals: d, capture: d, metrics: d}
}

func newDeviceBackend(ctx context.Context, config Config) (*deviceBackend, error) {
	if config.ADBSerial == "" {
		return localBackend(), nil
	}
	c, err := adbclient.New(config.ADBServer)
	if err != nil {
		return nil, err
	}
	if err = c.RequireShellV2(ctx, config.ADBSerial); err != nil {
		return nil, err
	}
	d := &adbDevice{client: c, serial: config.ADBSerial}
	if err = d.prepareDirectory(ctx); err != nil {
		return nil, err
	}
	return &deviceBackend{commands: d, files: d, terminals: d, capture: d, metrics: d, adb: d}, nil
}

func (c *client) device() *deviceBackend {
	if c.backend != nil {
		return c.backend
	}
	return localBackend()
}

func (d localDevice) Execute(ctx context.Context, name string, args []string, stdout, stderr io.Writer) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdout, command.Stderr = stdout, stderr
	return command.Run()
}

func (d *adbDevice) Execute(ctx context.Context, name string, args []string, stdout, stderr io.Writer) error {
	command, err := adbclient.Command(name, args...)
	if err != nil {
		return err
	}
	return d.client.Run(ctx, d.serial, command, stdout, stderr)
}

func executeOutput(ctx context.Context, executor commandExecutor, name string, args ...string) (string, error) {
	var output boundedOutput
	err := executor.Execute(ctx, name, args, &output, &output)
	return output.String(), err
}

func (c *client) runCommand(ctx context.Context, name string, args ...string) (string, error) {
	return executeOutput(ctx, c.device().commands, name, args...)
}

func (d *deviceBackend) property(name string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output, err := executeOutput(ctx, d.commands, "getprop", name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(output)
}

func (d localDevice) ReadMetric(_ context.Context, path string) ([]byte, error) {
	return os.ReadFile(path)
}
func (d *adbDevice) ReadMetric(ctx context.Context, path string) ([]byte, error) {
	output, err := executeOutput(ctx, d, "cat", "--", path)
	return []byte(output), err
}

func (d localDevice) DiskUsage(_ context.Context) (diskUsage, error) {
	var value unix.Statfs_t
	err := unix.Statfs("/data", &value)
	return diskUsage{value.Blocks, value.Bavail, uint64(value.Bsize)}, err
}

func (d *adbDevice) DiskUsage(ctx context.Context) (diskUsage, error) {
	output, err := executeOutput(ctx, d, "stat", "-f", "-c", "%b %a %S", "/data")
	if err != nil {
		return diskUsage{}, err
	}
	fields := strings.Fields(output)
	if len(fields) != 3 {
		return diskUsage{}, errors.New("invalid Android filesystem statistics")
	}
	values := make([]uint64, 3)
	for i, field := range fields {
		values[i], err = strconv.ParseUint(field, 10, 64)
		if err != nil {
			return diskUsage{}, err
		}
	}
	return diskUsage{values[0], values[1], values[2]}, nil
}

func (d *adbDevice) watchConnection(ctx context.Context, cancel context.CancelFunc) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		check, stop := context.WithTimeout(ctx, 2*time.Second)
		err := d.client.Online(check, d.serial)
		stop()
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("ADB connection lost for %s: %v", d.serial, err)
			}
			cancel()
			return
		}
	}
}
