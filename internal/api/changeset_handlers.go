package api

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ith5/ith5/internal/changesets"
	"github.com/ith5/ith5/internal/db"
	"github.com/ith5/ith5/internal/organizations"
	"github.com/ith5/ith5/internal/platform/httpx"
	"github.com/ith5/ith5/internal/releases"
	"github.com/ith5/ith5/internal/resources"
	"github.com/ith5/ith5/internal/sync"
)

// ---- 请求 / 响应 ----

// OpJSON 是变更集操作。
type OpJSON struct {
	Seq                 int                 `json:"seq,omitempty"`
	Op                  string              `json:"op"`
	Level               string              `json:"level"`
	TeamID              string              `json:"team_id,omitempty"`
	ProjectID           string              `json:"project_id,omitempty"`
	Kind                string              `json:"kind"`
	Name                string              `json:"name"`
	Files               []resources.FileRef `json:"files,omitempty"`
	ExpectedPrevVersion *int                `json:"expected_prev_version,omitempty"`
}

// ChangesetReq 创建或编辑变更集。
type ChangesetReq struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Ops         []OpJSON `json:"ops"`
	FastTrack   bool     `json:"fast_track,omitempty"`
}

// ReviewJSON 是一条审核。
type ReviewJSON struct {
	ID            string    `json:"id"`
	ReviewerEmail string    `json:"reviewer_email"`
	Decision      string    `json:"decision"`
	Digest        string    `json:"digest"`
	Superseded    bool      `json:"superseded"`
	Comment       string    `json:"comment"`
	CreatedAt     time.Time `json:"created_at"`
}

// ChangesetJSON 是变更集。
type ChangesetJSON struct {
	ID              string            `json:"id"`
	AuthorEmail     string            `json:"author_email"`
	State           string            `json:"state"`
	Title           string            `json:"title"`
	Description     string            `json:"description"`
	BaseRevisions   map[string]string `json:"base_revisions"`
	SubmittedDigest string            `json:"submitted_digest,omitempty"`
	FastTrack       bool              `json:"fast_track"`
	ReleaseID       string            `json:"release_id,omitempty"`
	ETag            string            `json:"etag"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
	Ops             []OpJSON          `json:"ops"`
	Reviews         []ReviewJSON      `json:"reviews"`
}

// ReviewReq 提交审核。
type ReviewReq struct {
	Decision string `json:"decision"`
	Digest   string `json:"digest"`
	Comment  string `json:"comment,omitempty"`
}

// ReleaseJSON 是一次发布。
type ReleaseJSON struct {
	ID             string            `json:"id"`
	ChangesetID    string            `json:"changeset_id"`
	PublisherEmail string            `json:"publisher_email"`
	PublishedAt    time.Time         `json:"published_at"`
	Items          []releases.Item   `json:"items"`
	Revisions      map[string]string `json:"revisions"`
}

// RollbackReq 生成回滚变更集。
type RollbackReq struct {
	FastTrack bool `json:"fast_track,omitempty"`
}

// ResourceJSON 是后台列表里的资源。
type ResourceJSON struct {
	ID          string              `json:"id"`
	Level       string              `json:"level"`
	OwnerID     string              `json:"owner_id"`
	Namespace   string              `json:"namespace"`
	Kind        string              `json:"kind"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Tags        []string            `json:"tags"`
	Groups      []string            `json:"groups"`
	Archived    bool                `json:"archived"`
	Version     int                 `json:"version"`
	VersionID   string              `json:"version_id"`
	Checksum    string              `json:"checksum"`
	Deleted     bool                `json:"deleted"`
	Files       []resources.FileRef `json:"files"`
	PublishedAt *time.Time          `json:"published_at,omitempty"`
	UpdatedAt   time.Time           `json:"updated_at"`
}

// VersionJSON 是版本历史里的一条。
type VersionJSON struct {
	ID                string              `json:"id"`
	Version           int                 `json:"version"`
	Files             []resources.FileRef `json:"files"`
	Checksum          string              `json:"checksum"`
	Deleted           bool                `json:"deleted"`
	RollbackOfVersion *int                `json:"rollback_of_version,omitempty"`
	ReleaseID         string              `json:"release_id,omitempty"`
	PublishedBy       string              `json:"published_by,omitempty"`
	PublishedAt       time.Time           `json:"published_at"`
	// Contents 只在读单个版本时填充：小于 64 KiB 的文本文件内联，其余给哈希自己取。
	Contents map[string]string `json:"contents,omitempty"`
}

