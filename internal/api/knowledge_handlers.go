package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ith5/ith5/internal/knowledge"
	"github.com/ith5/ith5/internal/organizations"
	"github.com/ith5/ith5/internal/platform/httpx"
	"github.com/ith5/ith5/internal/resources"
	"github.com/ith5/ith5/internal/telemetry"
)

// ContributeReq 是分享经验（teamai contribute）。
type ContributeReq struct {
	ProjectID  string   `json:"project_id,omitempty"`
	Title      string   `json:"title"`
	Content    string   `json:"content"`
	Tags       []string `json:"tags,omitempty"`
	Supersedes []string `json:"supersedes,omitempty"`
}

// PromoteReq 把 learning 晋升为正式资源。
type PromoteReq struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// LearningJSON 是知识库里的一条。
type LearningJSON struct {
	ID          string     `json:"id"`
	Level       string     `json:"level"`
	OwnerID     string     `json:"owner_id"`
	Namespace   string     `json:"namespace"`
	Name        string     `json:"name"`
	Title       string     `json:"title"`
	Author      string     `json:"author"`
	Tags        []string   `json:"tags"`
	Version     int        `json:"version"`
	Archived    bool       `json:"archived"`
	PublishedAt time.Time  `json:"published_at"`
	Recalled    int        `json:"recalled"`
	Upvoted     int        `json:"upvoted"`
	LastRecall  *time.Time `json:"last_recall,omitempty"`
	Confidence  float64    `json:"confidence"`
	Excerpt     string     `json:"excerpt"`
	Content     string     `json:"content,omitempty"`
}

// ReportsReq 是一批上报。
type ReportsReq struct {
	Events []telemetry.Event `json:"events"`
}

// ---------------------------------------------------------------
// learnings
// ---------------------------------------------------------------

// contribute 分享经验。项目级需要对该项目有 learning:contribute；组织级任何成员都可以。
func (s *Server) contribute(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var req ContributeReq
	if err := httpx.DecodeLenient(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	cs, err := s.Knowledge.Contribute(r.Context(), p.csActor(r), knowledge.ContributeInput{
		ProjectID: req.ProjectID, Title: req.Title, Content: req.Content, Tags: req.Tags, Supersedes: req.Supersedes,
	})
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, changesetJSON(cs))
}

func (s *Server) listLearnings(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	q := r.URL.Query()
	limit := parsePage(r).dbLimit()
	if pid := q.Get("project_id"); pid != "" && pid != "shared" && !p.Subject.Can(organizations.PermProjectRead, organizations.ProjectScope(pid)) {
		httpx.WriteError(w, r, s.log, httpx.ErrNotFound)
		return
	}
	list, err := s.Knowledge.Store().List(r.Context(), p.OrgID, knowledge.ListFilter{ProjectID: q.Get("project_id"), Query: q.Get("q"), Status: q.Get("status"), Limit: limit})
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	out := make([]LearningJSON, 0, len(list))
	for _, l := range list {
		if p.Subject.Can(organizations.PermProjectRead, organizations.Scope{Level: l.Level, OwnerID: l.OwnerID}) {
			out = append(out, learningJSON(l, ""))
		}
	}
	httpx.WriteJSON(w, http.StatusOK, pageSlice(out, parsePage(r)))
}

func (s *Server) getLearning(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	l, err := s.Knowledge.Store().Get(r.Context(), p.OrgID, chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	if !p.Subject.Can(organizations.PermProjectRead, organizations.Scope{Level: l.Level, OwnerID: l.OwnerID}) {
		httpx.WriteError(w, r, s.log, httpx.ErrNotFound)
		return
	}
	content, err := s.Knowledge.Store().Content(r.Context(), p.OrgID, l.ID)
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, learningJSON(l, content))
}

