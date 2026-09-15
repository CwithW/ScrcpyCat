package controlplane

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
	ErrInvalidAuth   = errors.New("invalid credentials")
	ErrForbidden     = errors.New("forbidden")
	ErrInvalidToken  = errors.New("invalid enrollment token")
)

// Store is intentionally synchronous at the HTTP boundary. Production uses a
// PostgreSQL-backed implementation; MemoryStore remains a deterministic test
// double and is never selected by LoadConfigFromEnv/NewServer.
type Store interface {
	CreateUser(string, string, Role, []string) (User, error)
	Authenticate(string, string) (User, error)
	User(string) (User, error)
	Users() []User
	CanAccessDevice(User, string) bool
	UpsertAgentDevice(string, DeviceInfo) Device
	MarkOffline(string)
	TouchDevice(string)
	DevicesFor(User) []Device
	Device(string) (Device, error)
	DeleteOfflineDevice(string) error
	DeleteUser(string) error
	UpdateUser(string, func(*User) error) (User, error)
	RenameUser(string, string) (User, error)
	ResetPassword(string, string) error
	RevokeUserToken(string, time.Time) error
	UserTokenRevoked(string) bool
	CreateShare(string, string, string, string, bool, *time.Time) (Share, error)
	Share(string) (Share, error)
	UpdateShare(string, func(*Share) error) (Share, error)
	ShareByCard(string) (Share, error)
	SharesFor(string) []Share
	RevokeShare(string) error
	AuthorizeShare(string, string) (Share, error)
	Tags() ([]Tag, map[string][]string)
	ReplaceTags([]Tag, map[string][]string)
	DefaultSettings() map[string]any
	SetDefaultSettings(map[string]any)
	DeviceSettings(string) map[string]any
	SetDeviceSettings(string, map[string]any) error
	DeploymentCredentials() []DeploymentCredential
	CreateDeploymentCredential(string, string, time.Duration) (DeploymentCredential, string, error)
	RevokeDeploymentCredential(string, string) (DeploymentCredential, error)
	IssueDeploymentEnrollment(string, string, time.Duration) (Enrollment, error)
	CreateEnrollment(string, time.Duration) Enrollment
	ExchangeEnrollment(string, string) (AgentCredential, error)
	ValidateAgentToken(string, string) bool
	Shortcuts(string) []Shortcut
	ReplaceShortcuts(string, []Shortcut)
	CreateAsset(Asset) (Asset, error)
	Assets() []Asset
	AssetByName(string) (Asset, error)
	Asset(string) (Asset, error)
	CreateTask(Task) (Task, error)
	Task(string) (Task, error)
	TasksFor(User) []Task
	PendingTasks(string) []string
	ClaimTask(string, string) (Task, error)
	SetTaskStatus(string, string, TaskStatus, string) (Task, error)
	MarkLeasedUnknown(string)
	Audit(AuditEvent)
	Clean(time.Time)
	Healthy() error
	Close()
}

type MemoryStore struct {
	mu                    sync.RWMutex
	users                 map[string]User
	usersByName           map[string]string
	devices               map[string]Device
	tags                  map[string]Tag
	deviceTags            map[string]map[string]struct{}
	shares                map[string]Share
	deploymentCredentials map[string]storedDeploymentCredential
	enrollments           map[string]Enrollment
	agentCredentials      map[string]AgentCredential
	defaultSettings       map[string]any
	deviceSettings        map[string]map[string]any
	shortcuts             map[string][]Shortcut
	assets                map[string]Asset
	assetsByName          map[string]string
	tasks                 map[string]Task
	audits                []AuditEvent
	healthErr             error
	revokedUserTokens     map[string]time.Time
}

func NewMemoryStore(adminUsername, adminPassword string) (*MemoryStore, error) {
	store := &MemoryStore{
		revokedUserTokens:     make(map[string]time.Time),
		deviceSettings:        make(map[string]map[string]any),
		users:                 make(map[string]User),
		usersByName:           make(map[string]string),
		devices:               make(map[string]Device),
		tags:                  make(map[string]Tag),
		deviceTags:            make(map[string]map[string]struct{}),
		shares:                make(map[string]Share),
		deploymentCredentials: make(map[string]storedDeploymentCredential),
		enrollments:           make(map[string]Enrollment),
		agentCredentials:      make(map[string]AgentCredential),
		defaultSettings: map[string]any{
			"connectionStayAwake": true,
			"previewStayAwake":    false,
			"showStats":           false,
			"maxBitrate":          4,
			"minBitrate":          1,
			"fps":                 30,
			"size":                1080,
			"bitrate":             4,
		},
		shortcuts:    make(map[string][]Shortcut),
		assets:       make(map[string]Asset),
		assetsByName: make(map[string]string),
		tasks:        make(map[string]Task),
	}
	if _, err := store.CreateUser(adminUsername, adminPassword, RoleAdmin, nil); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *MemoryStore) Healthy() error { s.mu.RLock(); defer s.mu.RUnlock(); return s.healthErr }
