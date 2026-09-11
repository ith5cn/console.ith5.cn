// Package changesets 实现变更集的状态机：草稿 → 审核 → 批准，以及审核作废规则。
//
// 设计见 docs/设计-变更集与发布.md。发布事务在 releases 包。
package changesets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ith5/ith5/internal/audit"
	"github.com/ith5/ith5/internal/organizations"
	"github.com/ith5/ith5/internal/resources"
)

// State 是变更集状态。
type State string

const (
	StateDraft     State = "draft"
	StateInReview  State = "in_review"
	StateApproved  State = "approved"
	StatePublished State = "published"
	StateRejected  State = "rejected"
	StateCancelled State = "cancelled"
)

// Terminal 表示不可再变。
func (s State) Terminal() bool {
	return s == StatePublished || s == StateRejected || s == StateCancelled
}

// OpKind 是操作类型。删除必须显式。
type OpKind string

const (
	OpPut    OpKind = "put"
	OpDelete OpKind = "delete"
)

// Op 是变更集里的一条操作。
type Op struct {
	Seq       int                 `json:"seq"`
	Op        OpKind              `json:"op"`
	Level     resources.Level     `json:"level"`
	TeamID    string              `json:"team_id,omitempty"`
	ProjectID string              `json:"project_id,omitempty"`
	Kind      resources.Kind      `json:"kind"`
	Name      string              `json:"name"`
	Files     []resources.FileRef `json:"files,omitempty"`
	// ExpectedPrevVersion 可选：作者声明基于第几版修改；发布时不一致即冲突。
	ExpectedPrevVersion *int `json:"expected_prev_version,omitempty"`
}

// OwnerID 返回该操作落点的 owner id；org 级返回空，由调用方填 orgID。
func (o Op) OwnerID(orgID string) string {
	switch o.Level {
	case resources.LevelTeam:
		return o.TeamID
	case resources.LevelProject:
		return o.ProjectID
	}
	return orgID
}

// Scope 返回该操作的权限作用域。
func (o Op) Scope(orgID string) organizations.Scope {
	return organizations.Scope{Level: o.Level, OwnerID: o.OwnerID(orgID)}
}

// ScopeKey 是 base_revisions / content_heads 的键：level:owner_id。
func ScopeKey(sc organizations.Scope) string { return string(sc.Level) + ":" + sc.OwnerID }

// Decision 是审核结论。
type Decision string

const (
	DecisionApprove        Decision = "approve"
	DecisionRequestChanges Decision = "request_changes"
	DecisionReject         Decision = "reject"
)

// Review 是一条审核记录。
type Review struct {
	ID            string
	ReviewerID    string
	ReviewerEmail string
	Decision      Decision
	Digest        string
	Superseded    bool
	Comment       string
	CreatedAt     time.Time
}

// Changeset 是一组对若干落点的操作。
type Changeset struct {
	ID              string
	OrgID           string
	AuthorID        string
	AuthorEmail     string
	State           State
	Title           string
	Description     string
	BaseRevisions   map[string]string
	SubmittedDigest string
	FastTrack       bool
	ReleaseID       string
	ETag            string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Ops             []Op
	Reviews         []Review
}

// Input 是创建或编辑时的输入。
type Input struct {
	Title       string
	Description string
	Ops         []Op
	FastTrack   bool
}

// ListFilter 是后台列表的筛选。
type ListFilter struct {
	State    State
	AuthorID string
	// ReviewableBy 非空时只列出该用户能审的（在审核中且非本人提交）。
	ReviewableBy string
	Limit        int
}

var (
	ErrNotFound      = errors.New("changesets: 记录不存在")
	ErrForbidden     = errors.New("changesets: 无权操作")
	ErrState         = errors.New("changesets: 当前状态不允许该操作")
	ErrETagMismatch  = errors.New("changesets: 对象已被修改")
	ErrInvalidInput  = errors.New("changesets: 输入非法")
	ErrDigest        = errors.New("changesets: 审核的摘要与当前内容不一致")
	ErrSelfReview    = errors.New("changesets: 不能审核自己的变更")
	ErrMissingBlob   = errors.New("changesets: 引用的内容尚未上传")
	ErrScopeNotFound = errors.New("changesets: 目标团队或项目不存在")
)

