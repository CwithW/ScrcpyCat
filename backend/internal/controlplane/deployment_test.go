package controlplane

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeploymentPackagesContainSelfHostedArtifacts(t *testing.T) {
	server := newTestServer(t)
	server.config.AgentDir = t.TempDir()
	server.config.ScrcpyJar = filepath.Join(t.TempDir(), "scrcpy.jar")
	os.WriteFile(server.config.ScrcpyJar, []byte("scrcpy server"), 0600)
	for _, abi := range deviceABIs {
		dir := filepath.Join(server.config.AgentDir, abi)
		os.MkdirAll(dir, 0755)
		os.WriteFile(filepath.Join(dir, "scrcpycat-agent"), []byte("agent "+abi), 0755)
	}
	admin, _ := server.store.Authenticate("admin", "password")
	token, _ := issueUserToken(server.config.JWTSecret, admin)
	for _, mode := range []string{"adb", "magisk"} {
		body, _ := json.Marshal(map[string]string{"mode": mode, "device_id": "deploy-test", "signaling": "http://192.168.1.20:8080"})
		request := httptest.NewRequest(http.MethodPost, "/api/deploy/package", bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", mode, response.Code, response.Body.String())
		}
		archive, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len()))
		if err != nil {
			t.Fatal(err)
		}
		files := map[string][]byte{}
		for _, file := range archive.File {
			reader, _ := file.Open()
			files[file.Name], _ = io.ReadAll(reader)
			reader.Close()
		}
		var config map[string]string
		if json.Unmarshal(files["config.json"], &config) != nil || config["signaling"] != "ws://192.168.1.20:8080/register_agent" || config["device_id"] != "deploy-test" {
			t.Fatal("incorrect deployment config")
		}
		if _, err := server.store.ExchangeEnrollment(config["enrollment_token"], "another-device"); err == nil {
			t.Fatal("registration token is not device-bound")
		}
		if _, err := server.store.ExchangeEnrollment(config["enrollment_token"], "deploy-test"); err != nil {
			t.Fatal(err)
		}
		for _, abi := range deviceABIs {
			if len(files["bin/"+abi+"/scrcpycat-agent"]) == 0 {
				t.Fatal("missing " + abi)
			}
		}
		for name, contents := range files {
			if strings.HasSuffix(name, ".sh") {
				file := filepath.Join(t.TempDir(), "script.sh")
				os.WriteFile(file, contents, 0600)
				if output, err := exec.Command("sh", "-n", file).CombinedOutput(); err != nil {
					t.Fatalf("%s: %s", name, output)
				}
			}
		}
		if mode == "adb" && !bytes.Contains(files["run.sh"], []byte("--enrollment-token")) {
			t.Fatal("ADB script lacks enrollment")
		}
		if mode == "magisk" && len(files["system/bin/scrcpycatctl"]) == 0 {
			t.Fatal("module has no control script")
		}
	}
}

func TestArtifactDownloadsRequireAdministrator(t *testing.T) {
	server := newTestServer(t)
	user, _ := server.store.CreateUser("viewer", "password", RoleUser, nil)
	token, _ := issueUserToken(server.config.JWTSecret, user)
	request := httptest.NewRequest(http.MethodGet, "/agent/arm64-v8a/scrcpycat-agent", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatal(response.Code)
	}
}
