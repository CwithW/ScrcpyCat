package adb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

// This opt-in test uses an existing ADB server; it never starts an ADB daemon.
func TestLiveDeviceTransport(t *testing.T) {
	server, serial := os.Getenv("SCRCPYCAT_TEST_ADB_SERVER"), os.Getenv("SCRCPYCAT_TEST_ADB_SERIAL")
	if server == "" || serial == "" {
		t.Skip("set SCRCPYCAT_TEST_ADB_SERVER and SCRCPYCAT_TEST_ADB_SERIAL for a real-device test")
	}
	client, err := New(server)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := client.Online(ctx, serial); err != nil {
		t.Fatal(err)
	}
	devices, err := client.Devices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("online devices: %+v", devices)
	found := false
	for _, device := range devices {
		found = found || device.Serial == serial
	}
	if !found {
		t.Fatal("online device missing from enumeration")
	}
	for _, legacy := range []bool{false, true} {
		var output bytes.Buffer
		if legacy {
			err = client.RunLegacy(ctx, serial, "printf adb-fixture; exit 7", &output)
		} else {
			err = client.Run(ctx, serial, "printf adb-fixture; exit 7", &output, &output)
		}
		var exit *ExitError
		if !errors.As(err, &exit) || exit.Code != 7 || output.String() != "adb-fixture" {
			t.Fatalf("legacy=%t output=%q error=%v", legacy, output.String(), err)
		}
	}
	path := fmt.Sprintf("/data/local/tmp/.scrcpycat-adb-test-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = client.RunLegacy(ctx, serial, "rm -f -- "+Quote(path), nil)
	})
	data := []byte("ScrcpyCat ADB Sync test\n\x00binary")
	if err := client.Push(ctx, serial, bytes.NewReader(data), path, 0600); err != nil {
		t.Fatal(err)
	}
	var downloaded bytes.Buffer
	if err := client.Pull(ctx, serial, path, &downloaded); err != nil || !bytes.Equal(downloaded.Bytes(), data) {
		t.Fatalf("Sync roundtrip: %v", err)
	}
}