// Store 是变更集的持久化能力。
type Store interface {
	Create(ctx context.Context, cs Changeset) (string, error)
	Get(ctx context.Context, orgID, id string) (Changeset, error)
	List(ctx context.Context, orgID string, f ListFilter) ([]Changeset, error)
	// Update 覆盖标题、描述与操作，轮换 etag；ifMatch 不符返回 ErrETagMismatch。
	Update(ctx context.Context, cs Changeset, ifMatch string) error
	// SetState 改状态；digest 非 nil 时同时写 submitted_digest。
	SetState(ctx context.Context, orgID, id string, state State, digest *string) error
	AddReview(ctx context.Context, orgID, changesetID string, r Review) error
	SupersedeReviews(ctx context.Context, orgID, changesetID string) error
	// MissingBlobs 返回未上传的哈希。
	MissingBlobs(ctx context.Context, orgID string, shas []string) ([]string, error)
	// GetBlob 读内容，供发布口校验入口文件；不做可见性判断（只在校验作者自己上传的内容时用）。
	GetBlob(ctx context.Context, orgID, sha string) ([]byte, error)
	// HeadRevisions 返回各落点当前 revision；没有 head 的键不出现。
	HeadRevisions(ctx context.Context, orgID string, keys []string) (map[string]string, error)
	// ScopeExists 校验团队 / 项目属于本组织。
	ScopeExists(ctx context.Context, orgID string, sc organizations.Scope) (bool, error)
	// CurrentVersion 返回资源当前版本号与是否已删除；不存在返回 0,false,nil。
	CurrentVersion(ctx context.Context, orgID string, sc organizations.Scope, kind resources.Kind, name string) (version int, deleted bool, err error)
}

// PolicyReader 读组织的审核人数要求。
type PolicyReader interface {
	GetPolicy(ctx context.Context, orgID string) (organizations.Policy, error)
}

// Service 编排状态机。
type Service struct {
	store  Store
	policy PolicyReader
	audit  audit.Recorder
	now    func() time.Time
}

// NewService 构造。
func NewService(store Store, policy PolicyReader, rec audit.Recorder) *Service {
	if rec == nil {
		rec = audit.Nop{}
	}
	return &Service{store: store, policy: policy, audit: rec, now: time.Now}
}

// Store 暴露仓储。
func (s *Service) Store() Store { return s.store }

// Actor 是操作者。Email 只用于展示（如 learning 的 author），不参与任何判断。
type Actor struct {
	UserID    string
	Email     string
	OrgID     string
	RequestID string
	Subject   organizations.Subject
}

// ---------------------------------------------------------------
// 创建与编辑
// ---------------------------------------------------------------

// Create 建草稿。每个操作都要求作者对其落点有 resource:write；fast_track 还要求 release:publish。
func (s *Service) Create(ctx context.Context, a Actor, in Input) (Changeset, error) {
	if err := s.validate(ctx, a, in); err != nil {
		return Changeset{}, err
	}
	keys := scopeKeys(a.OrgID, in.Ops)
	heads, err := s.store.HeadRevisions(ctx, a.OrgID, keys)
	if err != nil {
		return Changeset{}, err
	}
	base := map[string]string{}
	for _, k := range keys {
		base[k] = heads[k] // 没有 head 的落点记空串，发布时视为「从无到有」
	}
	cs := Changeset{
		OrgID: a.OrgID, AuthorID: a.UserID, State: StateDraft, Title: strings.TrimSpace(in.Title),
		Description: strings.TrimSpace(in.Description), BaseRevisions: base, FastTrack: in.FastTrack, Ops: normalizeOps(in.Ops),
	}
	id, err := s.store.Create(ctx, cs)
	if err != nil {
		return Changeset{}, err
	}
	_ = s.record(ctx, a, "changeset.create", id, map[string]any{"ops": len(cs.Ops), "fast_track": in.FastTrack})
	return s.store.Get(ctx, a.OrgID, id)
}

