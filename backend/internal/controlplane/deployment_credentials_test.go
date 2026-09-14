package controlplane

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func deploymentAPI(t *testing.T, server *Server, token, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func deploymentAdmin(t *testing.T, server *Server) string {
	t.Helper()
	admin, err := server.store.Authenticate("admin", "password")
	if err != nil {
		t.Fatal(err)
	}
	token, err := issueUserToken(server.config.JWTSecret, admin)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func createDeploymentViaAPI(t *testing.T, server *Server, admin string, body any) (DeploymentCredential, string) {
	t.Helper()
	response := deploymentAPI(t, server, admin, http.MethodPost, "/api/admin/deployment-credentials", body)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("credential response must not be cached")
	}
	var result struct {
		Credential DeploymentCredential `json:"credential"`
		Token      string               `json:"token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result.Credential, result.Token
}

func TestDeploymentCredentialDefaultsToUnlimitedAndPersistsOnlyHashes(t *testing.T) {
	server := newTestServer(t)
	admin := deploymentAdmin(t, server)
	credential, token := createDeploymentViaAPI(t, server, admin, map[string]any{"name": "USB deployer"})
	if credential.ExpiresAt != nil || credential.Scope != deploymentCredentialScope || credential.CreatedBy != "admin" {
		t.Fatalf("unexpected credential metadata: %+v", credential)
	}
	if !strings.HasPrefix(token, "scd."+credential.ID+".") {
		t.Fatal("missing independent deployment credential format")
	}
	zero, _ := createDeploymentViaAPI(t, server, admin, map[string]any{"name": "Explicit unlimited", "ttl_seconds": 0})
	if zero.ExpiresAt != nil {
		t.Fatal("zero TTL must remain unlimited")
	}
	inner := server.store.(*MemoryStore)
	if !inner.deploymentCredentials[credential.ID].active(time.Now().AddDate(100, 0, 0)) {
		t.Fatal("unlimited credential acquires an implicit expiration")
	}
	enrollment, err := inner.IssueDeploymentEnrollment(token, "phone", maxDeploymentEnrollmentTTL)
	if err != nil {
		t.Fatal(err)
	}
	if enrollment.ExpiresAt.Sub(time.Now()) > maxDeploymentEnrollmentTTL || enrollment.ExpiresAt.IsZero() {
		t.Fatal("unlimited credential must still issue short-lived enrollment")
	}
	listed := deploymentAPI(t, server, admin, http.MethodGet, "/api/admin/deployment-credentials", nil)
	if listed.Code != http.StatusOK || bytes.Contains(listed.Body.Bytes(), []byte(token)) || bytes.Contains(listed.Body.Bytes(), []byte("token_hash")) {
		t.Fatal("credential listing exposes secret material")
	}
	snapshot, err := (&PostgresStore{inner: inner}).snapshotJSON()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(snapshot, []byte(token)) || bytes.Contains(snapshot, []byte(enrollment.Token)) {
		t.Fatal("snapshot or audit log stores a raw credential")
	}
	var state persistedState
	if err := json.Unmarshal(snapshot, &state); err != nil {
		t.Fatal(err)
	}
	saved := state.DeploymentCredentials[credential.ID]
	if saved.TokenHash != tokenDigest(token) || saved.ExpiresAt != nil || saved.LastUsedAt == nil {
		t.Fatal("snapshot lost hashed credential metadata")
	}
	if state.Enrollments[tokenDigest(enrollment.Token)].DeploymentCredentialID != credential.ID {
		t.Fatal("snapshot lost enrollment issuer")
	}
	foundCreate, foundUse := false, false
	for _, event := range state.Audits {
		foundCreate = foundCreate || event.Action == "deployment.credential.create" && event.Target == credential.ID && event.Actor == "admin"
		foundUse = foundUse || event.Action == "deployment.enrollment.create" && event.Target == "phone" && event.Actor == "deployment:"+credential.ID
	}
	if !foundCreate || !foundUse {
		t.Fatal("missing credential audit trail")
	}
}

func TestDeploymentCredentialIsNotAUserOrAgentCredential(t *testing.T) {
	server := newTestServer(t)
	admin := deploymentAdmin(t, server)
	credential, token := createDeploymentViaAPI(t, server, admin, map[string]any{"name": "Scoped deployer"})
	user, err := server.store.CreateUser("operator", "password", RoleUser, nil)
	if err != nil {
		t.Fatal(err)
	}
	userToken, _ := issueUserToken(server.config.JWTSecret, user)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		response := deploymentAPI(t, server, userToken, method, "/api/admin/deployment-credentials", map[string]any{"name": "denied"})
		if response.Code != http.StatusForbidden {
			t.Fatalf("ordinary user could manage deployment credentials: %d", response.Code)
		}
	}
	if response := deploymentAPI(t, server, userToken, http.MethodPost, "/api/admin/deployment-credentials/revoke", map[string]any{"id": credential.ID}); response.Code != http.StatusForbidden {
		t.Fatal("ordinary user could revoke deployment credentials")
	}
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/me"}, {http.MethodGet, "/devices"},
		{http.MethodGet, "/api/admin/users"}, {http.MethodGet, "/api/admin/deployment-credentials"},
		{http.MethodPost, "/api/admin/deployment-credentials/revoke"},
		{http.MethodGet, "/api/files"}, {http.MethodPost, "/upload"},
		{http.MethodPost, "/api/tasks"}, {http.MethodPost, "/api/agents/enrollment-tokens"},
		{http.MethodPost, "/api/deploy/package"}, {http.MethodGet, "/agent/arm64-v8a/scrcpycat-agent"},
		{http.MethodGet, "/connect_client"},
	} {
		response := deploymentAPI(t, server, token, route.method, route.path, map[string]any{})
		if response.Code != http.StatusUnauthorized {
			t.Errorf("deployment credential authorized %s %s: %d", route.method, route.path, response.Code)
		}
	}
	if server.store.ValidateAgentToken(token, "phone") {
		t.Fatal("deployment credential directly authenticates an Agent")
	}
	if _, err := server.store.ExchangeEnrollment(token, "phone"); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("deployment credential directly exchanges as an enrollment")
	}
	endpoint := "/api/deploy/enrollment-tokens"
	body := map[string]any{"device_id": "phone"}
	for _, other := range []string{"", token + "tampered", admin, userToken} {
		response := deploymentAPI(t, server, other, http.MethodPost, endpoint, body)
		if response.Code != http.StatusUnauthorized {
			t.Errorf("incorrect credential accepted by dedicated endpoint: %d", response.Code)
		}
	}
	response := deploymentAPI(t, server, "", http.MethodPost, endpoint+"?token="+token, body)
	if response.Code != http.StatusUnauthorized {
		t.Fatal("deployment credential accepted from a URL")
	}
	response = deploymentAPI(t, server, token, http.MethodPost, endpoint, body)
	if response.Code != http.StatusCreated {
		t.Fatalf("valid dedicated credential rejected: %d", response.Code)
	}
	legacy := deploymentAPI(t, server, admin, http.MethodPost, "/api/agents/enrollment-tokens", body)
	if legacy.Code != http.StatusCreated {
		t.Fatal("existing administrator enrollment flow changed")
	}
}

func TestDeploymentCredentialExpiryRevocationAndRotation(t *testing.T) {
	server := newTestServer(t)
	inner := server.store.(*MemoryStore)
	admin := deploymentAdmin(t, server)
	limited, limitedToken := createDeploymentViaAPI(t, server, admin, map[string]any{"name": "Temporary deployer", "ttl_seconds": 3600})
	if limited.ExpiresAt == nil {
		t.Fatal("explicit TTL ignored")
	}
	// A nearly expired issuer cannot create an enrollment which outlives it.
	soon := time.Now().UTC().Add(time.Minute)
	inner.mu.Lock()
	saved := inner.deploymentCredentials[limited.ID]
	saved.ExpiresAt = &soon
	inner.deploymentCredentials[limited.ID] = saved
	inner.mu.Unlock()
	limitedEnrollment, err := inner.IssueDeploymentEnrollment(limitedToken, "phone", maxDeploymentEnrollmentTTL)
	if err != nil || !limitedEnrollment.ExpiresAt.Equal(soon) {
		t.Fatalf("enrollment did not inherit issuer deadline: %v", err)
	}
	past := time.Now().UTC().Add(-time.Second)
	inner.mu.Lock()
	saved.ExpiresAt = &past
	inner.deploymentCredentials[limited.ID] = saved
	inner.mu.Unlock()
	if _, err := inner.IssueDeploymentEnrollment(limitedToken, "phone", time.Minute); !errors.Is(err, ErrInvalidAuth) {
		t.Fatal("expired credential can issue enrollment")
	}
	if _, err := inner.ExchangeEnrollment(limitedEnrollment.Token, "phone"); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("enrollment with expired issuer can be consumed")
	}

	old, oldToken := createDeploymentViaAPI(t, server, admin, map[string]any{"name": "USB deployer"})
	activeEnrollment, _ := inner.IssueDeploymentEnrollment(oldToken, "phone", time.Minute)
	agent, err := inner.ExchangeEnrollment(activeEnrollment.Token, "phone")
	if err != nil {
		t.Fatal(err)
	}
	pendingEnrollment, _ := inner.IssueDeploymentEnrollment(oldToken, "new-phone", time.Minute)
	_, replacementToken := createDeploymentViaAPI(t, server, admin, map[string]any{"name": "Replacement deployer"})
	if _, err := inner.IssueDeploymentEnrollment(replacementToken, "phone", time.Minute); err != nil {
		t.Fatal("new credential cannot be activated before revoking the old one")
	}
	revoked := deploymentAPI(t, server, admin, http.MethodPost, "/api/admin/deployment-credentials/revoke", map[string]any{"id": old.ID})
	if revoked.Code != http.StatusOK {
		t.Fatalf("revoke status = %d", revoked.Code)
	}
	if _, err := inner.IssueDeploymentEnrollment(oldToken, "phone", time.Minute); !errors.Is(err, ErrInvalidAuth) {
		t.Fatal("revoked credential still issues enrollment")
	}
	if _, err := inner.ExchangeEnrollment(pendingEnrollment.Token, "new-phone"); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("revocation did not invalidate an unused enrollment")
	}
	if !inner.ValidateAgentToken(agent.Token, "phone") {
		t.Fatal("revoking deployer incorrectly invalidated an already-enrolled Agent")
	}
	enrollment, err := inner.IssueDeploymentEnrollment(replacementToken, "new-phone", time.Minute)
	if err != nil {
		t.Fatal("revocation invalidated the replacement credential")
	}
	if _, err := inner.ExchangeEnrollment(enrollment.Token, "other-phone"); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("enrollment was not bound to the requested device")
	}
	if _, err := inner.ExchangeEnrollment(enrollment.Token, "new-phone"); err != nil {
		t.Fatal(err)
	}
	if _, err := inner.ExchangeEnrollment(enrollment.Token, "new-phone"); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("enrollment was reusable")
	}
	revokeCount := 0
	for _, event := range inner.audits {
		if event.Action == "deployment.credential.revoke" && event.Target == old.ID {
			revokeCount++
		}
	}
	if revokeCount != 1 {
		t.Fatal("missing revocation audit event")
	}
}

func TestDeploymentCredentialValidationAndUnavailableStore(t *testing.T) {
	server := newTestServer(t)
	admin := deploymentAdmin(t, server)
	for _, body := range []map[string]any{
		{"name": ""}, {"name": strings.Repeat("x", 81)},
		{"name": "bad", "ttl_seconds": -1}, {"name": "bad", "ttl_seconds": int64(1<<63 - 1)},
		{"name": "bad", "scope": "admin"},
	} {
		response := deploymentAPI(t, server, admin, http.MethodPost, "/api/admin/deployment-credentials", body)
		if response.Code != http.StatusBadRequest {
			t.Errorf("invalid request accepted: %d", response.Code)
		}
	}
	_, token := createDeploymentViaAPI(t, server, admin, map[string]any{"name": "Test"})
	for _, body := range []map[string]any{
		{"device_id": ""}, {"device_id": "phone", "ttl_seconds": -1},
		{"device_id": "phone", "ttl_seconds": 901}, {"device_id": "phone", "scope": "admin"},
	} {
		response := deploymentAPI(t, server, token, http.MethodPost, "/api/deploy/enrollment-tokens", body)
		if response.Code != http.StatusBadRequest {
			t.Errorf("invalid enrollment request accepted: %d", response.Code)
		}
	}
	broken := &PostgresStore{inner: server.store.(*MemoryStore), healthErr: errors.New("database unavailable")}
	if _, raw, err := broken.CreateDeploymentCredential("No database", "admin", 0); err == nil || raw != "" {
		t.Fatal("unavailable store disclosed a new secret")
	}
	if enrollment, err := broken.IssueDeploymentEnrollment(token, "phone", time.Minute); err == nil || enrollment.Token != "" {
		t.Fatal("unavailable store issued enrollment")
	}
}
