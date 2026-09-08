// Package api 是 HTTP 层：认证、schema 校验与响应映射。
// 事务与业务规则不进入本包（技术方案 §4）。
package api

import "time"

// ---- 认证 ----

type DeviceStartReq struct {
	Fingerprint string `json:"fingerprint"`
	Hostname    string `json:"hostname"`
	OS          string `json:"os"`
}

type DeviceStartResp struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	Interval        int    `json:"interval"`
	ExpiresIn       int    `json:"expires_in"`
}

type DevicePollReq struct {
	DeviceCode string `json:"device_code"`
}

type TokenResp struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token,omitempty"`
	ExpiresIn    int      `json:"expires_in"`
	User         UserInfo `json:"user,omitempty"`
	MachineID    string   `json:"machine_id,omitempty"`
}

type UserInfo struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Role  string `json:"role"`
	OrgID string `json:"org_id"`
}

type RefreshReq struct {
	RefreshToken string `json:"refresh_token"`
}

type DeviceActivateReq struct {
	UserCode string `json:"user_code"`
	OrgSlug  string `json:"org_slug"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// ---- 分发 ----

// ManifestResp 只含元数据，不含正文。
// CLI 拿它跟本地 lock 对比后决定下载什么（技术方案 §8.2）。
type ManifestResp struct {
	GeneratedAt time.Time        `json:"generated_at"`
	TTLSeconds  int              `json:"ttl_seconds"`
	Bundles     []ManifestBundle `json:"bundles"`
}

type ManifestBundle struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Version     int    `json:"version"`
	Checksum    string `json:"checksum"`
	Description string `json:"description"`
	// Via 说明这个 Bundle 是凭什么拿到的，供 CLI 展示与排障（T27）。
	Via []GrantSourceInfo `json:"via,omitempty"`
}

type GrantSourceInfo struct {
	SubjectType string `json:"subject_type"`
	GroupName   string `json:"group_name,omitempty"`
}

type BundleVersionResp struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Kind     string     `json:"kind"`
	Version  int        `json:"version"`
	Checksum string     `json:"checksum"`
	Files    []FileJSON `json:"files"`
}

type FileJSON struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// ---- 回执 ----

type DistributionEventsReq struct {
	Events []DistributionEventJSON `json:"events"`
}

type DistributionEventJSON struct {
	EventID    string         `json:"event_id"`
	BundleID   string         `json:"bundle_id"`
	Version    int            `json:"version"`
	Action     string         `json:"action"`
	Detail     map[string]any `json:"detail,omitempty"`
	OccurredAt time.Time      `json:"occurred_at"`
}

type AcceptedResp struct {
	Accepted int `json:"accepted"`
}

// ---- 错误 ----

type ErrorResp struct {
	Error     string `json:"error"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// ---- 管理后台 ----

type BundleSummary struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Kind          string    `json:"kind"`
	Description   string    `json:"description"`
	Archived      bool      `json:"archived"`
	LatestVersion int       `json:"latest_version"`
	Checksum      string    `json:"checksum"`
	Groups        []string  `json:"groups,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type BundleDetailResp struct {
	BundleSummary
	DraftFiles []FileJSON `json:"draft_files"`
}

type VersionInfo struct {
	Version   int    `json:"version"`
	Checksum  string `json:"checksum"`
	Changelog string `json:"changelog"`
	// RollbackOfVersion 非零表示本版是对该版本的回滚。
	// changelog 是自由文本，承担不了这个职责。
	RollbackOfVersion int       `json:"rollback_of_version,omitempty"`
	PublishedBy       string    `json:"published_by"`
	PublishedAt       time.Time `json:"published_at"`
}

type GroupInfo struct {
	ID          string   `json:"id"`
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Archived    bool     `json:"archived"`
	BundleIDs   []string `json:"bundle_ids"`
	BundleNames []string `json:"bundle_names"`
	AssignedTo  int      `json:"assigned_to"`
}

type AssignmentInfo struct {
	ID          string     `json:"id"`
	BundleID    string     `json:"bundle_id,omitempty"`
	BundleName  string     `json:"bundle_name,omitempty"`
	GroupID     string     `json:"group_id,omitempty"`
	GroupName   string     `json:"group_name,omitempty"`
	SubjectType string     `json:"subject_type"`
	SubjectID   string     `json:"subject_id,omitempty"`
	SubjectName string     `json:"subject_name,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	Expired     bool       `json:"expired"`
	CreatedAt   time.Time  `json:"created_at"`
}

type MemberInfo struct {
	ID         string     `json:"id"`
	Email      string     `json:"email"`
	Name       string     `json:"name"`
	Role       string     `json:"role"`
	Status     string     `json:"status"`
	Machines   int        `json:"machines"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
}

type AuditEntry struct {
	Email      string    `json:"email"`
	Hostname   string    `json:"hostname"`
	BundleName string    `json:"bundle_name"`
	Version    int       `json:"version"`
	Action     string    `json:"action"`
	Detail     string    `json:"detail,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type StaleMachine struct {
	Email      string    `json:"email"`
	Hostname   string    `json:"hostname"`
	LastSeenAt time.Time `json:"last_seen_at"`
	Days       int       `json:"days"`
	// UserLevel 为 true 表示该用户所有设备都掉队 —— 可能已离职未处理。
	UserLevel bool `json:"user_level"`
}

type ExplainEntry struct {
	BundleID   string            `json:"bundle_id"`
	BundleName string            `json:"bundle_name"`
	Kind       string            `json:"kind"`
	Version    int               `json:"version"`
	Via        []GrantSourceInfo `json:"via"`
}

// ExecutionEventJSON 是 L0 执行事件的上报格式。
//
// 字段集合即隐私白名单：服务端会用严格 schema 重建 summary，
// 未知字段一律丢弃（技术方案 §10.4）。
type ExecutionEventJSON struct {
	ID         string       `json:"id"`
	SessionID  string       `json:"session_id"`
	EventType  string       `json:"event_type"`
	ToolName   string       `json:"tool_name,omitempty"`
	Summary    EventSummary `json:"summary"`
	OccurredAt time.Time    `json:"occurred_at"`
}

type EventSummary struct {
	Repo         string `json:"repo,omitempty"`
	FilePath     string `json:"file_path,omitempty"`
	BashCommand  string `json:"bash_command,omitempty"`
	LinesChanged int    `json:"lines_changed,omitempty"`
	ExitCode     *int   `json:"exit_code,omitempty"`
}

type ExecutionEventsReq struct {
	Events []ExecutionEventJSON `json:"events"`
}

type ExecutionEntry struct {
	Email      string       `json:"email"`
	Hostname   string       `json:"hostname"`
	SessionID  string       `json:"session_id"`
	EventType  string       `json:"event_type"`
	ToolName   string       `json:"tool_name,omitempty"`
	Summary    EventSummary `json:"summary"`
	OccurredAt time.Time    `json:"occurred_at"`
}
