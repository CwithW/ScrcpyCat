package gadb

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"
)

// NewClientContext connects to an existing ADB server without starting one.
func NewClientContext(ctx context.Context, host string, port int) (Client, error) {
	c := Client{host: host, port: port, ctx: ctx}
	t, err := c.createTransport()
	if err != nil {
		return Client{}, err
	}
	_ = t.Close()
	return c, nil
}

// WithContext scopes every connection opened by the returned client.
func (c Client) WithContext(ctx context.Context) Client {
	c.ctx = ctx
	return c
}

// Device binds subsequent services to an explicit serial, without enumeration.
func (c Client) Device(serial string) Device {
	return Device{adbClient: c, serial: serial, attrs: map[string]string{}}
}

// OpenService returns an owned, full-duplex device service connection. Closing
// it also unregisters cancellation; shell,v2 sends SIGHUP when it disconnects.
func (d Device) OpenService(service string) (net.Conn, error) {
	t, err := d.createDeviceTransport()
	if err != nil {
		return nil, err
	}
	if err = t.Send(service); err == nil {
		err = t.VerifyResponse()
	}
	if err != nil {
		_ = t.Close()
		return nil, err
	}
	_ = t.sock.SetDeadline(time.Time{})
	return t.sock, nil
}

// HostQuery exposes framed smart-socket replies such as device features.
func (c Client) HostQuery(service string) (string, error) {
	return c.executeCommand(service)
}

type contextConn struct {
	net.Conn
	stop func() bool
	once sync.Once
}

func (c *contextConn) Close() error {
	c.once.Do(func() { c.stop() })
	return c.Conn.Close()
}

func newContextTransport(ctx context.Context, host string, port int) (transport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return transport{}, fmt.Errorf("adb transport: %w", err)
	}
	wrapped := &contextConn{Conn: conn}
	// The callback closes the underlying socket directly, so Close cannot race
	// with assignment of the cancellation callback.
	wrapped.stop = context.AfterFunc(ctx, func() { _ = conn.Close() })
	return transport{sock: wrapped, readTimeout: DefaultAdbReadTimeout}, nil
}
