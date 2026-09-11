package api

import "time"

// ---- 发现 ----

// CapabilitiesResp 是 GET /v1/capabilities 的响应。
type CapabilitiesResp struct {
	Version string `json:"version"`
	// APIVersion 是路径前缀 /v1 对应的契约版本；MinClientVersion 是服务端还愿意服务的最低客户端版本。
	APIVersion       string   `json:"api_version"`
	MinClientVersion string   `json:"min_client_version"`
	Auth             []string `json:"auth"`
	Limits           Limits   `json:"limits"`
}

// Limits 是服务端上限，客户端据此配置自己，不硬编码。
type Limits struct {
	MaxBlobBytes     int `json:"max_blob_bytes"`
	MaxSnapshotBytes int `json:"max_snapshot_bytes"`
	MaxEntries       int `json:"max_entries"`
}

// ---- 认证 ----

// LoginReq 是密码登录请求。
type LoginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LoginResp 返回账号与可进入的组织；不含令牌，令牌是组织内的。
type LoginResp struct {
	Account       AccountInfo      `json:"account"`
	LoginToken    string           `json:"login_token"`
	Organizations []MembershipInfo `json:"organizations"`
}

// SessionReq 用登录凭据换取某组织的会话令牌。
type SessionReq struct {
	LoginToken string `json:"login_token"`
	UserID     string `json:"user_id"`
}

// AccountInfo 是全局账号的展示信息。
type AccountInfo struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// MembershipInfo 是某组织内的成员身份。
type MembershipInfo struct {
	UserID  string `json:"user_id"`
	OrgID   string `json:"org_id"`
	OrgSlug string `json:"org_slug"`
	OrgName string `json:"org_name"`
	Role    string `json:"role"`
}

// TokenResp 是签发结果。
type TokenResp struct {
	AccessToken  string         `json:"access_token"`
	RefreshToken string         `json:"refresh_token,omitempty"`
	TokenType    string         `json:"token_type"`
	ExpiresIn    int            `json:"expires_in"`
	MachineID    string         `json:"machine_id,omitempty"`
	Membership   MembershipInfo `json:"membership"`
	// EnrollmentProjectIDs 非空表示这次登录带了接入码，客户端应据此创建绑定。
	EnrollmentProjectIDs []string `json:"enrollment_project_ids,omitempty"`
}

// DeviceStartReq 是 CLI 发起设备授权。
type DeviceStartReq struct {
	Fingerprint    string `json:"fingerprint"`
	Hostname       string `json:"hostname"`
	OS             string `json:"os"`
	EnrollmentCode string `json:"enrollment_code,omitempty"`
}

// DeviceStartResp 只在这一次返回 device_code。
type DeviceStartResp struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	Interval        int    `json:"interval"`
	ExpiresIn       int    `json:"expires_in"`
}

// DevicePollReq 是 CLI 轮询。
type DevicePollReq struct {
	DeviceCode string `json:"device_code"`
}

// DevicePeekResp 供审批页展示它正在批准什么。
type DevicePeekResp struct {
	Hostname   string        `json:"hostname"`
	OS         string        `json:"os"`
	ExpiresAt  time.Time     `json:"expires_at"`
	Enrollment *EnrollmentIn `json:"enrollment,omitempty"`
}

// EnrollmentIn 是接入码在审批页里的摘要。
type EnrollmentIn struct {
	ID         string   `json:"id"`
	ProjectIDs []string `json:"project_ids"`
	// Allowed 表示当前登录者对这些项目都有读权限，可以批准。
	Allowed bool `json:"allowed"`
}

// DeviceActivateReq 是浏览器端批准。
type DeviceActivateReq struct {
	UserCode string `json:"user_code"`
}

// RefreshReq 是刷新凭据轮换。
type RefreshReq struct {
	RefreshToken string `json:"refresh_token"`
}

// RevokeReq 撤销自己的设备凭据；不带 machine_id 时撤销当前令牌对应的设备。
type RevokeReq struct {
	MachineID string `json:"machine_id,omitempty"`
}

// CreateOrganizationReq 用登录令牌自建组织：登录后还不属于任何组织的人从这里开始。
type CreateOrganizationReq struct {
	LoginToken string `json:"login_token"`
	Name       string `json:"name"`
	Slug       string `json:"slug"`
}

// CreateOrganizationResp 返回新组织的成员身份，前端拿它换会话令牌。
type CreateOrganizationResp struct {
	Organization OrganizationInfo `json:"organization"`
	Membership   MembershipInfo   `json:"membership"`
}

// MeResp 是当前调用方。
type MeResp struct {
	Account     AccountInfo    `json:"account"`
	Membership  MembershipInfo `json:"membership"`
	MachineID   string         `json:"machine_id,omitempty"`
	TokenKind   string         `json:"token_kind"`
	Permissions []string       `json:"permissions"`
}

// ---- 组织 ----

// OrganizationInfo 是组织展示信息。
type OrganizationInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
}

// UpdateOrganizationReq 改组织名。
type UpdateOrganizationReq struct {
	Name string `json:"name"`
}

// PolicyJSON 是组织策略。
type PolicyJSON struct {
	RequiredApprovals int     `json:"required_approvals"`
	LearningsReview   bool    `json:"learnings_review"`
	ConfidencePrune   float64 `json:"confidence_prune"`
	ConfidencePromote float64 `json:"confidence_promote"`
	RetentionMonths   int     `json:"retention_months"`
}

// TeamInfo 是团队。
type TeamInfo struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Slug      string           `json:"slug"`
	Archived  bool             `json:"archived"`
	CreatedAt time.Time        `json:"created_at"`
	Members   []TeamMemberInfo `json:"members,omitempty"`
}

