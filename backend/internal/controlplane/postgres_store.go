package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// PostgresStore keeps one transactionally updated state document. The document
// is deliberately an implementation detail: callers use Store, while schema
// migrations and the database row provide durable, restart-safe state today.
// It keeps the service small without reintroducing process-local persistence.
type PostgresStore struct {
	inner     *MemoryStore
	pool      *pgxpool.Pool
	persistMu sync.Mutex
	healthMu  sync.RWMutex
	healthErr error
}

type persistedState struct {
	Users                 map[string]persistedUser              `json:"users"`
	UsersByName           map[string]string                     `json:"users_by_name"`
	Devices               map[string]Device                     `json:"devices"`
	Tags                  map[string]Tag                        `json:"tags"`
	DeviceTags            map[string]map[string]struct{}        `json:"device_tags"`
	Shares                map[string]Share                      `json:"shares"`
	DeploymentCredentials map[string]storedDeploymentCredential `json:"deployment_credentials"`
	Enrollments           map[string]Enrollment                 `json:"enrollments"`
	AgentCredentials      map[string]AgentCredential            `json:"agent_credentials"`
	DefaultSettings       map[string]any                        `json:"default_settings"`
	DeviceSettings        map[string]map[string]any             `json:"device_settings,omitempty"`
	Shortcuts             map[string][]Shortcut                 `json:"shortcuts"`
	Assets                map[string]Asset                      `json:"assets"`
	AssetsByName          map[string]string                     `json:"assets_by_name"`
	Tasks                 map[string]Task                       `json:"tasks"`
	Audits                []AuditEvent                          `json:"audits"`
	RevokedUserTokens     map[string]time.Time                  `json:"revoked_user_tokens,omitempty"`
}

// persistedUser is intentionally separate from the API model. User.PasswordHash
// must never appear in HTTP responses, but it is required to restore accounts
// from the durable state document.
type persistedUser struct {
	User
	PasswordHash []byte `json:"password_hash"`
	TokenVersion uint64 `json:"token_version"`
}

func NewPostgresStore(ctx context.Context, dsn, adminUsername, adminPassword string) (*PostgresStore, error) {
	if dsn == "" {
		return nil, errors.New("SCRCPYCAT_POSTGRES_DSN is required")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	store := &PostgresStore{pool: pool}
	if err := store.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	inner, err := NewMemoryStore(adminUsername, adminPassword)
	if err != nil {
		pool.Close()
		return nil, err
	}
	store.inner = inner
	if err := store.load(ctx, adminUsername, adminPassword); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func (s *PostgresStore) migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, "SELECT pg_advisory_lock(hashtext('scrcpycat-migrations'))"); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	defer func() {
		_, _ = s.pool.Exec(context.Background(), "SELECT pg_advisory_unlock(hashtext('scrcpycat-migrations'))")
	}()
	statements := []string{
		`CREATE TABLE IF NOT EXISTS scrcpycat_schema_migrations (version integer PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`,
		`CREATE TABLE IF NOT EXISTS scrcpycat_state (singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton), state jsonb NOT NULL, updated_at timestamptz NOT NULL DEFAULT now())`,
	}
	for _, statement := range statements {
		if _, err := s.pool.Exec(ctx, statement); err != nil {
			return fmt.Errorf("apply migration: %w", err)
		}
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO scrcpycat_schema_migrations(version) VALUES (1) ON CONFLICT (version) DO NOTHING`); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	return nil
}

func (s *PostgresStore) load(ctx context.Context, adminUsername, adminPassword string) error {
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT state FROM scrcpycat_state WHERE singleton = true`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return s.persist()
	}
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	var state persistedState
	if err := json.Unmarshal(raw, &state); err != nil {
		return fmt.Errorf("decode persisted state: %w", err)
	}
	users := make(map[string]User, len(state.Users))
	repaired := false
	for id, saved := range state.Users {
		user := saved.User
		user.PasswordHash = append([]byte(nil), saved.PasswordHash...)
		user.TokenVersion = saved.TokenVersion
		if len(user.PasswordHash) == 0 && user.Username == adminUsername {
			hash, hashErr := bcrypt.GenerateFromPassword([]byte(adminPassword), bcrypt.DefaultCost)
			if hashErr != nil {
				return fmt.Errorf("repair bootstrap administrator: %w", hashErr)
			}
			user.PasswordHash = hash
			repaired = true
		}
		users[id] = user
	}
	s.inner.mu.Lock()
	s.inner.users = users
	s.inner.usersByName = nonNil(state.UsersByName)
	s.inner.devices = nonNil(state.Devices)
	s.inner.tags = nonNil(state.Tags)
	s.inner.deviceTags = nonNil(state.DeviceTags)
	s.inner.shares = nonNil(state.Shares)
	s.inner.deploymentCredentials = nonNil(state.DeploymentCredentials)
	s.inner.enrollments = nonNil(state.Enrollments)
	s.inner.agentCredentials = nonNil(state.AgentCredentials)
	s.inner.defaultSettings = nonNil(state.DefaultSettings)
	s.inner.deviceSettings = nonNil(state.DeviceSettings)
	s.inner.shortcuts = nonNil(state.Shortcuts)
	s.inner.assets = nonNil(state.Assets)
	s.inner.assetsByName = nonNil(state.AssetsByName)
	s.inner.tasks = nonNil(state.Tasks)
	s.inner.audits = state.Audits
	s.inner.revokedUserTokens = nonNil(state.RevokedUserTokens)
	s.inner.mu.Unlock()
	if repaired {
		return s.persist()
	}
	return nil
}

