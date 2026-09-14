// Package adb connects to the official ADB server using pure Go. It never owns
// USB directly, and every operation is bound to an explicit device serial.
package adb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/electricbubble/gadb"
)

const DefaultServer = "127.0.0.1:5037"

type Client struct {
	host string
	port int
}

type Device struct {
	Serial string
	USB    bool
}

func New(address string) (*Client, error) {
	if address == "" {
		address = DefaultServer
	}
	host, rawPort, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("ADB server address: %w", err)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 || host == "" {
		return nil, errors.New("invalid ADB server address")
	}
	return &Client{host: host, port: port}, nil
}

func (c *Client) connect(ctx context.Context) (gadb.Client, error) {
	return gadb.NewClientContext(ctx, c.host, c.port)
}

func (c *Client) Ping(ctx context.Context) error {
	client, err := c.connect(ctx)
	if err != nil {
		return err
	}
	_, err = client.ServerVersion()
	return err
}

func (c *Client) device(ctx context.Context, serial string) (gadb.Device, error) {
	if strings.TrimSpace(serial) == "" || strings.ContainsRune(serial, 0) {
		return gadb.Device{}, errors.New("ADB serial is required")
	}
	client, err := c.connect(ctx)
	if err != nil {
		return gadb.Device{}, err
	}
	return client.Device(serial), nil
}

func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}
	devices, err := client.DeviceList()
	if err != nil {
		return nil, err
	}
	result := make([]Device, 0, len(devices))
	for _, device := range devices {
		state, stateErr := device.State()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if stateErr != nil || state != gadb.StateOnline {
			continue
		}
		usb, _ := device.IsUsb()
		result = append(result, Device{Serial: device.Serial(), USB: usb})
	}
	return result, nil
}

func (c *Client) Online(ctx context.Context, serial string) error {
	d, err := c.device(ctx, serial)
	if err != nil {
		return err
	}
	state, err := d.State()
	if err != nil {
		return err
	}
	if state != gadb.StateOnline {
		return fmt.Errorf("ADB device %s is %s", serial, state)
	}
	return nil
}

func (c *Client) RequireShellV2(ctx context.Context, serial string) error {
	client, err := c.connect(ctx)
	if err != nil {
		return err
	}
	features, err := client.HostQuery("host-serial:" + serial + ":features")
	if err != nil {
		return err
	}
	for _, feature := range strings.Split(strings.TrimSpace(features), ",") {
		if feature == "shell_v2" {
			return nil
		}
	}
	return errors.New("device does not support ADB Shell v2; use device deployment mode")
}

func (c *Client) Open(ctx context.Context, serial, service string) (net.Conn, error) {
	d, err := c.device(ctx, serial)
	if err != nil {
		return nil, err
	}
	return d.OpenService(service)
}

func (c *Client) Push(ctx context.Context, serial string, source io.Reader, destination string, mode os.FileMode) error {
	d, err := c.device(ctx, serial)
	if err != nil {
		return err
	}
	err = d.Push(source, destination, time.Now(), mode.Perm())
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (c *Client) Pull(ctx context.Context, serial, source string, destination io.Writer) error {
	d, err := c.device(ctx, serial)
	if err != nil {
		return err
	}
	err = d.Pull(source, destination)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (c *Client) List(ctx context.Context, serial, path string) ([]gadb.DeviceFileInfo, error) {
	d, err := c.device(ctx, serial)
	if err != nil {
		return nil, err
	}
	entries, err := d.List(path)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return entries, err
}

func (c *Client) Reverse(ctx context.Context, serial string, port string) error {
	conn, err := c.Open(ctx, serial, "reverse:forward:tcp:"+port+";tcp:"+port)
	if err != nil {
		return err
	}
	return conn.Close()
}

func Quote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

func Command(name string, args ...string) (string, error) {
	words := append([]string{name}, args...)
	for i, word := range words {
		if strings.ContainsRune(word, 0) {
			return "", errors.New("command contains a NUL byte")
		}
		words[i] = Quote(word)
	}
	return strings.Join(words, " "), nil
}
