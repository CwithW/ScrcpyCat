package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	adbclient "github.com/cwithw/ScrcpyCat/internal/adb"
)

const commandOutputLimit = 1 << 20

type boundedOutput struct {
	buffer bytes.Buffer
	limit  int
}

func (output *boundedOutput) String() string { return output.buffer.String() }

func (output *boundedOutput) Write(data []byte) (int, error) {
	length := len(data)
	limit := output.limit
	if limit == 0 {
		limit = commandOutputLimit
	}
	if remaining := limit - output.buffer.Len(); remaining > 0 {
		_, _ = output.buffer.Write(data[:min(length, remaining)])
	}
	return length, nil
}

func runCommand(ctx context.Context, name string, args ...string) (string, error) {
	return executeOutput(ctx, localDevice{}, name, args...)
}

func shellResult(ctx context.Context, script string) map[string]any {
	return shellResultWithExecutor(ctx, script, localDevice{})
}

func shellResultWithExecutor(ctx context.Context, script string, executor commandExecutor) map[string]any {
	var stdout, stderr boundedOutput
	err := executor.Execute(ctx, "sh", []string{"-c", script}, &stdout, &stderr)
	exitCode := 0
	if err != nil {
		exitCode = 1
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			exitCode = exitError.ExitCode()
		}
		var adbExit *adbclient.ExitError
		if errors.As(err, &adbExit) {
			exitCode = adbExit.Code
		}
		if ctx.Err() != nil {
			exitCode = 124
			_, _ = stderr.Write([]byte(ctx.Err().Error()))
		}
	}
	return map[string]any{"stdout": stdout.String(), "stderr": stderr.String(), "exit_code": exitCode, "success": err == nil, "output": stdout.String() + stderr.String()}
}

func (c *client) executeCommand(writer *lockedWriter, message map[string]any) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	response := shellResultWithExecutor(ctx, stringField(message, "command"), c.device().commands)
	response["message_type"] = "command_result"
	response["client_id"] = stringField(message, "client_id")
	response["request_id"] = stringField(message, "request_id")
	_ = writer.writeJSON(response)
}

func (c *client) executeTask(writer *lockedWriter, task map[string]any) {
	taskID := stringField(task, "task_id")
	if taskID == "" {
		return
	}
	_ = writer.writeJSON(map[string]any{"message_type": "task_started", "task_id": taskID, "device_id": c.config.DeviceID})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	result, err := c.runTask(ctx, task)
	status := "success"
	if err != nil {
		status = "failed"
		if result == "" {
			result = err.Error()
		}
	}
	_ = writer.writeJSON(map[string]any{"message_type": "task_result", "task_id": taskID, "device_id": c.config.DeviceID, "status": status, "result": result})
}

func (c *client) runTask(ctx context.Context, task map[string]any) (string, error) {
	switch stringField(task, "type") {
	case "shell":
		return c.runCommand(ctx, "sh", "-c", stringField(task, "payload"))
	case "open_app":
		// Android monkey may be a script without a shebang; pass the package as an argument.
		return c.runCommand(ctx, "sh", "-c", `monkey -p "$1" 1`, "scrcpycat-open", stringField(task, "payload"))
	case "uninstall":
		return c.runCommand(ctx, "pm", "uninstall", stringField(task, "payload"))
	case "install", "push_file":
		file, err := c.downloadTaskAsset(ctx, stringField(task, "download_url"), stringField(task, "asset_sha256"))
		if err != nil {
			return "", err
		}
		defer os.Remove(file)
		if stringField(task, "type") == "install" {
			installPath := file
			if device := c.device(); device.adb != nil {
				installPath = fmt.Sprintf("%s/asset-%d.apk", device.adb.directory(), time.Now().UnixNano())
				if err = device.files.CommitUpload(ctx, file, installPath); err != nil {
					return "", err
				}
				defer func() {
					cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
					defer cancel()
					_ = device.files.Remove(cleanup, installPath)
				}()
			}
			return c.runCommand(ctx, "pm", "install", "-r", installPath)
		}
		dest := stringField(task, "dest_path")
		if !filepath.IsAbs(dest) {
			return "", fmt.Errorf("destination path must be absolute")
		}
		if err := c.device().files.Mkdir(ctx, filepath.Dir(dest), true); err != nil {
			return "", err
		}
		if err := c.device().files.CommitUpload(ctx, file, dest); err != nil {
			return "", err
		}
		return "stored " + dest, nil
	default:
		return "", fmt.Errorf("unsupported task type")
	}
}

func (c *client) downloadTaskAsset(ctx context.Context, rawURL, expectedHash string) (string, error) {
	endpoint, err := url.Parse(rawURL)
	if err != nil || !allowedTaskAssetURL(endpoint) {
		return "", fmt.Errorf("invalid task download URL")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned %s", response.Status)
	}
	temporary, err := os.CreateTemp("", "scrcpycat-asset-*")
	if err != nil {
		return "", err
	}
	path := temporary.Name()
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(response.Body, 2<<30))
	closeErr := temporary.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(path)
		if copyErr != nil {
			return "", copyErr
		}
		return "", closeErr
	}
	actualHash := fmt.Sprintf("%x", hash.Sum(nil))
	if expectedHash == "" || !strings.EqualFold(actualHash, expectedHash) {
		_ = os.Remove(path)
		return "", fmt.Errorf("asset checksum mismatch")
	}
	return path, nil
}

func allowedTaskAssetURL(endpoint *url.URL) bool {
	if endpoint == nil || endpoint.Hostname() == "" {
		return false
	}
	if endpoint.Scheme == "https" {
		return true
	}
	if endpoint.Scheme != "http" {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(endpoint.Hostname()), ".")
	if host == "localhost" {
		return true
	}
	parsed := net.ParseIP(host)
	return parsed != nil && (parsed.IsLoopback() || parsed.IsPrivate() || parsed.IsLinkLocalUnicast())
}

func copyFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, err = io.Copy(output, input)
	closeErr := output.Close()
	if err != nil {
		return err
	}
	return closeErr
}

// terminalData is the WSS representation of raw terminal bytes. WebRTC uses
// the same session semantics but sends bytes directly on its DataChannel.
func terminalData(data []byte) string { return base64.RawStdEncoding.EncodeToString(data) }

func (c *client) screenshot(writer *lockedWriter, message map[string]any) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output := boundedOutput{limit: 32 << 20}
	response := map[string]any{"message_type": "screenshot_response", "client_id": stringField(message, "client_id")}
	if err := c.device().commands.Execute(ctx, "screencap", []string{"-p"}, &output, nil); err != nil {
		response["error"] = err.Error()
	} else {
		response["data"] = base64.StdEncoding.EncodeToString(output.buffer.Bytes())
	}
	_ = writer.writeJSON(response)
}
