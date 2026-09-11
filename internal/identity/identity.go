// Package identity 负责身份：账号、组织成员关系、密码、访问令牌、设备授权流与刷新凭据。
//
// 设计见 docs/设计-身份与组织模型.md。账号是全局身份，键是 (issuer, subject)；
// 成员记录（users 表）是「某账号在某组织的身份」，访问令牌里放的是成员记录 id。
package identity

import (
	"errors"
	"time"
)

// Account 是全局身份。
type Account struct {
	ID         string
	Issuer     string
	Subject    string
	Email      string
	Name       string
	DisabledAt *time.Time
}

// Membership 是账号在某组织内的成员记录，也就是 users 表的一行。
type Membership struct {
	UserID    string
	AccountID string
	OrgID     string
	OrgSlug   string
	OrgName   string
	Email     string
	Name      string
	Role      Role
	Suspended bool
}

// Role 是组织级角色。细粒度权限由它和团队 / 项目角色推导。
type Role string

const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
	// RoleViewer 只读浏览后台，所有写请求在中间件拒绝。
	RoleViewer Role = "viewer"
)

// IsAdmin 判断是否具备组织级管理权限。
func (r Role) IsAdmin() bool { return r == RoleOwner || r == RoleAdmin }

// Identity 是身份适配器验证后的标准化结果。业务只认它，不认任何 IdP 的原始 token。
type Identity struct {
	Issuer  string
	Subject string
	Email   string
	Name    string
	Groups  []string
}

// Machine 是一台登记过的设备。
type Machine struct {
	ID          string
	OrgID       string
	UserID      string
	Fingerprint string
	Hostname    string
	OS          string
	RevokedAt   *time.Time
}

// DeviceCode 是设备授权流中的一条记录。
type DeviceCode struct {
	CodeHash     string
	UserCode     string
	Fingerprint  string
	Hostname     string
	OS           string
	Status       string
	EnrollmentID string
	UserID       string
	MachineID    string
	ExpiresAt    time.Time
}

// RefreshToken 是一条刷新凭据。同一 FamilyID 的 token 由轮换串成一族。
type RefreshToken struct {
	TokenHash string
	FamilyID  string
	UserID    string
	MachineID string
	ExpiresAt time.Time
	RevokedAt *time.Time
}

// Tokens 是一次签发的结果。
type Tokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
	Membership   Membership
	MachineID    string
	// EnrollmentProjectIDs 非空表示本次设备授权带了接入码，客户端应据此创建绑定。
	EnrollmentProjectIDs []string
}

var (
	// ErrNotFound 由 Store 在找不到记录时返回。
	ErrNotFound = errors.New("identity: 记录不存在")
	// ErrBadCredentials 统一表示账号或密码错误，不区分账号是否存在。
	ErrBadCredentials = errors.New("identity: 账号或密码不正确")
	// ErrSuspended 表示成员记录已停用。
	ErrSuspended = errors.New("identity: 账号已停用")
	// ErrDisabled 表示全局账号已禁用。
	ErrDisabled = errors.New("identity: 账号已禁用")
	// ErrAuthorizationPending 表示设备码尚未被批准。
	ErrAuthorizationPending = errors.New("identity: 等待用户批准")
	// ErrCodeExpired 表示设备码不存在、已过期或已消费。
	ErrCodeExpired = errors.New("identity: 设备码已失效")
	// ErrRefreshReused 表示刷新凭据被重复使用，整族已撤销。
	ErrRefreshReused = errors.New("identity: 刷新凭据已被重用")
	// ErrInvalidGrant 表示刷新凭据无效或已过期。
	ErrInvalidGrant = errors.New("identity: 刷新凭据无效")
	// ErrMachineRevoked 表示设备已被撤销。
	ErrMachineRevoked = errors.New("identity: 设备已撤销")
)
