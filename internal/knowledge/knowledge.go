// Package knowledge 负责 learnings：直接发布的经验分享、密钥扫描、归档、晋升、置信度与检索。
//
// learning 是一种资源 kind，走变更集与发布事务；本包只是在其上叠加权限通道与知识库视角
// （docs/设计-知识与上报面.md §2、§5）。
package knowledge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/ith5/ith5/internal/audit"
	"github.com/ith5/ith5/internal/changesets"
	"github.com/ith5/ith5/internal/organizations"
	"github.com/ith5/ith5/internal/releases"
	"github.com/ith5/ith5/internal/resources"
)

// Learning 是知识库里的一条。
type Learning struct {
	ID        string          `json:"id"`
	Level     resources.Level `json:"level"`
	OwnerID   string          `json:"owner_id"`
	Namespace string          `json:"namespace"`
	Name      string          `json:"name"`
	Title     string          `json:"title"`
	Author    string          `json:"author"`
	// AuthorID 是首版发布者的成员 id，用于「作者本人」的权限判断；Author 只是展示名。
	AuthorID    string     `json:"author_id"`
	Tags        []string   `json:"tags"`
	Version     int        `json:"version"`
	VersionID   string     `json:"version_id"`
	Archived    bool       `json:"archived"` // 当前版本是 tombstone
	PublishedAt time.Time  `json:"published_at"`
	Recalled    int        `json:"recalled"`
	Upvoted     int        `json:"upvoted"`
	LastRecall  *time.Time `json:"last_recall,omitempty"`
	Confidence  float64    `json:"confidence"`
	// Excerpt 是检索时的正文摘要。
	Excerpt string `json:"excerpt"`
}

// ListFilter 是检索条件。
type ListFilter struct {
	ProjectID string
	Query     string
	// Status: active | archived | all
	Status string
	Limit  int
}

// Health 是知识库健康报告。
type Health struct {
	ByKind            map[string]int `json:"by_kind"`
	TopRecalled       []Learning     `json:"top_recalled"`
	Silent            []Learning     `json:"silent"`
	PruneCandidates   []Learning     `json:"prune_candidates"`
	PromoteCandidates []Learning     `json:"promote_candidates"`
	ByAuthor          map[string]int `json:"by_author"`
	RecallTrend       []DayCount     `json:"recall_trend"`
}

// DayCount 是按天的计数。
type DayCount struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

// SecretHit 与 ErrSecretDetected 沿用 resources 包的定义：所有 kind 用同一套扫描。
type SecretHit = resources.SecretHit

// ErrSecretDetected 表示内容里疑似有密钥；按设计拒绝而不是打码。
type ErrSecretDetected = resources.SecretError

var (
	ErrNotFound  = errors.New("knowledge: 记录不存在")
	ErrForbidden = errors.New("knowledge: 无权操作")
	ErrInvalid   = errors.New("knowledge: 输入非法")
)

// Store 是知识库的读能力；写全部经变更集。
type Store interface {
	List(ctx context.Context, orgID string, f ListFilter) ([]Learning, error)
	Get(ctx context.Context, orgID, bundleID string) (Learning, error)
	// Content 读当前版本入口文件正文。
	Content(ctx context.Context, orgID, bundleID string) (string, error)
	Health(ctx context.Context, orgID string, policy organizations.Policy) (Health, error)
}

// Service 编排贡献、归档、晋升。
type Service struct {
	store    Store
	cs       *changesets.Service
	releases *releases.Service
	blobs    BlobWriter
	policy   changesets.PolicyReader
	audit    audit.Recorder
	now      func() time.Time
}

// BlobWriter 写内容，供贡献时上传正文。
type BlobWriter interface {
	PutBlob(ctx context.Context, orgID, sha string, content []byte, maxBytes int) error
}

// NewService 构造。
func NewService(store Store, cs *changesets.Service, rel *releases.Service, blobs BlobWriter, policy changesets.PolicyReader, rec audit.Recorder) *Service {
	if rec == nil {
		rec = audit.Nop{}
	}
	return &Service{store: store, cs: cs, releases: rel, blobs: blobs, policy: policy, audit: rec, now: time.Now}
}

// Store 暴露读仓储。
func (s *Service) Store() Store { return s.store }

// ContributeInput 是一次经验分享。
type ContributeInput struct {
	ProjectID  string // 空 = 组织级共享
	Title      string
	Content    string // 完整 Markdown，可含 frontmatter；缺 frontmatter 时自动补
	Tags       []string
	Supersedes []string // 被取代的 learning 资源 id
}