// TagsReq 覆盖标签。
type TagsReq struct {
	Tags []string `json:"tags"`
}

const inlineContentLimit = 64 << 10

// ---------------------------------------------------------------
// blob 上传
// ---------------------------------------------------------------

// uploadBlob 接收原始字节；头 X-Sha256 声明哈希，不符则 422。任何组织成员都可上传（内容不可见前无害）。
func (s *Server) uploadBlob(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	sha := strings.TrimSpace(r.Header.Get("X-Sha256"))
	if !strings.HasPrefix(sha, "sha256:") {
		httpx.WriteError(w, r, s.log, httpx.Validation("需要 X-Sha256 头，形如 sha256:<hex>"))
		return
	}
	limit := sync.DefaultLimits.MaxBlobBytes
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, int64(limit)+1))
	if err != nil || len(body) > limit {
		httpx.WriteError(w, r, s.log, httpx.New(http.StatusRequestEntityTooLarge, httpx.CodePayloadTooLarge, "内容超过上限"))
		return
	}
	if err := s.Resources.PutBlob(r.Context(), p.OrgID, sha, body, limit); err != nil {
		switch {
		case errors.Is(err, db.ErrBlobHashMismatch):
			httpx.WriteError(w, r, s.log, httpx.Validation("哈希与内容不符"))
		case errors.Is(err, db.ErrBlobTooLarge):
			httpx.WriteError(w, r, s.log, httpx.New(http.StatusRequestEntityTooLarge, httpx.CodePayloadTooLarge, "内容超过上限"))
		default:
			httpx.WriteError(w, r, s.log, err)
		}
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"sha256": sha, "size": len(body)})
}

// ---------------------------------------------------------------
// 变更集
// ---------------------------------------------------------------

func (p Principal) csActor(r *http.Request) changesets.Actor {
	a := p.orgActor(r)
	return changesets.Actor{UserID: a.UserID, Email: p.Email, OrgID: a.OrgID, RequestID: a.RequestID, Subject: a.Subject}
}

func toInput(req ChangesetReq) changesets.Input {
	in := changesets.Input{Title: req.Title, Description: req.Description, FastTrack: req.FastTrack}
	for _, o := range req.Ops {
		in.Ops = append(in.Ops, changesets.Op{
			Op: changesets.OpKind(o.Op), Level: resources.Level(o.Level), TeamID: o.TeamID, ProjectID: o.ProjectID,
			Kind: resources.Kind(o.Kind), Name: o.Name, Files: o.Files, ExpectedPrevVersion: o.ExpectedPrevVersion,
		})
	}
	return in
}

func (s *Server) listChangesets(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	q := r.URL.Query()
	limit := parsePage(r).dbLimit()
	f := changesets.ListFilter{State: changesets.State(q.Get("state")), Limit: limit}
	switch q.Get("view") {
	case "mine":
		f.AuthorID = p.UserID
	case "review":
		f.ReviewableBy = p.UserID
	default:
		if !p.Subject.Can(organizations.PermAuditRead, organizations.OrgScope(p.OrgID)) {
			f.AuthorID = p.UserID
		}
	}
	list, err := s.Changesets.Store().List(r.Context(), p.OrgID, f)
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	out := make([]ChangesetJSON, 0, len(list))
	for _, cs := range list {
		if f.ReviewableBy != "" && !s.canReviewAll(p, cs) {
			continue
		}
		out = append(out, changesetJSON(cs))
	}
	httpx.WriteJSON(w, http.StatusOK, pageSlice(out, parsePage(r)))
}

func (s *Server) canReviewAll(p Principal, cs changesets.Changeset) bool {
	for _, op := range cs.Ops {
		if !p.Subject.Can(organizations.PermReviewDecide, op.Scope(p.OrgID)) {
			return false
		}
	}
	return true
}

func (s *Server) createChangeset(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var req ChangesetReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	cs, err := s.Changesets.Create(r.Context(), p.csActor(r), toInput(req))
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	w.Header().Set("ETag", quote(cs.ETag))
	httpx.WriteJSON(w, http.StatusCreated, changesetJSON(cs))
}