func (s *MemoryStore) Close()         {}

func (s *MemoryStore) CreateUser(username, password string, role Role, assigned []string) (User, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return User{}, ErrInvalidAuth
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.usersByName[username]; ok {
		return User{}, ErrAlreadyExists
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	user := User{ID: randomID(), Username: username, PasswordHash: hash, Role: role, AssignedDevices: append([]string(nil), assigned...), CreatedAt: time.Now().UTC()}
	s.users[user.ID] = user
	s.usersByName[user.Username] = user.ID
	return sanitizeUser(user), nil
}

func (s *MemoryStore) Authenticate(username, password string) (User, error) {
	s.mu.RLock()
	id := s.usersByName[username]
	user, ok := s.users[id]
	s.mu.RUnlock()
	if !ok || (user.ExpiresAt != nil && time.Now().UTC().After(*user.ExpiresAt)) || bcrypt.CompareHashAndPassword(user.PasswordHash, []byte(password)) != nil {
		return User{}, ErrInvalidAuth
	}
	return sanitizeUser(user), nil
}

func (s *MemoryStore) User(id string) (User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	user, ok := s.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	return sanitizeUser(user), nil
}

func (s *MemoryStore) Users() []User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	users := make([]User, 0, len(s.users))
	for _, user := range s.users {
		users = append(users, sanitizeUser(user))
	}
	sort.Slice(users, func(i, j int) bool { return users[i].Username < users[j].Username })
	return users
}

func (s *MemoryStore) CanAccessDevice(user User, deviceID string) bool {
	if user.Role == RoleAdmin {
		return true
	}
	for _, id := range user.AssignedDevices {
		if id == "*" || id == deviceID {
			return true
		}
	}
	return false
}

func (s *MemoryStore) UpsertAgentDevice(id string, info DeviceInfo) Device {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	device, exists := s.devices[id]
	if !exists {
		device = Device{ID: id, FirstSeen: now}
	}
	device.Info = info
	device.Online = true
	device.LastSeen = now
	s.devices[id] = device
	return device
}

func (s *MemoryStore) MarkOffline(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if device, ok := s.devices[id]; ok {
		device.Online = false
		device.ClientCount = 0
		device.LastSeen = time.Now().UTC()
		s.devices[id] = device
	}
}

func (s *MemoryStore) TouchDevice(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if device, ok := s.devices[id]; ok {
		device.LastSeen = time.Now().UTC()
		device.Online = true
		s.devices[id] = device
	}
}

func (s *MemoryStore) DevicesFor(user User) []Device {
	s.mu.RLock()
	defer s.mu.RUnlock()
	devices := make([]Device, 0, len(s.devices))
	for _, device := range s.devices {
		if s.canAccessLocked(user, device.ID) {
			devices = append(devices, device)
		}
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].ID < devices[j].ID })
	return devices
}

func (s *MemoryStore) Device(id string) (Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	device, ok := s.devices[id]
	if !ok {
		return Device{}, ErrNotFound
	}
	return device, nil
}

func (s *MemoryStore) DeleteOfflineDevice(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	device, ok := s.devices[id]
	if !ok {
		return ErrNotFound
	}
	if device.Online {
		return ErrForbidden
	}
	delete(s.devices, id)
	delete(s.deviceTags, id)
	delete(s.deviceSettings, id)
	return nil
}

func (s *MemoryStore) DeleteUser(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.usersByName[username]
	if !ok {
		return ErrNotFound
	}
	if s.users[id].Role == RoleAdmin {
		admins := 0
		for _, user := range s.users {
			if user.Role == RoleAdmin {
				admins++
			}
		}
		if admins <= 1 {
			return ErrForbidden
		}
	}
	delete(s.users, id)
	delete(s.usersByName, username)
	return nil
}

func (s *MemoryStore) UpdateUser(username string, update func(*User) error) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.usersByName[username]
	if !ok {
		return User{}, ErrNotFound
	}
	user := s.users[id]
	if err := update(&user); err != nil {
		return User{}, err
	}
	s.users[id] = user
	return sanitizeUser(user), nil
}

