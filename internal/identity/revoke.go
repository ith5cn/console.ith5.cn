package identity

import (
	"context"
	"time"
)

// SuspendStore 在一个事务内完成停用的全部传播：成员记录停用、刷新凭据撤销、设备撤销、绑定撤销。
//
// 三处状态必须一起改。分开改的话，中间态里「账号停用了但设备令牌还能续期」就是一个漏洞。
type SuspendStore interface {
	SuspendUser(ctx context.Context, orgID, userID string, now time.Time) error
	ReactivateUser(ctx context.Context, orgID, userID string) error
	// RevokeMachine 撤销一台设备：设备标记、其刷新凭据、其绑定。
	RevokeMachine(ctx context.Context, orgID, machineID string, now time.Time) error
	ListMachines(ctx context.Context, orgID, userID string) ([]MachineInfo, error)
	RenameMachine(ctx context.Context, orgID, machineID, userID, hostname string) error
}

// MachineInfo 是设备的后台视图。
type MachineInfo struct {
	Machine
	Email      string
	LastSeenAt time.Time
	CreatedAt  time.Time
}

// Revoker 封装撤销传播。
type Revoker struct {
	store SuspendStore
	now   func() time.Time
}

// NewRevoker 构造。
func NewRevoker(store SuspendStore) *Revoker { return &Revoker{store: store, now: time.Now} }

// Suspend 停用成员并传播。
func (r *Revoker) Suspend(ctx context.Context, orgID, userID string) error {
	return r.store.SuspendUser(ctx, orgID, userID, r.now())
}

// Reactivate 重新启用成员。设备与绑定不自动恢复，需重新登录。
func (r *Revoker) Reactivate(ctx context.Context, orgID, userID string) error {
	return r.store.ReactivateUser(ctx, orgID, userID)
}

// RevokeMachine 撤销一台设备。
func (r *Revoker) RevokeMachine(ctx context.Context, orgID, machineID string) error {
	return r.store.RevokeMachine(ctx, orgID, machineID, r.now())
}

// Store 暴露仓储。
func (r *Revoker) Store() SuspendStore { return r.store }
