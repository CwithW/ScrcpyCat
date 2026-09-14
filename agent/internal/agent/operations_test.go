package agent

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestAllowedTaskAssetURL(t *testing.T) {
	tests := []struct {
		raw  string
		want bool
	}{
		{raw: "https://assets.example.com/file", want: true},
		{raw: "http://127.0.0.1:8443/file", want: true},
		{raw: "http://[::1]:8443/file", want: true},
		{raw: "http://localhost:8443/file", want: true},
		{raw: "http://assets.example.com/file", want: false},
		{raw: "http://10.0.0.1:8443/file", want: true},
		{raw: "http://8.8.8.8/file", want: false},
		{raw: "file:///tmp/file", want: false},
	}
	for _, test := range tests {
		t.Run(test.raw, func(t *testing.T) {
			endpoint, err := url.Parse(test.raw)
			if err != nil {
				t.Fatalf("parse URL: %v", err)
			}
			if got := allowedTaskAssetURL(endpoint); got != test.want {
				t.Fatalf("allowedTaskAssetURL(%q) = %t, want %t", test.raw, got, test.want)
			}
		})
	}
}

func TestShellResultMatchesBrowserContract(t *testing.T) {
	result := shellResult(context.Background(), "printf stdout; printf stderr >&2; exit 7")
	if result["stdout"] != "stdout" || result["stderr"] != "stderr" || result["exit_code"] != 7 || result["success"] != false {
		t.Fatalf("unexpected shell response: %#v", result)
	}
}

func TestCommandOutputIsBoundedDuringExecution(t *testing.T) {
	output, err := runCommand(context.Background(), "sh", "-c", "head -c 2097152 /dev/zero")
	if err != nil {
		t.Fatal(err)
	}
	if len(output) != commandOutputLimit {
		t.Fatalf("output bytes = %d", len(output))
	}
}

func TestOpenAppStartsAndroidScriptWithoutShebang(t *testing.T) {
	directory := t.TempDir()
	script := []byte("printf 'package=%s count=%s' \"$2\" \"$3\"\n")
	if err := os.WriteFile(filepath.Join(directory, "monkey"), script, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	client := &client{}
	result, err := client.runTask(context.Background(), map[string]any{
		"type": "open_app", "payload": "org.scrcpycat.fixture",
	})
	if err != nil {
		t.Fatalf("open Android app: %v", err)
	}
	if result != "package=org.scrcpycat.fixture count=1" {
		t.Fatalf("unexpected launch arguments: %q", result)
	}
}