func (s *MemoryStore) RenameUser(oldUsername, newUsername string) (User, error) {
	newUsername = strings.TrimSpace(newUsername)
	if newUsername == "" {
		return User{}, ErrInvalidAuth
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.usersByName[oldUsername]
	if !ok {
		return User{}, ErrNotFound
	}
	if existing, exists := s.usersByName[newUsername]; exists && existing != id {
		return User{}, ErrAlreadyExists
	}
	user := s.users[id]
	delete(s.usersByName, oldUsername)
	user.Username = newUsername
	s.users[id] = user
	s.usersByName[newUsername] = id
	return sanitizeUser(user), nil
}

func (s *MemoryStore) ResetPassword(username, password string) error {
	if password == "" {
		return ErrInvalidAuth
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.UpdateUser(username, func(user *User) error {
		user.PasswordHash = hash
		user.TokenVersion++
		return nil
	})
	return err
}

func (s *MemoryStore) CreateShare(deviceID, createdBy, password, description string, viewOnly bool, expiresAt *time.Time) (Share, error) {
	share := Share{Token: randomToken(), CardCode: randomToken()[:10], DeviceID: deviceID, ViewOnly: viewOnly, Description: description, ExpiresAt: expiresAt, CreatedBy: createdBy, CreatedAt: time.Now().UTC()}
	if password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return Share{}, err
		}
		share.PasswordHash = hash
	}
	s.mu.Lock()
	s.shares[share.Token] = share
	s.mu.Unlock()
	return share, nil
}

func (s *MemoryStore) SharesFor(deviceID string) []Share {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	shares := make([]Share, 0)
	for token, share := range s.shares {
		if share.ExpiresAt != nil && now.After(*share.ExpiresAt) {
			delete(s.shares, token)
			continue
		}
		if deviceID == "" || share.DeviceID == deviceID {
			shares = append(shares, share)
		}
	}
	sort.Slice(shares, func(i, j int) bool { return shares[i].CreatedAt.After(shares[j].CreatedAt) })
	return shares
}

func (s *MemoryStore) Share(token string) (Share, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	share, ok := s.shares[token]
	if !ok {
		return Share{}, ErrNotFound
	}
	return share, nil
}

func (s *MemoryStore) UpdateShare(token string, update func(*Share) error) (Share, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	share, ok := s.shares[token]
	if !ok {
		return Share{}, ErrNotFound
	}
	if err := update(&share); err != nil {
		return Share{}, err
	}
	s.shares[token] = share
	return share, nil
}

func (s *MemoryStore) ShareByCard(card string) (Share, error) {
	for _, share := range s.SharesFor("") {
		if share.CardCode == card {
			return share, nil
		}
	}
	return Share{}, ErrNotFound
}

func (s *MemoryStore) RevokeShare(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.shares[token]; !ok {
		return ErrNotFound
	}
	delete(s.shares, token)
	return nil
}

func (s *MemoryStore) AuthorizeShare(token, password string) (Share, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	share, ok := s.shares[token]
	if !ok {
		return Share{}, ErrNotFound
	}
	if share.ExpiresAt != nil && time.Now().UTC().After(*share.ExpiresAt) {
		delete(s.shares, token)
		return Share{}, ErrNotFound
	}
	if len(share.PasswordHash) > 0 && bcrypt.CompareHashAndPassword(share.PasswordHash, []byte(password)) != nil {
		return Share{}, ErrInvalidAuth
	}
	return share, nil
}

func (s *MemoryStore) Tags() ([]Tag, map[string][]string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tags := make([]Tag, 0, len(s.tags))
	for _, tag := range s.tags {
		tags = append(tags, tag)
	}
	sort.Slice(tags, func(i, j int) bool { return tags[i].Name < tags[j].Name })
	assignments := make(map[string][]string, len(s.deviceTags))
	for deviceID, set := range s.deviceTags {
		for tagID := range set {
			assignments[deviceID] = append(assignments[deviceID], tagID)
		}
		sort.Strings(assignments[deviceID])
	}
	return tags, assignments
}

func (s *MemoryStore) ReplaceTags(tags []Tag, assignments map[string][]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tags = make(map[string]Tag, len(tags))
	for _, tag := range tags {
		if tag.ID == "" {
			tag.ID = randomID()
		}
		s.tags[tag.ID] = tag
	}
	s.deviceTags = make(map[string]map[string]struct{}, len(assignments))
	for deviceID, ids := range assignments {
		set := make(map[string]struct{}, len(ids))
		for _, id := range ids {
			if _, ok := s.tags[id]; ok {
				set[id] = struct{}{}
			}
		}
		s.deviceTags[deviceID] = set
	}
}

