package controlplane

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLoginAndCreateEnrollmentToken(t *testing.T) {
	server := newTestServer(t)

	loginBody := bytes.NewBufferString(`{"username":"admin","password":"password"}`)
	loginRequest := httptest.NewRequest(http.MethodPost, "/api/login", loginBody)
	loginResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(loginResponse, loginRequest)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginResponse.Code, loginResponse.Body.String())
	}
	var login map[string]any
	if err := json.Unmarshal(loginResponse.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}
	token, _ := login["token"].(string)
	if token == "" {
		t.Fatal("login did not return a token")
	}

	enrollRequest := httptest.NewRequest(http.MethodPost, "/api/agents/enrollment-tokens", bytes.NewBufferString(`{"device_id":"device-1"}`))
	enrollRequest.Header.Set("Authorization", "Bearer "+token)
	enrollResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(enrollResponse, enrollRequest)
	if enrollResponse.Code != http.StatusCreated {
		t.Fatalf("enrollment status = %d, body = %s", enrollResponse.Code, enrollResponse.Body.String())
	}
}

func TestPreviewFrameValidation(t *testing.T) {
	frame := make([]byte, 49)
	copy(frame[:4], "PREV")
	copy(frame[4:36], "device-1")
	if !validPreviewFrame("device-1", frame) {
		t.Fatal("valid preview frame rejected")
	}
	if validPreviewFrame("device-2", frame) {
		t.Fatal("foreign preview frame accepted")
	}
}

func TestTaskPermissionsAndLifecycle(t *testing.T) {
	server := newTestServer(t)
	server.store.UpsertAgentDevice("device-1", DeviceInfo{Model: "test"})
	if _, err := server.store.CreateUser("operator", "password", RoleUser, []string{"device-1"}); err != nil {
		t.Fatal(err)
	}

	login := func(username string) string {
		req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewBufferString(`{"username":"`+username+`","password":"password"}`))
		rec := httptest.NewRecorder()
		server.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("login %s: %d", username, rec.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body["token"].(string)
	}
	operatorToken := login("operator")
	denied := httptest.NewRequest(http.MethodPost, "/api/tasks", bytes.NewBufferString(`{"type":"shell","targets":["device-1"],"payload":"id"}`))
	denied.Header.Set("Authorization", "Bearer "+operatorToken)
	deniedRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(deniedRec, denied)
	if deniedRec.Code != http.StatusForbidden {
		t.Fatalf("expected permission rejection, got %d", deniedRec.Code)
	}

	adminToken := login("admin")
	create := httptest.NewRequest(http.MethodPost, "/api/tasks", bytes.NewBufferString(`{"type":"shell","targets":["device-1"],"payload":"id"}`))
	create.Header.Set("Authorization", "Bearer "+adminToken)
	createRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(createRec, create)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("task create: %d %s", createRec.Code, createRec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	taskID := created["task_id"].(string)
	if pending := server.store.PendingTasks("device-1"); len(pending) != 1 || pending[0] != taskID {
		t.Fatalf("pending = %#v", pending)
	}
	if _, err := server.store.ClaimTask(taskID, "device-1"); err != nil {
		t.Fatal(err)
	}
	task, err := server.store.SetTaskStatus(taskID, "device-1", TaskSuccess, "uid=0")
	if err != nil || task.Devices["device-1"].Status != TaskSuccess {
		t.Fatalf("task result = %#v, err=%v", task, err)
	}
	server.store.Clean(time.Now().UTC().Add(time.Hour))
}