func (s *Server) getChangeset(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	cs, err := s.Changesets.Get(r.Context(), p.csActor(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	w.Header().Set("ETag", quote(cs.ETag))
	httpx.WriteJSON(w, http.StatusOK, changesetJSON(cs))
}

func (s *Server) updateChangeset(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	ifMatch := unquote(r.Header.Get("If-Match"))
	if ifMatch == "" {
		httpx.WriteError(w, r, s.log, httpx.New(http.StatusPreconditionRequired, httpx.CodePreconditionRequired, "需要 If-Match"))
		return
	}
	var req ChangesetReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	cs, err := s.Changesets.Edit(r.Context(), p.csActor(r), chi.URLParam(r, "id"), ifMatch, toInput(req))
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	w.Header().Set("ETag", quote(cs.ETag))
	httpx.WriteJSON(w, http.StatusOK, changesetJSON(cs))
}

func (s *Server) submitChangeset(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	cs, err := s.Changesets.Submit(r.Context(), p.csActor(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, changesetJSON(cs))
}

func (s *Server) reviewChangeset(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var req ReviewReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	cs, err := s.Changesets.Review(r.Context(), p.csActor(r), chi.URLParam(r, "id"), changesets.Decision(req.Decision), req.Digest, req.Comment)
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, changesetJSON(cs))
}

func (s *Server) cancelChangeset(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if err := s.Changesets.Cancel(r.Context(), p.csActor(r), chi.URLParam(r, "id")); err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

// publishChangeset 发布 APPROVED 的变更集；基线冲突返回 412 并附冲突详情。
func (s *Server) publishChangeset(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	rel, err := s.Releases.Publish(r.Context(), p.csActor(r), chi.URLParam(r, "id"))
	if err != nil {
		var mismatch *releases.ErrRevisionMismatch
		if errors.As(err, &mismatch) {
			httpx.WriteError(w, r, s.log, httpx.New(http.StatusPreconditionFailed, httpx.CodeRevisionMismatch,
				"基线已过期且与之后的发布重叠，请基于最新内容重建变更集").WithDetails(mismatch.Conflicts))
			return
		}
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, releaseJSON(rel))
}

// diffChangeset 列出每个操作相对当前版本的变化：新增、更新（含旧版本号）、删除。
func (s *Server) diffChangeset(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	cs, err := s.Changesets.Get(r.Context(), p.csActor(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	type diffItem struct {
		Op             string              `json:"op"`
		Kind           string              `json:"kind"`
		Name           string              `json:"name"`
		ScopeKey       string              `json:"scope_key"`
		Change         string              `json:"change"` // create | update | delete | noop
		CurrentVersion int                 `json:"current_version,omitempty"`
		Files          []resources.FileRef `json:"files,omitempty"`
	}
	out := make([]diffItem, 0, len(cs.Ops))
	for _, op := range cs.Ops {
		sc := op.Scope(p.OrgID)
		cur, deleted, err := s.Changesets.Store().CurrentVersion(r.Context(), p.OrgID, sc, op.Kind, op.Name)
		if err != nil {
			httpx.WriteError(w, r, s.log, err)
			return
		}
		d := diffItem{Op: string(op.Op), Kind: string(op.Kind), Name: op.Name, ScopeKey: changesets.ScopeKey(sc), CurrentVersion: cur, Files: op.Files}
		switch {
		case op.Op == changesets.OpDelete:
			d.Change = "delete"
		case cur == 0 || deleted:
			d.Change = "create"
		default:
			d.Change = "update"
		}
		out = append(out, d)
	}
	httpx.WriteJSON(w, http.StatusOK, pageSlice(out, parsePage(r)))
}

func changesetJSON(cs changesets.Changeset) ChangesetJSON {
	out := ChangesetJSON{
		ID: cs.ID, AuthorEmail: cs.AuthorEmail, State: string(cs.State), Title: cs.Title, Description: cs.Description,
		BaseRevisions: cs.BaseRevisions, SubmittedDigest: cs.SubmittedDigest, FastTrack: cs.FastTrack, ReleaseID: cs.ReleaseID,
		ETag: cs.ETag, CreatedAt: cs.CreatedAt, UpdatedAt: cs.UpdatedAt, Ops: []OpJSON{}, Reviews: []ReviewJSON{},
	}
	for _, o := range cs.Ops {
		out.Ops = append(out.Ops, OpJSON{
			Seq: o.Seq, Op: string(o.Op), Level: string(o.Level), TeamID: o.TeamID, ProjectID: o.ProjectID,
			Kind: string(o.Kind), Name: o.Name, Files: o.Files, ExpectedPrevVersion: o.ExpectedPrevVersion,
		})
	}
	for _, rv := range cs.Reviews {
		out.Reviews = append(out.Reviews, ReviewJSON{
			ID: rv.ID, ReviewerEmail: rv.ReviewerEmail, Decision: string(rv.Decision), Digest: rv.Digest,
			Superseded: rv.Superseded, Comment: rv.Comment, CreatedAt: rv.CreatedAt,
		})
	}
	return out
}

// ---------------------------------------------------------------
// 发布历史
// ---------------------------------------------------------------

func (s *Server) listReleases(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	q := r.URL.Query()
	scopeKey := ""
	switch {
	case q.Get("project_id") != "":
		if !p.Subject.Can(organizations.PermProjectRead, organizations.ProjectScope(q.Get("project_id"))) {
			httpx.WriteError(w, r, s.log, httpx.ErrNotFound)
			return
		}
		scopeKey = "project:" + q.Get("project_id")
	case q.Get("team_id") != "":
		if !p.Subject.Can(organizations.PermProjectRead, organizations.TeamScope(q.Get("team_id"))) {
			httpx.WriteError(w, r, s.log, httpx.ErrNotFound)
			return
		}
		scopeKey = "team:" + q.Get("team_id")
	case q.Get("level") == "org":
		scopeKey = "org:" + p.OrgID
	default:
		if !p.Subject.Can(organizations.PermAuditRead, organizations.OrgScope(p.OrgID)) {
			httpx.WriteError(w, r, s.log, httpx.ErrAccessDenied)
			return
		}
	}
	limit := parsePage(r).dbLimit()
	list, err := s.Releases.Store().List(r.Context(), p.OrgID, scopeKey, limit)
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	out := make([]ReleaseJSON, 0, len(list))
	for _, rel := range list {
		out = append(out, releaseJSON(rel))
	}
	httpx.WriteJSON(w, http.StatusOK, pageSlice(out, parsePage(r)))
}

func (s *Server) getRelease(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	rel, err := s.Releases.Store().Get(r.Context(), p.OrgID, chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	if !s.canReadRelease(p, rel) {
		httpx.WriteError(w, r, s.log, httpx.ErrNotFound)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, releaseJSON(rel))
}

func (s *Server) canReadRelease(p Principal, rel releases.Release) bool {
	for k := range rel.Revisions {
		level, owner := splitScope(k)
		if p.Subject.Can(organizations.PermProjectRead, organizations.Scope{Level: resources.Level(level), OwnerID: owner}) {
			return true
		}
	}
	return false
}

func (s *Server) rollbackRelease(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var req RollbackReq
	if err := httpx.DecodeLenient(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	cs, err := s.Releases.Rollback(r.Context(), p.csActor(r), chi.URLParam(r, "id"), req.FastTrack)
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	w.Header().Set("ETag", quote(cs.ETag))
	httpx.WriteJSON(w, http.StatusCreated, changesetJSON(cs))
}

func releaseJSON(rel releases.Release) ReleaseJSON {
	if rel.Items == nil {
		rel.Items = []releases.Item{}
	}
	return ReleaseJSON{
		ID: rel.ID, ChangesetID: rel.ChangesetID, PublisherEmail: rel.PublisherEmail, PublishedAt: rel.PublishedAt,
		Items: rel.Items, Revisions: rel.Revisions,
	}
}

// ---------------------------------------------------------------
// 资源读（§1.5）
// ---------------------------------------------------------------

func (s *Server) listResources(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	q := r.URL.Query()
	limit := parsePage(r).dbLimit()
	rows, err := s.Resources.List(r.Context(), p.OrgID, resources.Level(q.Get("level")), q.Get("owner_id"), resources.Kind(q.Get("kind")), q.Get("q"), limit)
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	out := make([]ResourceJSON, 0, len(rows))
	for _, row := range rows {
		if p.Subject.Can(organizations.PermProjectRead, organizations.Scope{Level: row.Level, OwnerID: row.OwnerID}) {
			out = append(out, resourceJSON(row))
		}
	}
	httpx.WriteJSON(w, http.StatusOK, pageSlice(out, parsePage(r)))
}

func (s *Server) getResource(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	row, versions, err := s.Resources.Get(r.Context(), p.OrgID, chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	if !p.Subject.Can(organizations.PermProjectRead, organizations.Scope{Level: row.Level, OwnerID: row.OwnerID}) {
		httpx.WriteError(w, r, s.log, httpx.ErrNotFound)
		return
	}
	body := resourceDetail(row, versions)
	setETag(w, body)
	httpx.WriteJSON(w, http.StatusOK, body)
}

type resourceDetailJSON struct {
	ResourceJSON
	Versions []VersionJSON `json:"versions"`
}

func resourceDetail(row db.ResourceRow, versions []db.VersionRow) resourceDetailJSON {
	vs := make([]VersionJSON, 0, len(versions))
	for _, v := range versions {
		vs = append(vs, versionJSON(v, nil))
	}
	return resourceDetailJSON{resourceJSON(row), vs}
}

func (s *Server) getResourceVersion(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	row, v, err := s.Resources.GetVersion(r.Context(), p.OrgID, chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	if !p.Subject.Can(organizations.PermProjectRead, organizations.Scope{Level: row.Level, OwnerID: row.OwnerID}) {
		httpx.WriteError(w, r, s.log, httpx.ErrNotFound)
		return
	}
	contents := map[string]string{}
	for _, f := range v.Files {
		if f.Size > inlineContentLimit {
			continue
		}
		b, err := s.Resources.ReadBlob(r.Context(), p.OrgID, f.SHA256)
		if err != nil {
			continue
		}
		contents[f.Path] = string(b)
	}
	httpx.WriteJSON(w, http.StatusOK, struct {
		Resource ResourceJSON `json:"resource"`
		VersionJSON
	}{resourceJSON(row), versionJSON(v, contents)})
}

func (s *Server) setResourceTags(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var req TagsReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	row, versions, err := s.Resources.Get(r.Context(), p.OrgID, chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	if !p.Subject.Can(organizations.PermReleasePublish, organizations.Scope{Level: row.Level, OwnerID: row.OwnerID}) {
		httpx.WriteError(w, r, s.log, httpx.ErrAccessDenied)
		return
	}
	if !s.checkIfMatch(w, r, resourceDetail(row, versions)) {
		return
	}
	for _, t := range req.Tags {
		if t == "" || len(t) > 40 || strings.ContainsAny(t, " \t\n,\"'") {
			httpx.WriteError(w, r, s.log, httpx.Validation("标签不能含空白、逗号或引号，且不超过 40 字符"))
			return
		}
	}
	if err := s.Resources.SetTags(r.Context(), p.OrgID, row.ID, req.Tags); err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	s.getResource(w, r)
}

func resourceJSON(row db.ResourceRow) ResourceJSON {
	if row.Tags == nil {
		row.Tags = []string{}
	}
	if row.Groups == nil {
		row.Groups = []string{}
	}
	if row.Files == nil {
		row.Files = []resources.FileRef{}
	}
	return ResourceJSON{
		ID: row.ID, Level: string(row.Level), OwnerID: row.OwnerID, Namespace: row.Namespace, Kind: string(row.Kind), Name: row.Name,
		Description: row.Description, Tags: row.Tags, Groups: row.Groups, Archived: row.Archived, Version: row.Version,
		VersionID: row.VersionID, Checksum: row.Checksum, Deleted: row.Deleted, Files: row.Files, PublishedAt: row.PublishedAt, UpdatedAt: row.UpdatedAt,
	}
}

func versionJSON(v db.VersionRow, contents map[string]string) VersionJSON {
	if v.Files == nil {
		v.Files = []resources.FileRef{}
	}
	return VersionJSON{
		ID: v.ID, Version: v.Version, Files: v.Files, Checksum: v.Checksum, Deleted: v.Deleted, RollbackOfVersion: v.RollbackOfVersion,
		ReleaseID: v.ReleaseID, PublishedBy: v.PublishedBy, PublishedAt: v.PublishedAt, Contents: contents,
	}
}

func splitScope(k string) (string, string) {
	i := strings.IndexByte(k, ':')
	if i < 0 {
		return "", k
	}
	return k[:i], k[i+1:]
}
