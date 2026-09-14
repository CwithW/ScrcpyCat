package controlplane

import (
	"archive/zip"
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

//go:embed deploy-assets/*
var deploymentAssets embed.FS

var deviceABIs = []string{"arm64-v8a", "armeabi-v7a", "x86_64", "x86"}

func (s *Server) agentArtifact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/agent/")
	path := ""
	if name == "scrcpy-server.jar" {
		path = s.config.ScrcpyJar
	}
	for _, abi := range deviceABIs {
		if name == abi+"/scrcpycat-agent" {
			path = filepath.Join(s.config.AgentDir, abi, "scrcpycat-agent")
		}
	}
	if path == "" {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, path)
}

func signalingEndpoint(raw string) (string, error) {
	endpoint, err := url.Parse(raw)
	if err != nil || endpoint.Hostname() == "" || endpoint.User != nil {
		return "", fmt.Errorf("invalid signaling address")
	}
	switch endpoint.Scheme {
	case "http":
		endpoint.Scheme = "ws"
	case "https":
		endpoint.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("signaling address requires ws:// or wss://")
	}
	endpoint.Path = "/register_agent"
	endpoint.RawQuery, endpoint.Fragment = "", ""
	return endpoint.String(), nil
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

func (s *Server) deploymentPackage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var request map[string]any
	if decodeJSON(r, &request) != nil {
		writeError(w, http.StatusBadRequest, "invalid deployment request")
		return
	}
	mode := stringField(request, "mode")
	if mode != "adb" && mode != "magisk" {
		writeError(w, http.StatusBadRequest, "mode must be adb or magisk")
		return
	}
	id := stringField(request, "device_id")
	if id == "" {
		id = "android-" + randomID()[:12]
	}
	if !regexp.MustCompile("^[A-Za-z0-9_.-]{1,32}$").MatchString(id) {
		writeError(w, http.StatusBadRequest, "invalid device ID")
		return
	}
	raw := stringField(request, "signaling")
	if raw == "" {
		raw = s.config.PublicURL
	}
	signaling, err := signalingEndpoint(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	payloads := map[string]string{"scrcpy-server.jar": s.config.ScrcpyJar}
	for _, abi := range deviceABIs {
		payloads["bin/"+abi+"/scrcpycat-agent"] = filepath.Join(s.config.AgentDir, abi, "scrcpycat-agent")
	}
	for _, path := range payloads {
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			writeError(w, http.StatusServiceUnavailable, "Agent artifacts have not been built or mounted")
			return
		}
	}
	enrollment := s.store.CreateEnrollment(id, 24*time.Hour)
	config := map[string]string{"device_id": id, "signaling": signaling, "enrollment_token": enrollment.Token}
	configJSON, _ := json.MarshalIndent(config, "", "  ")
	configEnv := "SCRCPYCAT_DEVICE_ID=" + shellQuote(id) + "\nSCRCPYCAT_SIGNALING=" + shellQuote(signaling) + "\nSCRCPYCAT_ENROLLMENT_TOKEN=" + shellQuote(enrollment.Token) + "\n"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"scrcpycat-%s.zip\"", mode))
	w.Header().Set("Cache-Control", "private, no-store")
	archive := zip.NewWriter(w)
	defer archive.Close()
	add := func(name string, contents io.Reader, executable bool) error {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		if executable {
			header.SetMode(0755)
		} else {
			header.SetMode(0600)
		}
		entry, err := archive.CreateHeader(header)
		if err == nil {
			_, err = io.Copy(entry, contents)
		}
		return err
	}
	if add("config.env", strings.NewReader(configEnv), false) != nil {
		return
	}
	if add("config.json", bytes.NewReader(configJSON), false) != nil {
		return
	}
	names := map[string]string{"run.sh": "run.sh", "run.ps1": "run.ps1", "run.bat": "run.bat"}
	if mode == "magisk" {
		names = map[string]string{"service.sh": "service.sh", "customize.sh": "customize.sh", "module.prop": "module.prop", "action.sh": "action.sh", "system/bin/scrcpycatctl": "scrcpycatctl"}
	}
	for target, source := range names {
		contents, err := deploymentAssets.ReadFile("deploy-assets/" + source)
		if err != nil || add(target, bytes.NewReader(contents), strings.HasSuffix(source, ".sh") || source == "scrcpycatctl") != nil {
			return
		}
	}
	for target, source := range payloads {
		file, err := os.Open(source)
		if err != nil {
			return
		}
		err = add(target, file, strings.HasSuffix(target, "scrcpycat-agent"))
		_ = file.Close()
		if err != nil {
			return
		}
	}
	s.audit(currentUser(r), "deployment.package", id, map[string]any{"mode": mode})
}