func (s *MemoryStore) DefaultSettings() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneMap(s.defaultSettings)
}

func (s *MemoryStore) SetDefaultSettings(settings map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defaultSettings = cloneMap(settings)
}

func (s *MemoryStore) CreateEnrollment(deviceID string, ttl time.Duration) Enrollment {
	enrollment := Enrollment{Token: randomToken(), DeviceID: deviceID, ExpiresAt: time.Now().UTC().Add(ttl)}
	s.mu.Lock()
	persisted := enrollment
	persisted.Token = ""
	s.enrollments[tokenDigest(enrollment.Token)] = persisted
	s.mu.Unlock()
	return enrollment
}

func (s *MemoryStore) ExchangeEnrollment(token, deviceID string) (AgentCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	enrollment, ok := s.enrollments[tokenDigest(token)]
	now := time.Now().UTC()
	if !ok || !now.Before(enrollment.ExpiresAt) || (enrollment.DeviceID != "" && enrollment.DeviceID != deviceID) {
		return AgentCredential{}, ErrInvalidToken
	}
	if enrollment.DeploymentCredentialID != "" {
		issuer, exists := s.deploymentCredentials[enrollment.DeploymentCredentialID]
		if !exists || !issuer.active(now) {
			return AgentCredential{}, ErrInvalidToken
		}
	}
	delete(s.enrollments, tokenDigest(token))
	credential := AgentCredential{Token: randomToken(), DeviceID: deviceID}
	persisted := credential
	persisted.Token = ""
	s.agentCredentials[tokenDigest(credential.Token)] = persisted
	return credential, nil
}

func (s *MemoryStore) ValidateAgentToken(token, deviceID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	credential, ok := s.agentCredentials[tokenDigest(token)]
	return ok && credential.DeviceID == deviceID
}

func (s *MemoryStore) Shortcuts(userID string) []Shortcut {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Shortcut(nil), s.shortcuts[userID]...)
}

func (s *MemoryStore) ReplaceShortcuts(userID string, shortcuts []Shortcut) {
	s.mu.Lock()
	defer s.mu.Unlock()
	clean := make([]Shortcut, 0, len(shortcuts))
	for _, shortcut := range shortcuts {
		shortcut.Name = strings.TrimSpace(shortcut.Name)
		shortcut.Cmd = strings.TrimSpace(shortcut.Cmd)
		if shortcut.Name != "" && shortcut.Cmd != "" {
			clean = append(clean, shortcut)
		}
	}
	s.shortcuts[userID] = clean
}

func (s *MemoryStore) CreateAsset(asset Asset) (Asset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if asset.ID == "" {
		asset.ID = randomID()
	}
	if asset.Name == "" {
		return Asset{}, ErrNotFound
	}
	if _, ok := s.assetsByName[asset.Name]; ok {
		return Asset{}, ErrAlreadyExists
	}
	if asset.CreatedAt.IsZero() {
		asset.CreatedAt = time.Now().UTC()
	}
	s.assets[asset.ID] = asset
	s.assetsByName[asset.Name] = asset.ID
	return asset, nil
}

func (s *MemoryStore) Assets() []Asset {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Asset, 0, len(s.assets))
	for _, asset := range s.assets {
		result = append(result, asset)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result
}

func (s *MemoryStore) AssetByName(name string) (Asset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id := s.assetsByName[name]
	asset, ok := s.assets[id]
	if !ok {
		return Asset{}, ErrNotFound
	}
	return asset, nil
}

func (s *MemoryStore) Asset(id string) (Asset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	asset, ok := s.assets[id]
	if !ok {
		return Asset{}, ErrNotFound
	}
	return asset, nil
}

func (s *MemoryStore) CreateTask(task Task) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if task.ID == "" {
		task.ID = randomID()
	}
	if task.Type == "" || len(task.Devices) == 0 {
		return Task{}, ErrNotFound
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now().UTC()
	}
	for id, device := range task.Devices {
		device.DeviceID = id
		device.Status = TaskQueued
		device.UpdatedAt = task.CreatedAt
		task.Devices[id] = device
	}
	s.tasks[task.ID] = task
	return cloneTask(task), nil
}

func (s *MemoryStore) Task(id string) (Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.tasks[id]
	if !ok {
		return Task{}, ErrNotFound
	}
	return cloneTask(task), nil
}

