package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ith5/ith5/internal/platform/httpx"
	"github.com/ith5/ith5/internal/sync"
)

// snapshot 返回绑定的完整清单；带 If-None-Match 且未变化时 304。
//
// 只有绑定的主人（持设备令牌）能取快照；后台管理员看清单走 S5 的只读接口。
func (s *Server) snapshot(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	b, err := s.Projects.Store().GetBinding(r.Context(), p.OrgID, chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	if b.UserID != p.UserID || (p.MachineID != "" && b.MachineID != p.MachineID) {
		httpx.WriteError(w, r, s.log, httpx.ErrNotFound)
		return
	}
	snap, err := s.Sync.Snapshot(r.Context(), b)
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	w.Header().Set("ETag", quote(snap.Revision))
	w.Header().Set("Cache-Control", "no-cache")
	if unquote(r.Header.Get("If-None-Match")) == snap.Revision {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, snap)
}

// blob 返回内容字节。无权与不存在都是 404，不区分。
func (s *Server) blob(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	sha := chi.URLParam(r, "sha256")
	if !strings.HasPrefix(sha, "sha256:") || len(sha) != len("sha256:")+64 {
		httpx.WriteError(w, r, s.log, httpx.ErrNotFound)
		return
	}
	b, err := s.Sync.Blob(r.Context(), p.OrgID, p.UserID, sha)
	if errors.Is(err, sync.ErrBlobNotVisible) {
		httpx.WriteError(w, r, s.log, httpx.ErrNotFound)
		return
	}
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	// 内容寻址：同一哈希永远是同一份字节，可以无限期缓存
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("ETag", quote(sha))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

// SyncResultsReq 是客户端物化后的回执。
type SyncResultsReq struct {
	AppliedRevision string        `json:"applied_revision"`
	Results         []sync.Result `json:"results"`
}

// syncResults 记录回执并推进 applied_revision。
func (s *Server) syncResults(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var req SyncResultsReq
	if err := httpx.DecodeLenient(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	if len(req.Results) > 5000 {
		httpx.WriteError(w, r, s.log, httpx.New(http.StatusRequestEntityTooLarge, httpx.CodePayloadTooLarge, "回执条目过多"))
		return
	}
	b, err := s.Projects.Store().GetBinding(r.Context(), p.OrgID, chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	if b.UserID != p.UserID {
		httpx.WriteError(w, r, s.log, httpx.ErrNotFound)
		return
	}
	if err := s.Sync.Report(r.Context(), b, req.AppliedRevision, httpx.ClientIP(r), req.Results); err != nil {
		httpx.WriteError(w, r, s.log, httpx.Validation(err.Error()))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"accepted": len(req.Results)})
}