func nonNil[T any](value map[string]T) map[string]T {
	if value == nil {
		return make(map[string]T)
	}
	return value
}

func (s *PostgresStore) snapshotJSON() ([]byte, error) {
	s.inner.mu.RLock()
	defer s.inner.mu.RUnlock()
	users := make(map[string]persistedUser, len(s.inner.users))
	for id, user := range s.inner.users {
		users[id] = persistedUser{User: user, PasswordHash: append([]byte(nil), user.PasswordHash...), TokenVersion: user.TokenVersion}
	}
	return json.Marshal(persistedState{
		Users: users, UsersByName: s.inner.usersByName, Devices: s.inner.devices,
		Tags: s.inner.tags, DeviceTags: s.inner.deviceTags, Shares: s.inner.shares,
		DeploymentCredentials: s.inner.deploymentCredentials,
		Enrollments:           s.inner.enrollments, AgentCredentials: s.inner.agentCredentials,
		DefaultSettings: s.inner.defaultSettings, Shortcuts: s.inner.shortcuts,
		DeviceSettings: s.inner.deviceSettings,
		Assets:         s.inner.assets, AssetsByName: s.inner.assetsByName, Tasks: s.inner.tasks,
		Audits: s.inner.audits, RevokedUserTokens: s.inner.revokedUserTokens,
	})
}

func (s *PostgresStore) persist() error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	raw, err := s.snapshotJSON()
	if err == nil {
		_, err = s.pool.Exec(context.Background(), `INSERT INTO scrcpycat_state(singleton, state, updated_at) VALUES (true, $1::jsonb, now()) ON CONFLICT (singleton) DO UPDATE SET state = EXCLUDED.state, updated_at = now()`, raw)
	}
	s.healthMu.Lock()
	s.healthErr = err
	s.healthMu.Unlock()
	return err
}

func (s *PostgresStore) mutate() { _ = s.persist() }
func (s *PostgresStore) Healthy() error {
	s.healthMu.RLock()
	defer s.healthMu.RUnlock()
	return s.healthErr
}
func (s *PostgresStore) Close() { s.pool.Close() }

