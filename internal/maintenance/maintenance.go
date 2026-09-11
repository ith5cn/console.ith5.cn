// Package maintenance 是服务端的定时清理：按组织保留期删上报数据、清过期的账本与令牌，
// 并统计 IdP 对账差异。只删不改，任何一步失败不影响其他步骤。
package maintenance

import (
	"context"
	"log/slog"
	"time"

	"github.com/ith5/ith5/internal/identity"
)

// Retention 是一个组织的保留期设置。
type Retention struct {
	OrgID  string
	Months int
}

// Counts 是一次清理各表删掉的行数。
type Counts map[string]int64

// Store 是清理需要的持久化能力。
type Store interface {
	ListRetention(ctx context.Context) ([]Retention, error)
	// PurgeTelemetry 删除该组织 before 之前的 sessions、usage_daily、execution_events。
	PurgeTelemetry(ctx context.Context, orgID string, before time.Time) (Counts, error)
	// PurgeLedgers 删除全局的过期账本：report_events（30 天）、idempotency_keys、device_codes、
	// 已撤销或过期超过 30 天的 refresh_tokens、过期超过 30 天的 enrollments。
	PurgeLedgers(ctx context.Context, now time.Time) (Counts, error)
}

// ReconcileSource 提供对账所需的映射表与成员。
type ReconcileSource interface {
	ListGroupMappings(ctx context.Context, orgID string) ([]identity.GroupMapping, error)
	ListOIDCMembers(ctx context.Context, orgID string) ([]identity.OIDCMember, error)
}

// LedgerWindow 是去重账本与撤销令牌的保留期，覆盖最长离线窗口。
const LedgerWindow = 30 * 24 * time.Hour

// Report 是一次运行的结果。
type Report struct {
	RanAt      time.Time         `json:"ran_at"`
	Deleted    Counts            `json:"deleted"`
	Reconcile  map[string]int    `json:"reconcile_diffs"` // org_id -> 差异成员数
	Errors     []string          `json:"errors,omitempty"`
	perOrgDone map[string]Counts // 仅日志用
}

// Janitor 按固定间隔运行清理。
type Janitor struct {
	store    Store
	idp      ReconcileSource
	log      *slog.Logger
	Interval time.Duration
	now      func() time.Time
}

// New 构造清理器；idp 可为 nil（不做对账统计）。
func New(store Store, idp ReconcileSource, log *slog.Logger) *Janitor {
	return &Janitor{store: store, idp: idp, log: log, Interval: 24 * time.Hour, now: time.Now}
}

// Run 立刻跑一次，然后按 Interval 循环，直到 ctx 结束。
func (j *Janitor) Run(ctx context.Context) {
	j.runLogged(ctx)
	t := time.NewTicker(j.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			j.runLogged(ctx)
		}
	}
}

func (j *Janitor) runLogged(ctx context.Context) {
	rep := j.RunOnce(ctx, "")
	j.log.Info("维护任务完成", "deleted", rep.Deleted, "reconcile_diffs", rep.Reconcile, "errors", len(rep.Errors))
	for _, e := range rep.Errors {
		j.log.Warn("维护任务出错", "err", e)
	}
}

// RunOnce 跑一轮。orgID 非空时只处理该组织的保留期与对账，并跳过全局账本清理。
func (j *Janitor) RunOnce(ctx context.Context, orgID string) Report {
	now := j.now()
	rep := Report{RanAt: now, Deleted: Counts{}, Reconcile: map[string]int{}}
	add := func(c Counts) {
		for k, v := range c {
			rep.Deleted[k] += v
		}
	}
	fail := func(err error) { rep.Errors = append(rep.Errors, err.Error()) }

	rets, err := j.store.ListRetention(ctx)
	if err != nil {
		fail(err)
		return rep
	}
	for _, r := range rets {
		if orgID != "" && r.OrgID != orgID {
			continue
		}
		before := now.AddDate(0, -r.Months, 0)
		c, err := j.store.PurgeTelemetry(ctx, r.OrgID, before)
		if err != nil {
			fail(err)
		}
		add(c)
		if j.idp != nil {
			n, err := j.reconcileCount(ctx, r.OrgID)
			if err != nil {
				fail(err)
			} else if n > 0 {
				rep.Reconcile[r.OrgID] = n
			}
		}
	}
	if orgID == "" {
		c, err := j.store.PurgeLedgers(ctx, now)
		if err != nil {
			fail(err)
		}
		add(c)
	}
	return rep
}

func (j *Janitor) reconcileCount(ctx context.Context, orgID string) (int, error) {
	mappings, err := j.idp.ListGroupMappings(ctx, orgID)
	if err != nil {
		return 0, err
	}
	members, err := j.idp.ListOIDCMembers(ctx, orgID)
	if err != nil {
		return 0, err
	}
	return len(identity.Reconcile(mappings, members)), nil
}
