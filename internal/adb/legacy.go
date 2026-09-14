package adb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// RunLegacy is only for deploying the standalone Android Agent, including to
// devices predating shell_v2. Capture and interactive host sessions use v2.
// A random trailer preserves exit status without trusting command output.
func (c *Client) RunLegacy(ctx context.Context, serial, command string, output io.Writer) error {
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	marker := "\nSCRCPYCAT_EXIT_" + hex.EncodeToString(nonce[:]) + ":"
	wrapped := "( " + command + "\n); result=$?; printf " + Quote(marker+"%d\n") + " \"$result\""
	conn, err := c.Open(ctx, serial, "shell:"+wrapped)
	if err != nil {
		return err
	}
	defer conn.Close()
	raw, err := io.ReadAll(io.LimitReader(conn, (8<<20)+1))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	if len(raw) > 8<<20 {
		return errors.New("ADB deployment command output exceeds limit")
	}
	index := strings.LastIndex(string(raw), marker)
	if index < 0 {
		return errors.New("ADB deployment shell ended without exit status")
	}
	status, err := strconv.Atoi(strings.TrimSpace(string(raw[index+len(marker):])))
	if err != nil || status < 0 || status > 255 {
		return errors.New("invalid ADB deployment exit status")
	}
	if output != nil {
		if _, err = output.Write(raw[:index]); err != nil {
			return err
		}
	}
	if status != 0 {
		return &ExitError{Code: status}
	}
	return nil
}

func (c *Client) Address() string { return fmt.Sprintf("%s:%d", c.host, c.port) }