// Edit 修改草稿。任何编辑都清空已有审核，APPROVED 打回 DRAFT。
func (s *Service) Edit(ctx context.Context, a Actor, id, ifMatch string, in Input) (Changeset, error) {
	cs, err := s.store.Get(ctx, a.OrgID, id)
	if err != nil {
		return Changeset{}, err
	}
	if cs.AuthorID != a.UserID {
		return Changeset{}, ErrForbidden
	}
	if cs.State.Terminal() {
		return Changeset{}, ErrState
	}
	if err := s.validate(ctx, a, in); err != nil {
		return Changeset{}, err
	}
	cs.Title, cs.Description, cs.Ops, cs.FastTrack = strings.TrimSpace(in.Title), strings.TrimSpace(in.Description), normalizeOps(in.Ops), in.FastTrack
	cs.State, cs.SubmittedDigest = StateDraft, ""
	if err := s.store.Update(ctx, cs, ifMatch); err != nil {
		return Changeset{}, err
	}
	if err := s.store.SupersedeReviews(ctx, a.OrgID, id); err != nil {
		return Changeset{}, err
	}
	_ = s.record(ctx, a, "changeset.edit", id, nil)
	return s.store.Get(ctx, a.OrgID, id)
}

// Submit 提交审核：算 digest 并冻结。fast_track 直接进入 APPROVED。
func (s *Service) Submit(ctx context.Context, a Actor, id string) (Changeset, error) {
	cs, err := s.store.Get(ctx, a.OrgID, id)
	if err != nil {
		return Changeset{}, err
	}
	if cs.AuthorID != a.UserID {
		return Changeset{}, ErrForbidden
	}
	if cs.State != StateDraft {
		return Changeset{}, ErrState
	}
	if len(cs.Ops) == 0 {
		return Changeset{}, fmt.Errorf("%w: 变更集没有操作", ErrInvalidInput)
	}
	digest := Digest(cs.Ops)
	next := StateInReview
	if cs.FastTrack {
		for _, op := range cs.Ops {
			if !CanFastTrack(a, op) {
				return Changeset{}, ErrForbidden
			}
		}
		next = StateApproved
	}
	if err := s.store.SetState(ctx, a.OrgID, id, next, &digest); err != nil {
		return Changeset{}, err
	}
	_ = s.record(ctx, a, "changeset.submit", id, map[string]any{"digest": digest, "state": next})
	return s.store.Get(ctx, a.OrgID, id)
}

// Review 记录审核。规则：在审核中、非作者、对每个落点有 review:decide、digest 一致。
func (s *Service) Review(ctx context.Context, a Actor, id string, decision Decision, digest, comment string) (Changeset, error) {
	cs, err := s.store.Get(ctx, a.OrgID, id)
	if err != nil {
		return Changeset{}, err
	}
	if cs.State != StateInReview {
		return Changeset{}, ErrState
	}
	if cs.AuthorID == a.UserID {
		return Changeset{}, ErrSelfReview
	}
	for _, op := range cs.Ops {
		if !a.Subject.Can(organizations.PermReviewDecide, op.Scope(a.OrgID)) {
			return Changeset{}, ErrForbidden
		}
	}
	if digest != cs.SubmittedDigest {
		return Changeset{}, ErrDigest
	}
	switch decision {
	case DecisionApprove, DecisionRequestChanges, DecisionReject:
	default:
		return Changeset{}, fmt.Errorf("%w: decision 非法", ErrInvalidInput)
	}
	if err := s.store.AddReview(ctx, a.OrgID, id, Review{ReviewerID: a.UserID, Decision: decision, Digest: digest, Comment: strings.TrimSpace(comment)}); err != nil {
		return Changeset{}, err
	}
	var next State
	switch decision {
	case DecisionReject:
		next = StateRejected
	case DecisionRequestChanges:
		next = StateDraft
		if err := s.store.SupersedeReviews(ctx, a.OrgID, id); err != nil {
			return Changeset{}, err
		}
	case DecisionApprove:
		pol, err := s.policy.GetPolicy(ctx, a.OrgID)
		if err != nil {
			return Changeset{}, err
		}
		cur, err := s.store.Get(ctx, a.OrgID, id)
		if err != nil {
			return Changeset{}, err
		}
		approvals := 0
		seen := map[string]bool{}
		for _, r := range cur.Reviews {
			if !r.Superseded && r.Decision == DecisionApprove && r.Digest == cs.SubmittedDigest && !seen[r.ReviewerID] {
				seen[r.ReviewerID] = true
				approvals++
			}
		}
		if approvals < pol.RequiredApprovals {
			_ = s.record(ctx, a, "changeset.review", id, map[string]any{"decision": decision, "approvals": approvals})
			return cur, nil
		}
		next = StateApproved
	}
	if err := s.store.SetState(ctx, a.OrgID, id, next, nil); err != nil {
		return Changeset{}, err
	}
	_ = s.record(ctx, a, "changeset.review", id, map[string]any{"decision": decision, "state": next})
	return s.store.Get(ctx, a.OrgID, id)
}

