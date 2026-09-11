package identity

import (
	"context"
	"time"
)

// Store 是身份模块需要的持久化能力。由 db 包实现；测试用内存实现。
//
// 所有方法都不接受客户端时间，时间由 Service 注入。
type Store interface {
	// FindLocalAccount 按邮箱查 local 账号，同时返回密码哈希。找不到返回 ErrNotFound。
	FindLocalAccount(ctx context.Context, email string) (Account, string, error)
	// ListMemberships 列出账号在所有组织的成员记录。
	ListMemberships(ctx context.Context, accountID string) ([]Membership, error)
	// GetMembership 按成员记录 id 读取，含组织信息与状态。
	GetMembership(ctx context.Context, userID string) (Membership, error)

	CreateDeviceCode(ctx context.Context, dc DeviceCode) error
	GetDeviceCodeByUserCode(ctx context.Context, userCode string) (DeviceCode, error)
	// ApproveDeviceCode 以 compare-and-set 批准：只有 pending 且未过期的码会被改写。
	ApproveDeviceCode(ctx context.Context, userCode, userID, machineID string, now time.Time) error
	// ConsumeDeviceCode 把 approved 的码改为 consumed 并返回该记录；同一个码只能成功一次。
	ConsumeDeviceCode(ctx context.Context, codeHash string, now time.Time) (DeviceCode, error)
	// DeviceCodeStatus 供轮询区分 pending / expired / consumed。
	DeviceCodeStatus(ctx context.Context, codeHash string) (status string, expiresAt time.Time, err error)

	// UpsertMachine 按 (user, fingerprint) 登记设备，返回 id。已撤销的设备会被重新启用。
	UpsertMachine(ctx context.Context, m Machine) (string, error)
	GetMachine(ctx context.Context, id string) (Machine, error)
	TouchMachine(ctx context.Context, id string, now time.Time) error

	CreateRefreshToken(ctx context.Context, t RefreshToken) error
	// GetRefreshToken 按哈希读取，包括已撤销的（调用方需要据此识别重用）。
	GetRefreshToken(ctx context.Context, tokenHash string) (RefreshToken, error)
	// RevokeRefreshToken 撤销单条。
	RevokeRefreshToken(ctx context.Context, tokenHash string, now time.Time) error
	// RevokeRefreshFamily 撤销整族。
	RevokeRefreshFamily(ctx context.Context, familyID string, now time.Time) error
	// RevokeMachineTokens 撤销某设备的全部刷新凭据（登出）。
	RevokeMachineTokens(ctx context.Context, userID, machineID string, now time.Time) error
}
