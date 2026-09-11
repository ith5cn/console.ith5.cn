// Package releases 实现发布事务与回滚（docs/设计-变更集与发布.md §5）。
//
// 发布是唯一改变「当前生效版本」的路径：一个事务里核对基线、写入版本、生成 release、推进 head。
// 客户端只会看到旧 head 或新 head，永远看不到一半。
package releases

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ith5/ith5/internal/audit"
	"github.com/ith5/ith5/internal/changesets"
	"github.com/ith5/ith5/internal/organizations"
	"github.com/ith5/ith5/internal/resources"
)

// Item 是 release 里的一条：某资源的某个版本。
type Item struct {
	BundleID string         `json:"bundle_id"`
	Kind     resources.Kind `json:"kind"`
	Name     string         `json:"name"`
	Version  int            `json:"version"`
	Deleted  bool           `json:"deleted"`
	ScopeKey string         `json:"scope_key"`
}

// Release 是一次原子发布。
type Release struct {
	ID             string
	OrgID          string
	ChangesetID    string
	PublishedBy    string
	PublisherEmail string
	PublishedAt    time.Time
	Items          []Item
	// Revisions 是发布后各落点的新 revision。
	Revisions map[string]string
}

// Conflict 描述基线过期且操作重叠的落点与资源键。
type Conflict struct {
	ScopeKey string   `json:"scope_key"`
	Keys     []string `json:"keys"` // kind/name
}

// ErrRevisionMismatch 表示基线过期且重叠，变更集停在 APPROVED，作者需重建。
type ErrRevisionMismatch struct {
	Conflicts []Conflict
}

func (e *ErrRevisionMismatch) Error() string {
	return "releases: 基线已过期且与之后的发布重叠"
}

var (
	ErrForbidden = errors.New("releases: 无权发布")
	ErrState     = errors.New("releases: 变更集不在可发布状态")
	ErrNotFound  = errors.New("releases: 记录不存在")
)

// Store 是发布的持久化能力。Publish 必须在一个事务内完成 §5 的全部步骤。
type Store interface {
	// Publish 执行发布事务：
	//  1. 按 scope key 排序锁 content_heads；
	//  2. 核对 cs.BaseRevisions；过期的落点检查其后 release 是否触碰同一资源键，重叠则返回 *ErrRevisionMismatch；
	//  3. put → 新版本（max+1），delete → tombstone 版本；
	//  4. 写 releases、更新 content_heads、变更集置 published。
	Publish(ctx context.Context, cs changesets.Changeset, publisherID string, now time.Time) (Release, error)
	Get(ctx context.Context, orgID, id string) (Release, error)
	List(ctx context.Context, orgID, scopeKey string, limit int) ([]Release, error)
	// PreviousFiles 返回某资源在指定版本之前那一版的文件清单；没有更早版本返回 nil。
	PreviousFiles(ctx context.Context, orgID, bundleID string, version int) (files []resources.FileRef, prevVersion int, deleted bool, err error)
}

// Service 编排发布与回滚。
type Service struct {
	store      Store
	changesets *changesets.Service
	audit      audit.Recorder
	now        func() time.Time
}

// NewService 构造。
func NewService(store Store, cs *changesets.Service, rec audit.Recorder) *Service {
	if rec == nil {
		rec = audit.Nop{}
	}
	return &Service{store: store, changesets: cs, audit: rec, now: time.Now}
}

// Store 暴露仓储。
func (s *Service) Store() Store { return s.store }

// Publish 发布一个 APPROVED 的变更集。发布者对每个落点都要有 release:publish。
func (s *Service) Publish(ctx context.Context, a changesets.Actor, changesetID string) (Release, error) {
	cs, err := s.changesets.Store().Get(ctx, a.OrgID, changesetID)
	if err != nil {
		return Release{}, err
	}
	if cs.State != changesets.StateApproved {
		return Release{}, ErrState
	}
	// learning 直通：作者自己发布只含 learning 的变更集，只需 learning:contribute
	learningSelf := changesets.LearningOnly(cs.Ops) && cs.AuthorID == a.UserID
	for _, op := range cs.Ops {
		if learningSelf && a.Subject.Can(organizations.PermLearningContribute, op.Scope(a.OrgID)) {
			continue
		}
		if !a.Subject.Can(organizations.PermReleasePublish, op.Scope(a.OrgID)) {
			return Release{}, ErrForbidden
		}
	}
	rel, err := s.store.Publish(ctx, cs, a.UserID, s.now())
	if err != nil {
		return Release{}, err
	}
	_ = s.audit.Record(ctx, audit.Event{
		OrgID: a.OrgID, ActorUserID: a.UserID, Type: "release.publish", TargetType: "release", TargetID: rel.ID,
		Detail: map[string]any{"changeset_id": cs.ID, "items": len(rel.Items), "fast_track": cs.FastTrack}, RequestID: a.RequestID,
	})
	return rel, nil
}

// Rollback 为某次发布生成回滚变更集：每个条目回到它之前那一版，之前没有的则删除。
// 不自动发布，走正常审核；发布者有权限时可 fast_track。
func (s *Service) Rollback(ctx context.Context, a changesets.Actor, releaseID string, fastTrack bool) (changesets.Changeset, error) {
	rel, err := s.store.Get(ctx, a.OrgID, releaseID)
	if err != nil {
		return changesets.Changeset{}, err
	}
	ops := make([]changesets.Op, 0, len(rel.Items))
	for _, it := range rel.Items {
		level, owner := splitScopeKey(it.ScopeKey)
		op := changesets.Op{Level: level, Kind: it.Kind, Name: it.Name}
		switch level {
		case resources.LevelTeam:
			op.TeamID = owner
		case resources.LevelProject:
			op.ProjectID = owner
		}
		files, prev, deleted, err := s.store.PreviousFiles(ctx, a.OrgID, it.BundleID, it.Version)
		if err != nil {
			return changesets.Changeset{}, err
		}
		switch {
		case prev == 0 || deleted:
			// 这次发布之前它不存在（或已是 tombstone）：回滚 = 删除
			if it.Deleted {
				continue // 删了一个本来就不存在的东西，回滚无事可做
			}
			op.Op = changesets.OpDelete
		default:
			op.Op, op.Files = changesets.OpPut, files
		}
		ops = append(ops, op)
	}
	if len(ops) == 0 {
		return changesets.Changeset{}, fmt.Errorf("%w: 该发布没有可回滚的内容", changesets.ErrInvalidInput)
	}
	cs, err := s.changesets.Create(ctx, a, changesets.Input{
		Title: "回滚 " + rel.ID[:8], Description: "回滚发布 " + rel.ID, Ops: ops, FastTrack: fastTrack,
	})
	if err != nil {
		return changesets.Changeset{}, err
	}
	_ = s.audit.Record(ctx, audit.Event{
		OrgID: a.OrgID, ActorUserID: a.UserID, Type: "release.rollback", TargetType: "release", TargetID: releaseID,
		Detail: map[string]any{"changeset_id": cs.ID}, RequestID: a.RequestID,
	})
	return cs, nil
}

func splitScopeKey(k string) (resources.Level, string) {
	for i := 0; i < len(k); i++ {
		if k[i] == ':' {
			return resources.Level(k[:i]), k[i+1:]
		}
	}
	return "", k
}
