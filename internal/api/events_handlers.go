package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/ith5/ith5/internal/db"
)

var validEventTypes = map[string]bool{"session_start": true, "tool_use": true}

// executionEvents 接收 L0 执行事件。
//
// **服务端用严格 schema 重建 summary，未知字段一律丢弃**（技术方案 §10.4）。
// 客户端已经按白名单过滤过一次，这里再过一次——两道闸都得有，
// 因为改客户端比改服务端容易得多。
func (s *Server) executionEvents(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	if p.MachineID == "" {
		s.fail(w, r, http.StatusForbidden, "forbidden", "该令牌未绑定设备")
		return
	}
	var req ExecutionEventsReq
	if !s.decodeLenient(w, r, &req) {
		return
	}
	if len(req.Events) == 0 {
		s.writeJSON(w, http.StatusOK, AcceptedResp{Accepted: 0})
		return
	}
	if len(req.Events) > maxEventsPerBatch {
		s.fail(w, r, http.StatusBadRequest, "too_many", "单次最多 200 条")
		return
	}

	out := make([]db.ExecutionEvent, 0, len(req.Events))
	for _, e := range req.Events {
		if _, err := uuid.Parse(e.ID); err != nil {
			s.fail(w, r, http.StatusBadRequest, "bad_request", "id 必须是 UUID")
			return
		}
		if !validEventTypes[e.EventType] {
			s.fail(w, r, http.StatusBadRequest, "bad_request", "event_type 取值非法")
			return
		}
		// 重建 summary：只保留白名单字段，客户端多送的一律不落库
		clean := EventSummary{
			Repo:         truncate(e.Summary.Repo, 200),
			FilePath:     truncate(e.Summary.FilePath, 400),
			BashCommand:  truncate(e.Summary.BashCommand, 64),
			LinesChanged: e.Summary.LinesChanged,
			ExitCode:     e.Summary.ExitCode,
		}
		blob, err := json.Marshal(clean)
		if err != nil {
			s.fail(w, r, http.StatusBadRequest, "bad_request", "summary 格式非法")
			return
		}
		occurred := e.OccurredAt
		if occurred.IsZero() {
			occurred = time.Now()
		}
		out = append(out, db.ExecutionEvent{
			ID: e.ID, SessionID: truncate(e.SessionID, 128), EventType: e.EventType,
			ToolName: truncate(e.ToolName, 64), Summary: blob, OccurredAt: occurred,
		})
	}

	n, err := s.db.InsertExecutionEvents(r.Context(), p.OrgID, p.UserID, p.MachineID, out)
	if err != nil {
		s.internal(w, r, err, "写入执行事件")
		return
	}
	s.writeJSON(w, http.StatusOK, AcceptedResp{Accepted: n})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// adminExecutions 是后台的执行记录视图。
func (s *Server) adminExecutions(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	rows, err := s.db.ListExecutionEvents(r.Context(), p.OrgID,
		r.URL.Query().Get("user_id"), r.URL.Query().Get("tool"), 200)
	if err != nil {
		s.internal(w, r, err, "读取执行记录")
		return
	}
	out := make([]ExecutionEntry, 0, len(rows))
	for _, e := range rows {
		var sum EventSummary
		_ = json.Unmarshal(e.Summary, &sum)
		out = append(out, ExecutionEntry{
			Email: e.Email, Hostname: e.Hostname, SessionID: e.SessionID,
			EventType: e.EventType, ToolName: e.ToolName,
			Summary: sum, OccurredAt: e.OccurredAt,
		})
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}