func (s *PostgresStore) CreateUser(a, b string, c Role, d []string) (User, error) {
	v, e := s.inner.CreateUser(a, b, c, d)
	if e == nil {
		s.mutate()
	}
	return v, e
}
func (s *PostgresStore) Authenticate(a, b string) (User, error) { return s.inner.Authenticate(a, b) }
func (s *PostgresStore) User(a string) (User, error)            { return s.inner.User(a) }
func (s *PostgresStore) Users() []User                          { return s.inner.Users() }
func (s *PostgresStore) CanAccessDevice(a User, b string) bool  { return s.inner.CanAccessDevice(a, b) }
func (s *PostgresStore) UpsertAgentDevice(a string, b DeviceInfo) Device {
	v := s.inner.UpsertAgentDevice(a, b)
	s.mutate()
	return v
}
func (s *PostgresStore) MarkOffline(a string)            { s.inner.MarkOffline(a); s.mutate() }
func (s *PostgresStore) TouchDevice(a string)            { s.inner.TouchDevice(a); s.mutate() }
func (s *PostgresStore) DevicesFor(a User) []Device      { return s.inner.DevicesFor(a) }
func (s *PostgresStore) Device(a string) (Device, error) { return s.inner.Device(a) }
func (s *PostgresStore) DeleteOfflineDevice(a string) error {
	e := s.inner.DeleteOfflineDevice(a)
	if e == nil {
		s.mutate()
	}
	return e
}
func (s *PostgresStore) DeleteUser(a string) error {
	e := s.inner.DeleteUser(a)
	if e == nil {
		s.mutate()
	}
	return e
}
func (s *PostgresStore) UpdateUser(a string, b func(*User) error) (User, error) {
	v, e := s.inner.UpdateUser(a, b)
	if e == nil {
		s.mutate()
	}
	return v, e
}
func (s *PostgresStore) RenameUser(a, b string) (User, error) {
	v, e := s.inner.RenameUser(a, b)
	if e == nil {
		s.mutate()
	}
	return v, e
}
func (s *PostgresStore) ResetPassword(a, b string) error {
	e := s.inner.ResetPassword(a, b)
	if e == nil {
		s.mutate()
	}
	return e
}
func (s *PostgresStore) CreateShare(a, b, c, d string, e bool, f *time.Time) (Share, error) {
	v, err := s.inner.CreateShare(a, b, c, d, e, f)
	if err == nil {
		s.mutate()
	}
	return v, err
}
func (s *PostgresStore) Share(a string) (Share, error) { return s.inner.Share(a) }
func (s *PostgresStore) UpdateShare(a string, b func(*Share) error) (Share, error) {
	v, e := s.inner.UpdateShare(a, b)
	if e == nil {
		s.mutate()
	}
	return v, e
}
func (s *PostgresStore) ShareByCard(a string) (Share, error) { return s.inner.ShareByCard(a) }
func (s *PostgresStore) SharesFor(a string) []Share          { return s.inner.SharesFor(a) }
func (s *PostgresStore) RevokeShare(a string) error {
	e := s.inner.RevokeShare(a)
	if e == nil {
		s.mutate()
	}
	return e
}
func (s *PostgresStore) AuthorizeShare(a, b string) (Share, error) {
	return s.inner.AuthorizeShare(a, b)
}
func (s *PostgresStore) Tags() ([]Tag, map[string][]string) { return s.inner.Tags() }
func (s *PostgresStore) ReplaceTags(a []Tag, b map[string][]string) {
	s.inner.ReplaceTags(a, b)
	s.mutate()
}
func (s *PostgresStore) DefaultSettings() map[string]any { return s.inner.DefaultSettings() }
func (s *PostgresStore) SetDefaultSettings(a map[string]any) {
	s.inner.SetDefaultSettings(a)
	s.mutate()
}
func (s *PostgresStore) CreateEnrollment(a string, b time.Duration) Enrollment {
	v := s.inner.CreateEnrollment(a, b)
	s.mutate()
	return v
}
func (s *PostgresStore) ExchangeEnrollment(a, b string) (AgentCredential, error) {
	if err := s.Healthy(); err != nil {
		return AgentCredential{}, err
	}
	v, err := s.inner.ExchangeEnrollment(a, b)
	if err == nil {
		err = s.persist()
	}
	if err != nil {
		return AgentCredential{}, err
	}
	return v, nil
}
func (s *PostgresStore) ValidateAgentToken(a, b string) bool { return s.inner.ValidateAgentToken(a, b) }
func (s *PostgresStore) Shortcuts(a string) []Shortcut       { return s.inner.Shortcuts(a) }
func (s *PostgresStore) ReplaceShortcuts(a string, b []Shortcut) {
	s.inner.ReplaceShortcuts(a, b)
	s.mutate()
}
func (s *PostgresStore) CreateAsset(a Asset) (Asset, error) {
	v, e := s.inner.CreateAsset(a)
	if e == nil {
		s.mutate()
	}
	return v, e
}
func (s *PostgresStore) Assets() []Asset                     { return s.inner.Assets() }
func (s *PostgresStore) AssetByName(a string) (Asset, error) { return s.inner.AssetByName(a) }
func (s *PostgresStore) Asset(a string) (Asset, error)       { return s.inner.Asset(a) }
func (s *PostgresStore) CreateTask(a Task) (Task, error) {
	v, e := s.inner.CreateTask(a)
	if e == nil {
		s.mutate()
	}
	return v, e
}
func (s *PostgresStore) Task(a string) (Task, error)    { return s.inner.Task(a) }
func (s *PostgresStore) TasksFor(a User) []Task         { return s.inner.TasksFor(a) }
func (s *PostgresStore) PendingTasks(a string) []string { return s.inner.PendingTasks(a) }
func (s *PostgresStore) ClaimTask(a, b string) (Task, error) {
	v, e := s.inner.ClaimTask(a, b)
	if e == nil {
		s.mutate()
	}
	return v, e
}
func (s *PostgresStore) SetTaskStatus(a, b string, c TaskStatus, d string) (Task, error) {
	v, e := s.inner.SetTaskStatus(a, b, c, d)
	if e == nil {
		s.mutate()
	}
	return v, e
}
func (s *PostgresStore) MarkLeasedUnknown(a string) { s.inner.MarkLeasedUnknown(a); s.mutate() }
func (s *PostgresStore) Audit(a AuditEvent)         { s.inner.Audit(a); s.mutate() }
func (s *PostgresStore) Clean(a time.Time)          { s.inner.Clean(a); s.mutate() }
