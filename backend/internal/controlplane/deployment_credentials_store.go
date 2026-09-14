package controlplane

import (
	"crypto/subtle"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const deploymentCredentialScope = "agent:enroll"
const maxDeploymentEnrollmentTTL = 15 * time.Minute

var errInvalidDeploymentRequest = errors.New("invalid deployment credential request")

// The durable record is separate from the API model so hashes cannot leak in lists.
type storedDeploymentCredential struct {
	DeploymentCredential
	TokenHash string `json:"token_hash"`
}

func (c storedDeploymentCredential) active(now time.Time) bool {
	return c.Scope == deploymentCredentialScope && c.RevokedAt == nil &&
		(c.ExpiresAt == nil || now.Before(*c.ExpiresAt))
}

func copyTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func copyDeploymentCredential(value DeploymentCredential) DeploymentCredential {
	value.ExpiresAt = copyTime(value.ExpiresAt)
	value.RevokedAt = copyTime(value.RevokedAt)
	value.LastUsedAt = copyTime(value.LastUsedAt)
	return value
}

func (s *MemoryStore) DeploymentCredentials() []DeploymentCredential {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]DeploymentCredential, 0, len(s.deploymentCredentials))
	for _, saved := range s.deploymentCredentials {
		result = append(result, copyDeploymentCredential(saved.DeploymentCredential))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result
}

// A zero TTL deliberately means no expiration, as requested for unattended deployment.
func (s *MemoryStore) CreateDeploymentCredential(name, actor string, ttl time.Duration) (DeploymentCredential, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 80 || ttl < 0 {
		return DeploymentCredential{}, "", errInvalidDeploymentRequest
	}
	now := time.Now().UTC()
	credential := DeploymentCredential{
		ID: randomID(), Name: name, Scope: deploymentCredentialScope,
		CreatedBy: actor, CreatedAt: now,
	}
	if ttl > 0 {
		expiresAt := now.Add(ttl)
		credential.ExpiresAt = &expiresAt
	}
	token := "scd." + credential.ID + "." + randomToken()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deploymentCredentials[credential.ID] = storedDeploymentCredential{
		DeploymentCredential: credential, TokenHash: tokenDigest(token),
	}
	s.auditLocked(AuditEvent{
		Actor: actor, Action: "deployment.credential.create", Target: credential.ID,
		Metadata: map[string]any{"name": name, "scope": credential.Scope, "expires_at": copyTime(credential.ExpiresAt)},
	})
	return copyDeploymentCredential(credential), token, nil
}

func (s *MemoryStore) RevokeDeploymentCredential(id, actor string) (DeploymentCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	saved, exists := s.deploymentCredentials[id]
	if !exists {
		return DeploymentCredential{}, ErrNotFound
	}
	if saved.RevokedAt == nil {
		now := time.Now().UTC()
		saved.RevokedAt = &now
		s.deploymentCredentials[id] = saved
		s.auditLocked(AuditEvent{Actor: actor, Action: "deployment.credential.revoke", Target: id})
	}
	return copyDeploymentCredential(saved.DeploymentCredential), nil
}

func (s *MemoryStore) IssueDeploymentEnrollment(token, deviceID string, ttl time.Duration) (Enrollment, error) {
	if strings.TrimSpace(deviceID) == "" || len(deviceID) > 128 || strings.ContainsAny(deviceID, "\x00\r\n") ||
		ttl <= 0 || ttl > maxDeploymentEnrollmentTTL {
		return Enrollment{}, errInvalidDeploymentRequest
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "scd" {
		return Enrollment{}, ErrInvalidAuth
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	saved, exists := s.deploymentCredentials[parts[1]]
	if !exists || !saved.active(now) ||
		subtle.ConstantTimeCompare([]byte(saved.TokenHash), []byte(tokenDigest(token))) != 1 {
		return Enrollment{}, ErrInvalidAuth
	}
	expiresAt := now.Add(ttl)
	if saved.ExpiresAt != nil && saved.ExpiresAt.Before(expiresAt) {
		expiresAt = *saved.ExpiresAt
	}
	enrollment := Enrollment{
		Token: randomToken(), DeviceID: deviceID, ExpiresAt: expiresAt, DeploymentCredentialID: saved.ID,
	}
	persisted := enrollment
	persisted.Token = ""
	s.enrollments[tokenDigest(enrollment.Token)] = persisted
	saved.LastUsedAt = &now
	s.deploymentCredentials[saved.ID] = saved
	s.auditLocked(AuditEvent{
		Actor: "deployment:" + saved.ID, Action: "deployment.enrollment.create", Target: deviceID,
		Metadata: map[string]any{"credential_id": saved.ID, "expires_at": expiresAt},
	})
	return enrollment, nil
}

func (s *PostgresStore) DeploymentCredentials() []DeploymentCredential {
	return s.inner.DeploymentCredentials()
}

// Credential changes and their audit events share the durable state update.
// Never report success or disclose a secret before that update is acknowledged.
func (s *PostgresStore) CreateDeploymentCredential(name, actor string, ttl time.Duration) (DeploymentCredential, string, error) {
	if err := s.Healthy(); err != nil {
		return DeploymentCredential{}, "", err
	}
	credential, token, err := s.inner.CreateDeploymentCredential(name, actor, ttl)
	if err == nil {
		err = s.persist()
	}
	if err != nil {
		return DeploymentCredential{}, "", err
	}
	return credential, token, nil
}

func (s *PostgresStore) RevokeDeploymentCredential(id, actor string) (DeploymentCredential, error) {
	credential, err := s.inner.RevokeDeploymentCredential(id, actor)
	if err == nil {
		err = s.persist()
	}
	if err != nil {
		return DeploymentCredential{}, err
	}
	return credential, nil
}

func (s *PostgresStore) IssueDeploymentEnrollment(token, deviceID string, ttl time.Duration) (Enrollment, error) {
	if err := s.Healthy(); err != nil {
		return Enrollment{}, err
	}
	enrollment, err := s.inner.IssueDeploymentEnrollment(token, deviceID, ttl)
	if err == nil {
		err = s.persist()
	}
	if err != nil {
		return Enrollment{}, err
	}
	return enrollment, nil
}
