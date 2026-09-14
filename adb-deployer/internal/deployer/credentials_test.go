package deployer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeploymentCredentialConfiguration(t *testing.T) {
	base := Config{ADBPath: "adb", ArtifactDir: "/artifacts", ScrcpyJar: "/scrcpy.jar", SignalingURL: "ws://control/register_agent", ControlPlaneURL: "http://control"}
	for _, test := range []struct {
		name, deployment, admin string
		valid                   bool
	}{
		{"dedicated", "deployment-secret", "", true},
		{"legacy", "", "admin-jwt", true},
		{"missing", "", "", false},
		{"ambiguous", "deployment-secret", "admin-jwt", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := base
			config.DeploymentToken, config.AdminToken = test.deployment, test.admin
			if err := config.Validate(); (err == nil) != test.valid {
				t.Fatalf("valid=%v, err=%v", test.valid, err)
			}
		})
	}
}

func TestEnrollmentUsesDedicatedEndpointWithoutAuthFallback(t *testing.T) {
	for _, test := range []struct {
		name, deployment, admin, path, credential string
		status                                    int
	}{
		{"dedicated", "scd.test.secret", "", "/api/deploy/enrollment-tokens", "scd.test.secret", http.StatusCreated},
		{"legacy", "", "admin-jwt", "/api/agents/enrollment-tokens", "admin-jwt", http.StatusCreated},
		{"revoked", "scd.revoked.secret", "must-not-fallback", "/api/deploy/enrollment-tokens", "scd.revoked.secret", http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				calls++
				if request.Method != http.MethodPost || request.URL.Path != test.path || request.Header.Get("Authorization") != "Bearer "+test.credential {
					t.Error("incorrect credential endpoint, method or Authorization header")
				}
				var body struct {
					DeviceID string `json:"device_id"`
					TTL      int    `json:"ttl_seconds"`
				}
				if json.NewDecoder(request.Body).Decode(&body) != nil || body.DeviceID != "phone" || body.TTL != 900 {
					t.Error("incorrect enrollment binding or TTL")
				}
				w.WriteHeader(test.status)
				if test.status == http.StatusCreated {
					_, _ = w.Write([]byte(`{"token":"one-time-enrollment"}`))
				}
			}))
			defer server.Close()
			r := &runner{config: Config{ControlPlaneURL: server.URL, DeploymentToken: test.deployment, AdminToken: test.admin}, client: server.Client()}
			token, err := r.enrollmentToken(context.Background(), "phone")
			if test.status == http.StatusCreated {
				if err != nil || token != "one-time-enrollment" {
					t.Fatalf("enrollment failed: %v", err)
				}
			} else if err == nil || token != "" {
				t.Fatal("rejected credential silently fell back or succeeded")
			}
			if calls != 1 {
				t.Fatalf("requests=%d; credentials must not trigger fallback", calls)
			}
		})
	}
}

func TestRejectedCredentialDoesNotStopOrReplaceAgent(t *testing.T) {
	directory := t.TempDir()
	devices := &fakeADB{}
	if err := os.Mkdir(filepath.Join(directory, "arm64-v8a"), 0700); err != nil {
		t.Fatal(err)
	}
	agentPath := filepath.Join(directory, "arm64-v8a", "scrcpycat-agent")
	jarPath := filepath.Join(directory, "scrcpy.jar")
	for _, path := range []string{agentPath, jarPath} {
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer server.Close()
	r := &runner{config: Config{Mode: ModeDevice, ArtifactDir: directory, ScrcpyJar: jarPath, ControlPlaneURL: server.URL, DeploymentToken: "revoked"}, devices: devices, client: server.Client()}
	err := r.ensureAgent(context.Background(), "phone")
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected credential rejection, got %v", err)
	}
	if devices.mutated() {
		t.Fatal("credential failure still stopped, pushed or launched an Agent")
	}
}
