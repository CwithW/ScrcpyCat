package controlplane

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func contractRequest(t *testing.T, s *Server, token, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(raw))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, request)
	return response
}

func contractToken(t *testing.T, s *Server, username string) string {
	t.Helper()
	user, err := s.store.Authenticate(username, "password")
	if err != nil {
		t.Fatal(err)
	}
	token, err := issueUserToken(s.config.JWTSecret, user)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestFrontendTagsPersistBothSpellingsAndFilterAssignments(t *testing.T) {
	s := newTestServer(t)
	s.store.UpsertAgentDevice("allowed", DeviceInfo{})
	s.store.UpsertAgentDevice("hidden", DeviceInfo{})
	_, _ = s.store.CreateUser("operator", "password", RoleUser, []string{"allowed"})
	admin, operator := contractToken(t, s, "admin"), contractToken(t, s, "operator")
	for _, spelling := range []string{"deviceTags", "device_tags"} {
		body := map[string]any{"tags": []Tag{{ID: "tag", Name: "设备", Color: "#22c55e"}}, spelling: map[string][]string{"allowed": {"tag"}, "hidden": {"tag"}}}
		response := contractRequest(t, s, admin, http.MethodPost, "/api/tags", body)
		if response.Code != http.StatusOK {
			t.Fatalf("%s save: %d %s", spelling, response.Code, response.Body)
		}
		response = contractRequest(t, s, operator, http.MethodGet, "/api/tags", nil)
		var saved struct {
			Tags       []Tag               `json:"tags"`
			DeviceTags map[string][]string `json:"deviceTags"`
			Legacy     map[string][]string `json:"device_tags"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &saved); err != nil {
			t.Fatal(err)
		}
		if len(saved.Tags) != 1 || !reflect.DeepEqual(saved.DeviceTags, map[string][]string{"allowed": {"tag"}}) || !reflect.DeepEqual(saved.DeviceTags, saved.Legacy) {
			t.Fatal(saved)
		}
		if response := contractRequest(t, s, operator, http.MethodPost, "/api/tags", body); response.Code != http.StatusForbidden {
			t.Fatalf("operator changed shared tags: %d", response.Code)
		}
	}
}

func TestFrontendNetworkSpeedsSupportBothAgentGenerations(t *testing.T) {
	legacy := map[string]any{"download_speed": float64(2048), "upload_speed": float64(1024), "cpu": float64(20)}
	metrics := normalizeNetworkMetrics(legacy)
	if metrics["download_speed"] != float64(2) || metrics["upload_speed"] != float64(1) || metrics["cpu"] != float64(20) || legacy["download_speed"] != float64(2048) {
		t.Fatal(metrics, legacy)
	}
	if converted := normalizeNetworkMetrics(metrics); !reflect.DeepEqual(converted, metrics) {
		t.Fatal("new Agent rates were converted twice", converted)
	}
}

func TestLockedConnectionSettingsUseDeviceDefaults(t *testing.T) {
	s := newTestServer(t)
	s.store.UpsertAgentDevice("phone", DeviceInfo{})
	s.store.SetDefaultSettings(map[string]any{"size": float64(720), "fps": float64(30)})
	if err := s.store.SetDeviceSettings("phone", map[string]any{"size": float64(960), "fps": float64(15)}); err != nil {
		t.Fatal(err)
	}
	user := User{Role: RoleUser, ForbidResolution: true, ForbidFPS: true}
	options := s.constrainedStreamOptions(user, "phone", map[string]any{"max_size": float64(100), "max_fps": float64(60), "snapshot_interval": float64(1), "snapshotInterval": float64(1)})
	if options["max_size"] != float64(960) || options["max_fps"] != float64(15) {
		t.Fatal("locked settings ignored the device defaults", options)
	}
	for _, key := range []string{"snapshot_interval", "snapshotInterval"} {
		if _, ok := options[key]; ok {
			t.Fatal("non-admin changed the device snapshot interval", key)
		}
	}
}

func TestFrontendLogoutRevokesCurrentLoginAndItsTickets(t *testing.T) {
	s := newTestServer(t)
	first, other := contractToken(t, s, "admin"), contractToken(t, s, "admin")
	if first == other {
		t.Fatal("separate logins share a token")
	}
	ticketResponse := contractRequest(t, s, first, http.MethodPost, "/api/ws-ticket", map[string]any{})
	var ticket struct {
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal(ticketResponse.Body.Bytes(), &ticket); err != nil || ticket.Ticket == "" {
		t.Fatal(ticketResponse.Body.String(), err)
	}
	if response := contractRequest(t, s, first, http.MethodGet, "/api/logout", nil); response.Code != http.StatusMethodNotAllowed {
		t.Fatal("GET logged out the user")
	}
	if response := contractRequest(t, s, first, http.MethodPost, "/api/logout", nil); response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	if response := contractRequest(t, s, first, http.MethodGet, "/api/me", nil); response.Code != http.StatusUnauthorized {
		t.Fatal("logged-out token still works")
	}
	if response := contractRequest(t, s, other, http.MethodGet, "/api/me", nil); response.Code != http.StatusOK {
		t.Fatal("logout revoked another login")
	}
	if _, _, _, err := s.browserAccessFromTicket(ticket.Ticket); err == nil {
		t.Fatal("logged-out browser ticket still works")
	}
	store := &PostgresStore{inner: s.store.(*MemoryStore)}
	raw, err := store.snapshotJSON()
	if err != nil {
		t.Fatal(err)
	}
	var persisted persistedState
	if err := json.Unmarshal(raw, &persisted); err != nil || len(persisted.RevokedUserTokens) != 1 {
		t.Fatal("logout omitted from durable snapshot", err)
	}
	_ = s.store.RevokeUserToken("expired", time.Now().Add(-time.Second))
	s.store.Clean(time.Now().Add(-time.Hour))
	if s.store.UserTokenRevoked("expired") {
		t.Fatal("expired revocation retained")
	}
}

func TestFrontendUserPresenceIncludesGlobalAndBoundSessions(t *testing.T) {
	s := newTestServer(t)
	admin, _ := s.store.Authenticate("admin", "password")
	idle, _ := s.store.CreateUser("idle", "password", RoleUser, nil)
	s.hub.addBrowser(&browserPeer{id: "global", user: admin})
	s.hub.addBrowser(&browserPeer{id: "screen", user: admin, deviceID: "phone"})
	s.hub.addBrowser(&browserPeer{id: "second-screen", user: admin, deviceID: "phone"})
	entries := s.hub.usersWithPresence([]User{admin, idle})
	if !entries[0].Online || !reflect.DeepEqual(entries[0].ActiveDevices, []string{"phone"}) || entries[1].Online || len(entries[1].ActiveDevices) != 0 {
		t.Fatal(entries)
	}
	s.hub.removeBrowser("screen")
	s.hub.removeBrowser("second-screen")
	entries = s.hub.usersWithPresence([]User{admin})
	if !entries[0].Online || len(entries[0].ActiveDevices) != 0 {
		t.Fatal("global presence treated as a control session", entries)
	}
}

func TestFrontendDeviceSettingsReachAgentAndPersist(t *testing.T) {
	s := newTestServer(t)
	s.store.UpsertAgentDevice("phone", DeviceInfo{})
	s.store.UpsertAgentDevice("other", DeviceInfo{})
	_, _ = s.store.CreateUser("operator", "password", RoleUser, []string{"phone"})
	admin, operator := contractToken(t, s, "admin"), contractToken(t, s, "operator")
	body := map[string]any{"snapshotInterval": float64(-1), "debug": true, "connectionStayAwake": false}
	if response := contractRequest(t, s, admin, http.MethodPost, "/api/devices/phone/settings", body); response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	s.store.SetDefaultSettings(map[string]any{"snapshotInterval": float64(30)})
	if s.agentSettings("phone")["snapshotInterval"] != float64(-1) || s.agentSettings("other")["snapshotInterval"] != float64(30) {
		t.Fatal("per-device snapshot setting was lost")
	}
	if response := contractRequest(t, s, operator, http.MethodPost, "/api/devices/phone/settings", body); response.Code != http.StatusForbidden {
		t.Fatal("operator overwrote shared device defaults")
	}
	if response := contractRequest(t, s, operator, http.MethodGet, "/api/devices/other/settings", nil); response.Code != http.StatusForbidden {
		t.Fatal("settings leaked across device assignments")
	}
	if response := contractRequest(t, s, admin, http.MethodPost, "/api/devices/phone/settings", map[string]any{"snapshotInterval": -2}); response.Code != http.StatusBadRequest {
		t.Fatal("invalid snapshot interval accepted")
	}
	raw, _ := (&PostgresStore{inner: s.store.(*MemoryStore)}).snapshotJSON()
	var persisted persistedState
	if err := json.Unmarshal(raw, &persisted); err != nil || !reflect.DeepEqual(persisted.DeviceSettings["phone"], body) {
		t.Fatal("device settings omitted from durable snapshot", err)
	}
	if response := contractRequest(t, s, admin, http.MethodDelete, "/api/devices/phone/settings", nil); response.Code != http.StatusOK || s.agentSettings("phone")["snapshotInterval"] != float64(30) {
		t.Fatal("reset did not restore global defaults")
	}
}

func TestForegroundPreviewWinsWithoutLosingWakeRequests(t *testing.T) {
	h := newRealtimeHub()
	h.setPreviewRequest("screen", "phone", map[string]any{"preview": false, "stay_awake": false, "max_size": float64(0)})
	h.setPreviewRequest("thumbnail", "phone", map[string]any{"preview": true, "stay_awake": true, "max_size": float64(360)})
	selected := h.selectedPreviewRequest("phone")
	if selected["preview"] != false || selected["stay_awake"] != true || selected["max_size"] != float64(0) {
		t.Fatal(selected)
	}
}
