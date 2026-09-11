// Package sync 组装快照、校验 blob 访问、接收同步回执（docs/设计-同步协议.md）。
//
// 核心思路：每次都给完整清单，内容按文件哈希取，revision 就是清单的哈希。
package sync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/ith5/ith5/internal/projects"
	"github.com/ith5/ith5/internal/resources"
)

// Limits 是随快照下发的上限，客户端不硬编码。
type Limits struct {
	MaxBlobBytes     int `json:"max_blob_bytes"`
	MaxSnapshotBytes int `json:"max_snapshot_bytes"`
	MaxEntries       int `json:"max_entries"`
}

// DefaultLimits 是当前部署的上限。
var DefaultLimits = Limits{MaxBlobBytes: 32 << 20, MaxSnapshotBytes: 1 << 30, MaxEntries: 5000}

// ProjectRef 是快照里的项目摘要。
type ProjectRef struct {
	ID       string `json:"id"`
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Revision string `json:"revision"`
}

// GrantRef 是被授权的权限组，客户端据此生成 roles.yaml。
type GrantRef struct {
	GroupKey string `json:"group_key"`
	Name     string `json:"name"`
}

// Snapshot 是一份绑定应得的完整清单。
type Snapshot struct {
	Revision    string            `json:"revision"`
	GeneratedAt time.Time         `json:"generated_at"`
	Org         OrgRef            `json:"org"`
	Projects    []ProjectRef      `json:"projects"`
	Policy      resources.Policy  `json:"policy"`
	Grants      []GrantRef        `json:"grants"`
	Culture     *resources.Entry  `json:"culture,omitempty"`
	Resources   []resources.Entry `json:"resources"`
	Limits      Limits            `json:"limits"`
}

// OrgRef 是组织摘要。
type OrgRef struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// Result 是客户端上报的一条物化结果。
type Result struct {
	Kind   resources.Kind `json:"kind"`
	Name   string         `json:"name"`
	Action string         `json:"action"`
	Detail map[string]any `json:"detail,omitempty"`
}

// ValidActions 是回执允许的动作。
var ValidActions = map[string]bool{"installed": true, "updated": true, "removed": true, "conflict_skipped": true, "failed": true}

var (
	ErrBlobNotVisible = errors.New("sync: blob 不存在或不可见")
	ErrBindingRevoked = errors.New("sync: 绑定已撤销")
)

// Store 是同步模块需要的持久化能力。
type Store interface {
	// ListCandidates 返回组织内全部有已发布版本的资源（含 tombstone），policy/culture 附带正文。
	ListCandidates(ctx context.Context, orgID string) ([]resources.Candidate, error)
	// ListGroupCandidates 返回经权限组授予给该用户的资源，Namespace 为组 key，ViaGroup 已填。
	ListGroupCandidates(ctx context.Context, orgID, userID string, projectIDs []string) ([]resources.Candidate, []GrantRef, error)
	// ProjectRefs 返回绑定项目的摘要与所属团队 id。
	ProjectRefs(ctx context.Context, orgID string, projectIDs []string) (refs []ProjectRef, teamIDs []string, err error)
	OrgRef(ctx context.Context, orgID string) (OrgRef, error)
	// GetBlob 返回内容；仅当该哈希被用户可见的某个版本引用时放行，否则 ErrBlobNotVisible。
	GetBlob(ctx context.Context, orgID, userID, sha256 string) ([]byte, error)
	// RecordResults 写入同步回执并更新绑定的 applied_revision 与 last_sync_at。
	RecordResults(ctx context.Context, b projects.Binding, revision, ip string, results []Result, now time.Time) error
}

// Service 组装快照。
type Service struct {
	store Store
	now   func() time.Time
}

// NewService 构造。
func NewService(store Store) *Service { return &Service{store: store, now: time.Now} }

// WithClock 替换时钟。
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// Snapshot 为一份绑定计算清单。suspended 由调用方在认证层拦截；这里只按绑定状态判断。
//
// 撤销的绑定返回空清单（而不是错误）：客户端据此卸载全部内容，这是「切断续期」的落地方式。
func (s *Service) Snapshot(ctx context.Context, b projects.Binding) (Snapshot, error) {
	snap := Snapshot{GeneratedAt: s.now(), Limits: DefaultLimits, Resources: []resources.Entry{}, Projects: []ProjectRef{}, Grants: []GrantRef{}}
	org, err := s.store.OrgRef(ctx, b.OrgID)
	if err != nil {
		return snap, err
	}
	snap.Org = org
	if b.State == projects.BindingRevoked {
		snap.Policy = resources.DefaultPolicy()
		snap.Revision = Revision(snap)
		return snap, nil
	}

	refs, teamIDs, err := s.store.ProjectRefs(ctx, b.OrgID, b.ProjectIDs)
	if err != nil {
		return snap, err
	}
	snap.Projects = refs
	cands, err := s.store.ListCandidates(ctx, b.OrgID)
	if err != nil {
		return snap, err
	}
	groupCands, grants, err := s.store.ListGroupCandidates(ctx, b.OrgID, b.UserID, b.ProjectIDs)
	if err != nil {
		return snap, err
	}
	snap.Grants = grants
	if snap.Grants == nil {
		snap.Grants = []GrantRef{}
	}

	activeProjects := make([]string, 0, len(refs))
	for _, r := range refs {
		activeProjects = append(activeProjects, r.ID)
	}
	out := resources.Resolve(resources.ResolveInput{
		OrgID: b.OrgID, TeamIDs: teamIDs, ProjectIDs: activeProjects,
		Candidates: append(cands, groupCands...),
	})
	snap.Resources = out.Entries
	if snap.Resources == nil {
		snap.Resources = []resources.Entry{}
	}
	snap.Policy = out.Policy
	snap.Culture = out.Culture
	snap.Revision = Revision(snap)
	return snap, nil
}

// Revision 对快照除 generated_at 与 limits 外的部分做规范化 SHA-256。
//
// 不透明、只比较相等、不可排序，不需要签名密钥；任何一项变化都会得到新值。
func Revision(s Snapshot) string {
	canon := struct {
		Org       OrgRef            `json:"org"`
		Projects  []ProjectRef      `json:"projects"`
		Policy    resources.Policy  `json:"policy"`
		Grants    []GrantRef        `json:"grants"`
		Culture   *resources.Entry  `json:"culture"`
		Resources []resources.Entry `json:"resources"`
	}{s.Org, s.Projects, s.Policy, s.Grants, s.Culture, s.Resources}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(canon)
	sum := sha256.Sum256(bytes.TrimRight(buf.Bytes(), "\n"))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Blob 读取内容，带可见性校验。
func (s *Service) Blob(ctx context.Context, orgID, userID, sha string) ([]byte, error) {
	return s.store.GetBlob(ctx, orgID, userID, sha)
}

// Report 接收回执。撤销的绑定仍可回执（它正在卸载），但不再更新 applied_revision 之外的状态。
func (s *Service) Report(ctx context.Context, b projects.Binding, revision, ip string, results []Result) error {
	for _, r := range results {
		if !ValidActions[r.Action] || !r.Kind.Valid() || r.Name == "" {
			return errors.New("sync: 回执条目非法")
		}
	}
	return s.store.RecordResults(ctx, b, revision, ip, results, s.now())
}
