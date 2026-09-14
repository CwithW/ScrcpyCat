package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

var errAgentQuit = errors.New("agent stop requested")

func numberProperty(name string) int { value, _ := strconv.Atoi(property(name)); return value }

func isAgentProcess(rawPID string) bool {
	pid, err := strconv.Atoi(rawPID)
	if err != nil || pid < 2 {
		return false
	}
	self, err := os.Executable()
	if err != nil {
		return false
	}
	other, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	if err != nil {
		return false
	}
	return strings.TrimSuffix(other, " (deleted)") == strings.TrimSuffix(self, " (deleted)")
}

func acquireInstance(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("agent instance is already locked: %w", err)
	}
	fail := func(err error) (func(), error) { _ = lock.Close(); return nil, err }
	// The held flock is authoritative. A stale PID can be reused by a different
	// device's Linux Agent after a container restart.
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0600); err != nil {
		return fail(err)
	}
	return func() { releaseInstance(path); _ = unix.Flock(int(lock.Fd()), unix.LOCK_UN); _ = lock.Close() }, nil
}