// CanFastTrack 判断某人能否对某操作跳过审核。
//
// 两条通道：有 release:publish 的人对任何操作都可以；只含 learning 的操作，
// 有 learning:contribute 的人也可以——这是「learnings 直接发布」的落地（docs/设计-知识与上报面.md §2）。
func CanFastTrack(a Actor, op Op) bool {
	sc := op.Scope(a.OrgID)
	if a.Subject.Can(organizations.PermReleasePublish, sc) {
		return true
	}
	return op.Kind == resources.KindLearning && a.Subject.Can(organizations.PermLearningContribute, sc)
}

// LearningOnly 判断变更集是否只碰 learning。
func LearningOnly(ops []Op) bool {
	if len(ops) == 0 {
		return false
	}
	for _, op := range ops {
		if op.Kind != resources.KindLearning {
			return false
		}
	}
	return true
}

// Cancel 作者或组织 admin 取消非终态变更集。
func (s *Service) Cancel(ctx context.Context, a Actor, id string) error {
	cs, err := s.store.Get(ctx, a.OrgID, id)
	if err != nil {
		return err
	}
	if cs.AuthorID != a.UserID && !a.Subject.Can(organizations.PermMembershipManage, organizations.OrgScope(a.OrgID)) {
		return ErrForbidden
	}
	if cs.State.Terminal() {
		return ErrState
	}
	if err := s.store.SetState(ctx, a.OrgID, id, StateCancelled, nil); err != nil {
		return err
	}
	return s.record(ctx, a, "changeset.cancel", id, nil)
}

// Get 读取；作者、对任一落点有读权限的人可见。
func (s *Service) Get(ctx context.Context, a Actor, id string) (Changeset, error) {
	cs, err := s.store.Get(ctx, a.OrgID, id)
	if err != nil {
		return Changeset{}, err
	}
	if cs.AuthorID == a.UserID {
		return cs, nil
	}
	for _, op := range cs.Ops {
		if a.Subject.Can(organizations.PermProjectRead, op.Scope(a.OrgID)) {
			return cs, nil
		}
	}
	return Changeset{}, ErrNotFound
}

// ---------------------------------------------------------------
// 校验与摘要
// ---------------------------------------------------------------

