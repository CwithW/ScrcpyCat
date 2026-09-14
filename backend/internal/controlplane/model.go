package controlplane

import "time"

type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

type User struct {
	ID               string               `json:"id"`
	Username         string               `json:"username"`
	PasswordHash     []byte               `json:"-"`
	TokenVersion     uint64               `json:"-"`
	Role             Role                 `json:"role"`
	AssignedDevices  []string             `json:"assigned_devices"`
	Note             string               `json:"note,omitempty"`
	ExpiresAt        *time.Time           `json:"expires_at,omitempty"`
	ForbidBitrate    bool                 `json:"forbid_bitrate"`
	ForbidFPS        bool                 `json:"forbid_fps"`
	ForbidResolution bool                 `json:"forbid_resolution"`
	ForbidAudio      bool                 `json:"forbid_audio"`
	Settings         map[string]any       `json:"settings,omitempty"`
	Permissions      OperationPermissions `json:"permissions"`
	CreatedAt        time.Time            `json:"created_at"`
}

// OperationPermissions are deliberately independent from device assignment.
// A user must have both the device assignment and the relevant capability.
type OperationPermissions struct {
	Shell       bool `json:"shell"`
	FileLibrary bool `json:"file_library"`
	BatchTasks  bool `json:"batch_tasks"`
}

func (u User) Can(operation string) bool {
	if u.Role == RoleAdmin {
		return true
	}
	switch operation {
	case "shell":
		return u.Permissions.Shell
	case "file_library":
		return u.Permissions.FileLibrary
	case "batch_tasks":
		return u.Permissions.BatchTasks
	default:
		return false
	}
}

type DeviceInfo struct {
	Model          string          `json:"model,omitempty"`
	ABI            string          `json:"abi,omitempty"`
	SDK            string          `json:"sdk,omitempty"`
	AndroidVersion string          `json:"android_version,omitempty"`
	Displays       []Display       `json:"displays,omitempty"`
	Cameras        []Camera        `json:"cameras,omitempty"`
	Capabilities   map[string]bool `json:"capabilities,omitempty"`
	AgentVersion   string          `json:"agent_version,omitempty"`
	ConnectionMode string          `json:"connection_mode,omitempty"`
}

type Display struct {
	ID   int `json:"id"`
	XRes int `json:"x_res"`
	YRes int `json:"y_res"`
}

type Camera struct {
	ID      string  `json:"id"`
	Facing  string  `json:"facing"`
	XRes    int     `json:"x_res"`
	YRes    int     `json:"y_res"`
	ZoomMin float64 `json:"zoom_min,omitempty"`
	ZoomMax float64 `json:"zoom_max,omitempty"`
}

type Device struct {
	ID          string           `json:"device_id"`
	Info        DeviceInfo       `json:"device_info"`
	Online      bool             `json:"online"`
	FirstSeen   time.Time        `json:"first_seen"`
	LastSeen    time.Time        `json:"last_seen"`
	ClientCount int              `json:"client_count"`
	Clients     []map[string]any `json:"clients,omitempty"`
	SnapshotURL string           `json:"snapshot_url,omitempty"`
	Metrics     map[string]any   `json:"metrics,omitempty"`
}

type Tag struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
	Icon  string `json:"icon"`
}

type Share struct {
	Token            string         `json:"token"`
	CardCode         string         `json:"card_code"`
	DeviceID         string         `json:"device_id"`
	ViewOnly         bool           `json:"view_only"`
	PasswordHash     []byte         `json:"-"`
	Description      string         `json:"description,omitempty"`
	ExpiresAt        *time.Time     `json:"expires_at,omitempty"`
	CreatedBy        string         `json:"created_by"`
	CreatedAt        time.Time      `json:"created_at"`
	ForbidBitrate    bool           `json:"forbid_bitrate"`
	ForbidFPS        bool           `json:"forbid_fps"`
	ForbidResolution bool           `json:"forbid_resolution"`
	ForbidAudio      bool           `json:"forbid_audio"`
	GuestSettings    map[string]any `json:"guest_settings,omitempty"`
}

type Enrollment struct {
	Token                  string
	ExpiresAt              time.Time
	DeviceID               string
	DeploymentCredentialID string `json:",omitempty"`
}

// DeploymentCredential is public metadata. The secret is returned only at creation.
type DeploymentCredential struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Scope      string     `json:"scope"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

type AgentCredential struct {
	Token    string
	DeviceID string
}

type Shortcut struct {
	Name string `json:"name"`
	Cmd  string `json:"cmd"`
}

type Asset struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Path      string    `json:"-"`
	SHA256    string    `json:"sha256"`
	Size      int64     `json:"size"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

type TaskStatus string

const (
	TaskQueued  TaskStatus = "queued"
	TaskLeased  TaskStatus = "leased"
	TaskRunning TaskStatus = "running"
	TaskSuccess TaskStatus = "success"
	TaskFailed  TaskStatus = "failed"
	TaskUnknown TaskStatus = "unknown"
	TaskExpired TaskStatus = "expired"
)

type TaskDevice struct {
	DeviceID  string     `json:"device_id"`
	Status    TaskStatus `json:"status"`
	Result    string     `json:"result,omitempty"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type Task struct {
	ID        string                `json:"task_id"`
	Type      string                `json:"type"`
	Payload   string                `json:"-"`
	AssetID   string                `json:"asset_id,omitempty"`
	DestPath  string                `json:"dest_path,omitempty"`
	CreatedBy string                `json:"created_by"`
	CreatedAt time.Time             `json:"created_at"`
	ExpiresAt time.Time             `json:"expires_at"`
	Devices   map[string]TaskDevice `json:"devices"`
}

type AuditEvent struct {
	ID        string         `json:"id"`
	Actor     string         `json:"actor"`
	Action    string         `json:"action"`
	Target    string         `json:"target,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}