// Contribute 分享一条经验：密钥扫描 → 上传正文 → 变更集（put learning，可选 delete 被取代的）→
// 按组织策略直接发布或进入审核。
func (s *Service) Contribute(ctx context.Context, a changesets.Actor, in ContributeInput) (changesets.Changeset, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" || len(title) > 200 {
		return changesets.Changeset{}, fmt.Errorf("%w: 标题长度 1 到 200", ErrInvalid)
	}
	if strings.TrimSpace(in.Content) == "" || len(in.Content) > 256<<10 {
		return changesets.Changeset{}, fmt.Errorf("%w: 正文为空或超过 256 KiB", ErrInvalid)
	}
	if hits := ScanSecrets(in.Content); len(hits) > 0 {
		return changesets.Changeset{}, &ErrSecretDetected{Hits: hits}
	}
	content := ensureFrontmatter(in.Content, title, a.Email, in.Tags, s.now())
	sha := resources.BlobSum([]byte(content))
	if err := s.blobs.PutBlob(ctx, a.OrgID, sha, []byte(content), 32<<20); err != nil {
		return changesets.Changeset{}, err
	}
	name, err := LearningName(title, s.now())
	if err != nil {
		return changesets.Changeset{}, err
	}
	level, projectID := resources.LevelOrg, ""
	if in.ProjectID != "" {
		level, projectID = resources.LevelProject, in.ProjectID
	}
	ops := []changesets.Op{{
		Op: changesets.OpPut, Level: level, ProjectID: projectID, Kind: resources.KindLearning, Name: name,
		Files: []resources.FileRef{{Path: "LEARNING.md", SHA256: sha, Size: len(content)}},
	}}
	for _, id := range in.Supersedes {
		old, err := s.store.Get(ctx, a.OrgID, id)
		if err != nil {
			return changesets.Changeset{}, fmt.Errorf("%w: 被取代的 learning %s 不存在", ErrInvalid, id)
		}
		if old.Archived {
			continue
		}
		ops = append(ops, deleteOp(old))
	}
	pol, err := s.policy.GetPolicy(ctx, a.OrgID)
	if err != nil {
		return changesets.Changeset{}, err
	}
	cs, err := s.cs.Create(ctx, a, changesets.Input{Title: "分享经验：" + title, Ops: ops, FastTrack: !pol.LearningsReview})
	if err != nil {
		return changesets.Changeset{}, err
	}
	cs, err = s.cs.Submit(ctx, a, cs.ID)
	if err != nil {
		return changesets.Changeset{}, err
	}
	if cs.State == changesets.StateApproved {
		if _, err := s.releases.Publish(ctx, a, cs.ID); err != nil {
			return changesets.Changeset{}, err
		}
		cs, err = s.cs.Store().Get(ctx, a.OrgID, cs.ID)
		if err != nil {
			return changesets.Changeset{}, err
		}
	}
	_ = s.audit.Record(ctx, audit.Event{
		OrgID: a.OrgID, ActorUserID: a.UserID, Type: "learning.contribute", TargetType: "changeset", TargetID: cs.ID,
		Detail: map[string]any{"name": name, "project_id": projectID, "state": cs.State}, RequestID: a.RequestID,
	})
	return cs, nil
}

// Archive 归档：作者或对该落点有发布权限的人，发布一条 tombstone。
func (s *Service) Archive(ctx context.Context, a changesets.Actor, bundleID string) (changesets.Changeset, error) {
	l, err := s.store.Get(ctx, a.OrgID, bundleID)
	if err != nil {
		return changesets.Changeset{}, err
	}
	if l.Archived {
		return changesets.Changeset{}, fmt.Errorf("%w: 已归档", ErrInvalid)
	}
	sc := organizations.Scope{Level: l.Level, OwnerID: l.OwnerID}
	if l.AuthorID != a.UserID && !a.Subject.Can(organizations.PermReleasePublish, sc) {
		return changesets.Changeset{}, ErrForbidden
	}
	cs, err := s.cs.Create(ctx, a, changesets.Input{Title: "归档经验：" + l.Title, Ops: []changesets.Op{deleteOp(l)}, FastTrack: true})
	if err != nil {
		return changesets.Changeset{}, err
	}
	if cs, err = s.cs.Submit(ctx, a, cs.ID); err != nil {
		return changesets.Changeset{}, err
	}
	if _, err := s.releases.Publish(ctx, a, cs.ID); err != nil {
		return changesets.Changeset{}, err
	}
	_ = s.audit.Record(ctx, audit.Event{OrgID: a.OrgID, ActorUserID: a.UserID, Type: "learning.archive", TargetType: "resource", TargetID: bundleID, RequestID: a.RequestID})
	return s.cs.Store().Get(ctx, a.OrgID, cs.ID)
}