func (s *Server) archiveLearning(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	cs, err := s.Knowledge.Archive(r.Context(), p.csActor(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, changesetJSON(cs))
}

func (s *Server) promoteLearning(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var req PromoteReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	cs, err := s.Knowledge.Promote(r.Context(), p.csActor(r), chi.URLParam(r, "id"), resources.Kind(req.Kind), req.Name)
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	w.Header().Set("ETag", quote(cs.ETag))
	httpx.WriteJSON(w, http.StatusCreated, changesetJSON(cs))
}

func learningJSON(l knowledge.Learning, content string) LearningJSON {
	if l.Tags == nil {
		l.Tags = []string{}
	}
	return LearningJSON{
		ID: l.ID, Level: string(l.Level), OwnerID: l.OwnerID, Namespace: l.Namespace, Name: l.Name, Title: l.Title, Author: l.Author,
		Tags: l.Tags, Version: l.Version, Archived: l.Archived, PublishedAt: l.PublishedAt, Recalled: l.Recalled, Upvoted: l.Upvoted,
		LastRecall: l.LastRecall, Confidence: l.Confidence, Excerpt: l.Excerpt, Content: content,
	}
}

// ---------------------------------------------------------------
// 上报
// ---------------------------------------------------------------

// reportEvents 只接受设备令牌：上报必须归属到一台具体机器，去重账本按机器分。
func (s *Server) reportEvents(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if p.MachineID == "" {
		httpx.WriteError(w, r, s.log, httpx.New(http.StatusForbidden, httpx.CodeAccessDenied, "上报需要设备令牌"))
		return
	}
	var req ReportsReq
	if err := httpx.DecodeLenient(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	ack, err := s.Telemetry.Ingest(r.Context(), telemetry.Context{OrgID: p.OrgID, UserID: p.UserID, MachineID: p.MachineID}, req.Events)
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ack)
}

func (s *Server) digest(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	week := time.Now()
	if v := r.URL.Query().Get("week"); v != "" {
		t, err := time.Parse("2006-01-02", v)
		if err != nil {
			httpx.WriteError(w, r, s.log, httpx.Validation("week 必须是 YYYY-MM-DD"))
			return
		}
		week = t
	}
	d, err := s.Telemetry.Store().Digest(r.Context(), p.OrgID, week)
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

func (s *Server) kbHealth(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	pol, err := s.Orgs.GetPolicy(r.Context(), p.OrgID)
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	h, err := s.Knowledge.Store().Health(r.Context(), p.OrgID, pol)
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h)
}

func (s *Server) usage(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if !p.Subject.Can(organizations.PermAuditRead, organizations.OrgScope(p.OrgID)) {
		httpx.WriteError(w, r, s.log, httpx.ErrAccessDenied)
		return
	}
	q := r.URL.Query()
	to := time.Now().AddDate(0, 0, 1)
	from := to.AddDate(0, 0, -31)
	if v := q.Get("from"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			from = t
		}
	}
	if v := q.Get("to"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			to = t
		}
	}
	rows, err := s.Telemetry.Store().Usage(r.Context(), p.OrgID, q.Get("user_id"), from, to)
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, pageSlice(rows, parsePage(r)))
}

// mapKnowledgeErr 补充知识与上报面的错误映射；由 mapErr 兜底调用。
func mapKnowledgeErr(err error) (error, bool) {
	switch {
	case errors.Is(err, knowledge.ErrNotFound):
		return httpx.ErrNotFound, true
	case errors.Is(err, knowledge.ErrForbidden):
		return httpx.ErrAccessDenied, true
	case errors.Is(err, knowledge.ErrInvalid):
		return httpx.Validation(trimPrefix(err.Error())), true
	case errors.Is(err, telemetry.ErrWindowExpired):
		return httpx.New(http.StatusGone, httpx.CodeEventWindowExpired, "事件早于去重窗口，请对账后重新生成"), true
	case errors.Is(err, telemetry.ErrInvalidEvent):
		return httpx.Validation(trimPrefix(err.Error())), true
	}
	return nil, false
}
