package deployer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type startingADB struct {
	*fakeADB
	pings int
}

func (a *startingADB) Ping(context.Context) error {
	a.pings++
	if a.pings == 1 {
		return errors.New("connection refused")
	}
	return nil
}

func TestADBServerStartupUsesLocalPortOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "adb")
	// Official ADB rejects an explicit host in -L when starting a daemon.
	script := "#!/bin/sh\n[ \"$#\" = 3 ] && [ \"$1\" = -L ] && [ \"$2\" = tcp:15037 ] && [ \"$3\" = start-server ]\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	devices := &startingADB{fakeADB: &fakeADB{}}
	r := &runner{config: Config{ADBPath: path, ADBServer: "127.0.0.1:15037"}, devices: devices}
	if err := r.ensureADBServer(context.Background()); err != nil {
		t.Fatal(err)
	}
	if devices.pings != 2 {
		t.Fatal("new ADB server was not checked")
	}
}

func TestExternalADBServerIsNeverStartedLocally(t *testing.T) {
	r := &runner{config: Config{ADBPath: "/missing/adb", ADBServer: "192.0.2.10:5037"}, devices: &startingADB{fakeADB: &fakeADB{}}}
	if err := r.ensureADBServer(context.Background()); err == nil || err.Error() != "external ADB server 192.0.2.10:5037 is unavailable" {
		t.Fatalf("unexpected startup result: %v", err)
	}
}