// validate 逐条检查操作：类型、名字、落点存在、权限、文件清单、blob 已上传、入口文件内容合法。
func (s *Service) validate(ctx context.Context, a Actor, in Input) error {
	if strings.TrimSpace(in.Title) == "" || len(in.Title) > 200 {
		return fmt.Errorf("%w: 标题长度 1 到 200", ErrInvalidInput)
	}
	if len(in.Ops) > 100 {
		return fmt.Errorf("%w: 一个变更集最多 100 个操作", ErrInvalidInput)
	}
	seen := map[string]bool{}
	var shas []string
	for i, op := range in.Ops {
		if !op.Level.Valid() || !op.Kind.Valid() {
			return fmt.Errorf("%w: 操作 %d 的 level 或 kind 非法", ErrInvalidInput, i)
		}
		if err := resources.ValidateName(op.Kind, op.Name); err != nil {
			return fmt.Errorf("%w: 操作 %d: %v", ErrInvalidInput, i, err)
		}
		sc := op.Scope(a.OrgID)
		if (op.Level == resources.LevelTeam && op.TeamID == "") || (op.Level == resources.LevelProject && op.ProjectID == "") {
			return fmt.Errorf("%w: 操作 %d 缺少落点 id", ErrInvalidInput, i)
		}
		ok, err := s.store.ScopeExists(ctx, a.OrgID, sc)
		if err != nil {
			return err
		}
		if !ok {
			return ErrScopeNotFound
		}
		if !a.Subject.Can(organizations.PermResourceWrite, sc) {
			return ErrForbidden
		}
		key := ScopeKey(sc) + "/" + string(op.Kind) + "/" + op.Name
		if seen[key] {
			return fmt.Errorf("%w: 操作 %d 与之前的操作指向同一资源", ErrInvalidInput, i)
		}
		seen[key] = true
		switch op.Op {
		case OpPut:
			paths := make([]string, len(op.Files))
			for j, f := range op.Files {
				paths[j] = f.Path
				shas = append(shas, f.SHA256)
			}
			if err := resources.ValidateFiles(op.Kind, paths); err != nil {
				return fmt.Errorf("%w: 操作 %d: %v", ErrInvalidInput, i, err)
			}
		case OpDelete:
			if len(op.Files) != 0 {
				return fmt.Errorf("%w: 操作 %d: delete 不带文件", ErrInvalidInput, i)
			}
			v, deleted, err := s.store.CurrentVersion(ctx, a.OrgID, sc, op.Kind, op.Name)
			if err != nil {
				return err
			}
			if v == 0 || deleted {
				return fmt.Errorf("%w: 操作 %d: 要删除的资源不存在", ErrInvalidInput, i)
			}
		default:
			return fmt.Errorf("%w: 操作 %d 的 op 非法", ErrInvalidInput, i)
		}
	}
	if len(shas) > 0 {
		missing, err := s.store.MissingBlobs(ctx, a.OrgID, shas)
		if err != nil {
			return err
		}
		if len(missing) > 0 {
			return fmt.Errorf("%w: %s", ErrMissingBlob, strings.Join(missing, ", "))
		}
	}
	// 入口文件内容校验：与发布口同一套规则，错误挡在提交前而不是发布时
	for i, op := range in.Ops {
		if op.Op != OpPut {
			continue
		}
		// 入口文件必读；其余文本文件也读出来做密钥扫描，二进制与超大文件只带路径
		var files []resources.File
		for _, f := range op.Files {
			isEntry := f.Path == op.Kind.EntryFile() || (op.Kind == resources.KindSkill && f.Path == "SKILL.md")
			if !isEntry && (!resources.IsTextPath(f.Path) || f.Size > resources.MaxScanBytes) {
				files = append(files, resources.File{Path: f.Path})
				continue
			}
			b, err := s.store.GetBlob(ctx, a.OrgID, f.SHA256)
			if err != nil {
				return err
			}
			files = append(files, resources.File{Path: f.Path, Content: string(b)})
		}
		if err := resources.ValidateForPublish(op.Kind, op.Name, files); err != nil {
			var secret *resources.SecretError
			if errors.As(err, &secret) {
				return fmt.Errorf("操作 %d: %w", i, err)
			}
			return fmt.Errorf("%w: 操作 %d: %v", ErrInvalidInput, i, err)
		}
	}
	return nil
}

// Digest 对操作集合做规范化 SHA-256：审核记录必须匹配它才生效。
func Digest(ops []Op) string {
	canon := normalizeOps(ops)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(canon)
	sum := sha256.Sum256(bytes.TrimRight(buf.Bytes(), "\n"))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// normalizeOps 排序并重编 seq，文件清单规范化，使同一内容得到同一摘要。
func normalizeOps(ops []Op) []Op {
	out := make([]Op, len(ops))
	copy(out, ops)
	for i := range out {
		out[i].Files = resources.CanonicalRefs(out[i].Files)
		if len(out[i].Files) == 0 {
			out[i].Files = nil
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Level != b.Level {
			return a.Level < b.Level
		}
		if a.TeamID+a.ProjectID != b.TeamID+b.ProjectID {
			return a.TeamID+a.ProjectID < b.TeamID+b.ProjectID
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Name < b.Name
	})
	for i := range out {
		out[i].Seq = i + 1
	}
	return out
}

func scopeKeys(orgID string, ops []Op) []string {
	seen := map[string]bool{}
	var keys []string
	for _, op := range ops {
		k := ScopeKey(op.Scope(orgID))
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func (s *Service) record(ctx context.Context, a Actor, typ, id string, detail map[string]any) error {
	return s.audit.Record(ctx, audit.Event{
		OrgID: a.OrgID, ActorUserID: a.UserID, Type: typ, TargetType: "changeset", TargetID: id,
		Detail: detail, RequestID: a.RequestID,
	})
}