func (s *MemoryStore) TasksFor(user User) []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Task, 0, len(s.tasks))
	for _, task := range s.tasks {
		if task.CreatedBy == user.Username || s.canAccessAllTaskDevicesLocked(user, task) {
			result = append(result, cloneTask(task))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result
}

func (s *MemoryStore) PendingTasks(deviceID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now().UTC()
	ids := make([]string, 0)
	for id, task := range s.tasks {
		if now.After(task.ExpiresAt) {
			continue
		}
		if device, ok := task.Devices[deviceID]; ok && device.Status == TaskQueued {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func (s *MemoryStore) ClaimTask(taskID, deviceID string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[taskID]
	if !ok {
		return Task{}, ErrNotFound
	}
	if !task.ExpiresAt.IsZero() && time.Now().UTC().After(task.ExpiresAt) {
		return Task{}, ErrInvalidToken
	}
	device, ok := task.Devices[deviceID]
	if !ok || device.Status != TaskQueued {
		return Task{}, ErrForbidden
	}
	device.Status = TaskLeased
	device.UpdatedAt = time.Now().UTC()
	task.Devices[deviceID] = device
	s.tasks[taskID] = task
	return cloneTask(task), nil
}

func (s *MemoryStore) SetTaskStatus(taskID, deviceID string, status TaskStatus, result string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[taskID]
	if !ok {
		return Task{}, ErrNotFound
	}
	device, ok := task.Devices[deviceID]
	if !ok {
		return Task{}, ErrNotFound
	}
	device.Status, device.Result, device.UpdatedAt = status, result, time.Now().UTC()
	task.Devices[deviceID] = device
	s.tasks[taskID] = task
	return cloneTask(task), nil
}

func (s *MemoryStore) MarkLeasedUnknown(deviceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, task := range s.tasks {
		if device, ok := task.Devices[deviceID]; ok && (device.Status == TaskLeased || device.Status == TaskRunning) {
			device.Status, device.Result, device.UpdatedAt = TaskUnknown, "agent disconnected after claim", time.Now().UTC()
			task.Devices[deviceID] = device
			s.tasks[id] = task
		}
	}
}

func (s *MemoryStore) canAccessAllTaskDevicesLocked(user User, task Task) bool {
	if user.Role == RoleAdmin {
		return true
	}
	if !user.Can("batch_tasks") {
		return false
	}
	for id := range task.Devices {
		allowed := false
		for _, assigned := range user.AssignedDevices {
			if assigned == "*" || assigned == id {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	return true
}

func (s *MemoryStore) Audit(event AuditEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditLocked(event)
}

func (s *MemoryStore) auditLocked(event AuditEvent) {
	if event.ID == "" {
		event.ID = randomID()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	s.audits = append(s.audits, event)
}

func (s *MemoryStore) Clean(before time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for digest, expires := range s.revokedUserTokens {
		if !time.Now().Before(expires) {
			delete(s.revokedUserTokens, digest)
		}
	}
	for digest, enrollment := range s.enrollments {
		if !time.Now().UTC().Before(enrollment.ExpiresAt) {
			delete(s.enrollments, digest)
		}
	}
	for id, task := range s.tasks {
		if !task.ExpiresAt.IsZero() && time.Now().UTC().After(task.ExpiresAt) {
			for devID, device := range task.Devices {
				if device.Status == TaskQueued {
					device.Status = TaskExpired
					device.UpdatedAt = time.Now().UTC()
					task.Devices[devID] = device
				}
			}
			s.tasks[id] = task
		}
	}
	kept := s.audits[:0]
	for _, event := range s.audits {
		if event.CreatedAt.After(before) {
			kept = append(kept, event)
		}
	}
	s.audits = kept
}

func (s *MemoryStore) canAccessLocked(user User, deviceID string) bool {
	if user.Role == RoleAdmin {
		return true
	}
	for _, id := range user.AssignedDevices {
		if id == "*" || id == deviceID {
			return true
		}
	}
	return false
}

func sanitizeUser(user User) User {
	user.PasswordHash = nil
	user.AssignedDevices = append([]string(nil), user.AssignedDevices...)
	return user
}

func cloneMap(source map[string]any) map[string]any {
	copy := make(map[string]any, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}

func cloneTask(task Task) Task {
	devices := make(map[string]TaskDevice, len(task.Devices))
	for id, device := range task.Devices {
		devices[id] = device
	}
	task.Devices = devices
	return task
}

func randomID() string { return randomToken()[:16] }

func randomToken() string {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func tokenDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