// Promote 把 learning 晋升为正式资源：生成一个含 put 的草稿变更集，走正常审核。
// target 只能是 rule / doc / skill；正文去掉 learning 的 frontmatter 后作为入口内容。
func (s *Service) Promote(ctx context.Context, a changesets.Actor, bundleID string, target resources.Kind, name string) (changesets.Changeset, error) {
	l, err := s.store.Get(ctx, a.OrgID, bundleID)
	if err != nil {
		return changesets.Changeset{}, err
	}
	body, err := s.store.Content(ctx, a.OrgID, bundleID)
	if err != nil {
		return changesets.Changeset{}, err
	}
	var content string
	switch target {
	case resources.KindRule, resources.KindDoc:
		content = "# " + l.Title + "\n\n" + stripFrontmatter(body)
	case resources.KindSkill:
		content = "---\nname: " + name + "\ndescription: " + oneLine(l.Title) + "\n---\n\n" + stripFrontmatter(body)
	default:
		return changesets.Changeset{}, fmt.Errorf("%w: 只能晋升为 rule、doc 或 skill", ErrInvalid)
	}
	sha := resources.BlobSum([]byte(content))
	if err := s.blobs.PutBlob(ctx, a.OrgID, sha, []byte(content), 32<<20); err != nil {
		return changesets.Changeset{}, err
	}
	op := changesets.Op{Op: changesets.OpPut, Level: l.Level, Kind: target, Name: name,
		Files: []resources.FileRef{{Path: target.EntryFile(), SHA256: sha, Size: len(content)}}}
	if l.Level == resources.LevelProject {
		op.ProjectID = l.OwnerID
	} else if l.Level == resources.LevelTeam {
		op.TeamID = l.OwnerID
	}
	cs, err := s.cs.Create(ctx, a, changesets.Input{Title: "晋升经验：" + l.Title, Description: "来自 learning " + l.Name, Ops: []changesets.Op{op}})
	if err != nil {
		return changesets.Changeset{}, err
	}
	_ = s.audit.Record(ctx, audit.Event{OrgID: a.OrgID, ActorUserID: a.UserID, Type: "learning.promote", TargetType: "resource", TargetID: bundleID,
		Detail: map[string]any{"changeset_id": cs.ID, "target": target}, RequestID: a.RequestID})
	return cs, nil
}

func deleteOp(l Learning) changesets.Op {
	op := changesets.Op{Op: changesets.OpDelete, Level: l.Level, Kind: resources.KindLearning, Name: l.Name}
	switch l.Level {
	case resources.LevelProject:
		op.ProjectID = l.OwnerID
	case resources.LevelTeam:
		op.TeamID = l.OwnerID
	}
	return op
}

// ---------------------------------------------------------------
// 密钥扫描
// ---------------------------------------------------------------

// ScanSecrets 见 resources.ScanSecrets。
func ScanSecrets(content string) []SecretHit { return resources.ScanSecrets(content) }

// ---------------------------------------------------------------
// 命名、frontmatter、置信度
// ---------------------------------------------------------------

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// LearningName 沿用 teamai 的命名：<title-slug>-<date>-<random>。中文标题拿不到 slug 时用 learning。
func LearningName(title string, now time.Time) (string, error) {
	slug := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if len(slug) > 40 {
		slug = strings.TrimRight(slug[:40], "-")
	}
	if slug == "" {
		slug = "learning"
	}
	b := make([]byte, 2)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s-%s", slug, now.Format("2006-01-02"), hex.EncodeToString(b)), nil
}

// ensureFrontmatter 缺 frontmatter 时补上 title / author / date / tags；有则原样保留。
func ensureFrontmatter(content, title, author string, tags []string, now time.Time) string {
	if strings.HasPrefix(strings.TrimLeft(content, "\uFEFF \t\r\n"), "---") {
		return content
	}
	var sb strings.Builder
	sb.WriteString("---\ntitle: \"" + strings.ReplaceAll(title, `"`, `\"`) + "\"\n")
	sb.WriteString("author: " + author + "\n")
	sb.WriteString("date: " + now.Format("2006-01-02") + "\n")
	if len(tags) > 0 {
		sb.WriteString("tags: [" + strings.Join(tags, ", ") + "]\n")
	}
	sb.WriteString("---\n\n")
	sb.WriteString(content)
	return sb.String()
}

func stripFrontmatter(content string) string {
	s := strings.TrimLeft(content, "\uFEFF \t\r\n")
	if !strings.HasPrefix(s, "---") {
		return content
	}
	rest := s[3:]
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[i+1:]
	}
	if end := strings.Index(rest, "\n---"); end >= 0 {
		rest = rest[end+4:]
	}
	return strings.TrimLeft(rest, "\r\n")
}

func oneLine(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), ":", "：")
}

// Confidence 计算置信度（docs/设计-知识与上报面.md §5）。
func Confidence(recalled, upvoted int, lastRecall *time.Time, now time.Time) float64 {
	decay := 1.0
	if lastRecall != nil {
		months := now.Sub(*lastRecall).Hours() / (24 * 30)
		decay = math.Pow(0.5, months/6)
	}
	c := float64(upvoted+1) / float64(recalled+2) * decay
	return math.Max(0, math.Min(1, c))
}
