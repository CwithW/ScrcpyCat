package controlplane

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestShareUIReadOnlyContract(t *testing.T) {
	for _, mode := range []string{"view_only", "view"} {
		t.Run(mode, func(t *testing.T) {
			server := newTestServer(t)
			request := httptest.NewRequest(http.MethodPost, "/api/share/create", strings.NewReader(`{"device_id":"phone","access_mode":"`+mode+`"}`))
			request = request.WithContext(context.WithValue(request.Context(), userContextKey, User{Username: "admin", Role: RoleAdmin}))
			response := httptest.NewRecorder()
			server.shareCreate(response, request)
			if response.Code != http.StatusCreated {
				t.Fatal(response.Body.String())
			}
			var result struct {
				Data struct {
					Token string `json:"token"`
					Mode  string `json:"access_mode"`
				} `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			share, err := server.store.AuthorizeShare(result.Data.Token, "")
			if err != nil || !share.ViewOnly || result.Data.Mode != "view_only" {
				t.Fatalf("read-only share became %q, stored viewOnly=%v, err=%v", result.Data.Mode, share.ViewOnly, err)
			}
		})
	}
}

func TestTaskListDetailsAndBroadcastUseSameAccess(t *testing.T) {
	server := newTestServer(t)
	task, err := server.store.CreateTask(Task{Type: "shell", CreatedBy: "creator", ExpiresAt: time.Now().Add(time.Hour), Devices: map[string]TaskDevice{"phone": {DeviceID: "phone"}}})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		user User
		want bool
	}{
		{User{Username: "admin", Role: RoleAdmin}, true},
		{User{Username: "viewer", Role: RoleUser, AssignedDevices: []string{"phone"}}, false},
		{User{Username: "operator", Role: RoleUser, AssignedDevices: []string{"phone"}, Permissions: OperationPermissions{BatchTasks: true}}, true},
		{User{Username: "other", Role: RoleUser, AssignedDevices: []string{"different"}, Permissions: OperationPermissions{BatchTasks: true}}, false},
		{User{Username: "creator", Role: RoleUser}, true},
	}
	for _, test := range cases {
		listed := len(server.store.TasksFor(test.user)) == 1
		detailed := server.canSeeTask(test.user, task)
		event := server.filterBroadcast(&browserPeer{user: test.user}, map[string]any{"message_type": "task_status_updated", "task": task}) != nil
		if listed != test.want || detailed != test.want || event != test.want {
			t.Fatalf("%s: list=%v details=%v event=%v, want %v", test.user.Username, listed, detailed, event, test.want)
		}
	}
}

func TestSnapshotAndRealtimeDeviceAuthorization(t *testing.T) {
	server := newTestServer(t)
	var contents bytes.Buffer
	if err := png.Encode(&contents, image.NewNRGBA(image.Rect(0, 0, 8, 12))); err != nil {
		t.Fatal(err)
	}
	if err := server.saveSnapshot("phone", base64.StdEncoding.EncodeToString(contents.Bytes())); err != nil {
		t.Fatal(err)
	}
	user := User{Role: RoleUser, AssignedDevices: []string{"phone"}}
	request := httptest.NewRequest(http.MethodGet, "/api/devices/phone/snapshot", nil)
	request = request.WithContext(context.WithValue(request.Context(), userContextKey, user))
	response := httptest.NewRecorder()
	server.deviceSnapshot(response, request, "phone")
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), contents.Bytes()) {
		t.Fatal("authorized snapshot unavailable")
	}
	denied := httptest.NewRecorder()
	server.deviceSnapshot(denied, request, "other")
	if denied.Code != http.StatusForbidden {
		t.Fatal("unassigned snapshot was served")
	}
	peer := &browserPeer{user: user}
	if server.filterBroadcast(peer, map[string]any{"type": "device_metrics", "device_id": "other", "metrics": map[string]any{"cpu": 10}}) != nil {
		t.Fatal("unassigned metrics broadcast")
	}
	raw := server.filterBroadcast(peer, map[string]any{"message_type": "device_list_update", "devices": []Device{{ID: "phone"}, {ID: "other"}}})
	list := raw.(map[string]any)["devices"].([]Device)
	if len(list) != 1 || list[0].ID != "phone" {
		t.Fatal(list)
	}
}

func TestReplacedAgentDoesNotMarkNewConnectionOffline(t *testing.T) {
	hub := newRealtimeHub()
	old, current := &socketPeer{}, &socketPeer{}
	hub.setAgent("phone", old)
	if hub.setAgent("phone", current) != old {
		t.Fatal("previous connection lost")
	}
	if hub.removeAgent("phone", old) {
		t.Fatal("old connection removed replacement")
	}
	if !hub.removeAgent("phone", current) {
		t.Fatal("current connection could not be removed")
	}
}