// TeamMemberInfo 是团队成员。
type TeamMemberInfo struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Name   string `json:"name"`
	Role   string `json:"role"`
}

// CreateTeamReq 建团队。
type CreateTeamReq struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// UpdateTeamReq 改团队。
type UpdateTeamReq struct {
	Name     string `json:"name"`
	Archived bool   `json:"archived"`
}

// PutMemberRoleReq 团队或项目内的角色。
type PutMemberRoleReq struct {
	Role string `json:"role"`
}

// ReviewersReq 覆盖 reviewer 名单。
type ReviewersReq struct {
	UserIDs []string `json:"user_ids"`
}

// ReviewerInfo 是一位 reviewer。
type ReviewerInfo struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
}

// MemberInfo 是组织成员。
type MemberInfo struct {
	UserID     string     `json:"user_id"`
	Email      string     `json:"email"`
	Name       string     `json:"name"`
	Role       string     `json:"role"`
	Status     string     `json:"status"`
	Machines   int        `json:"machines"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// CreateMemberReq 邀请密码账号。
type CreateMemberReq struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	Password string `json:"password"`
}

// MemberStatusReq 停用或启用。
type MemberStatusReq struct {
	Status string `json:"status"`
}

// MemberRoleReq 改组织角色。
type MemberRoleReq struct {
	Role string `json:"role"`
}

// PasswordReq 重置密码。
type PasswordReq struct {
	Password string `json:"password"`
}

// ---- 项目与绑定 ----

// ProjectInfo 是项目。
type ProjectInfo struct {
	ID        string           `json:"id"`
	TeamID    string           `json:"team_id,omitempty"`
	Name      string           `json:"name"`
	Slug      string           `json:"slug"`
	Archived  bool             `json:"archived"`
	CreatedAt time.Time        `json:"created_at"`
	Members   []TeamMemberInfo `json:"members,omitempty"`
}

// CreateProjectReq 建项目。
type CreateProjectReq struct {
	Name   string `json:"name"`
	Slug   string `json:"slug"`
	TeamID string `json:"team_id,omitempty"`
}

// UpdateProjectReq 改项目。
type UpdateProjectReq struct {
	Name     string `json:"name"`
	TeamID   string `json:"team_id"`
	Archived bool   `json:"archived"`
}

// BindingReq 创建或修改绑定。
type BindingReq struct {
	WorkspaceID string   `json:"workspace_id,omitempty"`
	DisplayName string   `json:"display_name"`
	ProjectIDs  []string `json:"project_ids"`
}

// BindingInfo 是绑定。
type BindingInfo struct {
	ID              string     `json:"id"`
	MachineID       string     `json:"machine_id"`
	WorkspaceID     string     `json:"workspace_id"`
	DisplayName     string     `json:"display_name"`
	ProjectIDs      []string   `json:"project_ids"`
	AppliedRevision string     `json:"applied_revision,omitempty"`
	State           string     `json:"state"`
	LastSyncAt      *time.Time `json:"last_sync_at,omitempty"`
	UpdatedAt       time.Time  `json:"updated_at"`
	ETag            string     `json:"etag"`
}

// ---- 设备与接入码 ----

// DeviceInfo 是设备。
type DeviceInfo struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	Email      string     `json:"email"`
	Hostname   string     `json:"hostname"`
	OS         string     `json:"os"`
	LastSeenAt time.Time  `json:"last_seen_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// RenameDeviceReq 改设备名。
type RenameDeviceReq struct {
	Hostname string `json:"hostname"`
}

// CreateEnrollmentReq 签发接入码。
type CreateEnrollmentReq struct {
	ProjectIDs []string `json:"project_ids"`
	TTLHours   int      `json:"ttl_hours,omitempty"`
	MaxUses    int      `json:"max_uses,omitempty"`
}

// EnrollmentInfo 是接入码；Code 只在创建响应里出现一次。
type EnrollmentInfo struct {
	ID         string     `json:"id"`
	Code       string     `json:"code,omitempty"`
	ProjectIDs []string   `json:"project_ids"`
	CreatedBy  string     `json:"created_by,omitempty"`
	ExpiresAt  time.Time  `json:"expires_at"`
	MaxUses    int        `json:"max_uses"`
	UsedCount  int        `json:"used_count"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	// Command 是给员工的完整接入命令。
	Command string `json:"command,omitempty"`
}

// ---- 身份源 ----

// IdPConfigJSON 是 OIDC 配置；ClientSecret 只写不读。
type IdPConfigJSON struct {
	ID           string   `json:"id,omitempty"`
	Issuer       string   `json:"issuer"`
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret,omitempty"`
	Scopes       []string `json:"scopes"`
	Enabled      bool     `json:"enabled"`
}

// GroupMappingJSON 是用户组到角色的映射。
type GroupMappingJSON struct {
	IdPGroup string `json:"idp_group"`
	Target   string `json:"target"`
	TargetID string `json:"target_id,omitempty"`
	Role     string `json:"role"`
	Priority int    `json:"priority"`
}

// ---- 审计 ----

// AuditEventInfo 是审计记录。
type AuditEventInfo struct {
	ID         string         `json:"id"`
	ActorEmail string         `json:"actor_email,omitempty"`
	Type       string         `json:"type"`
	TargetType string         `json:"target_type"`
	TargetID   string         `json:"target_id"`
	Detail     map[string]any `json:"detail"`
	RequestID  string         `json:"request_id,omitempty"`
	OccurredAt time.Time      `json:"occurred_at"`
}

// Page 是分页响应的通用外壳。
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}
