// Package watch 把本机 trace 流还原成「工作流现在走到哪」的实时视图，
// 并通过一个只监听 127.0.0.1 的小服务把它推给浏览器看板。
//
// 它刻意不依赖 ith5-server：那是带 Postgres 与 JWT 的企业后端，
// 而看板是开发者看自己本机的东西，不该需要登录、更不该联网。
package watch

import (
	"sort"
	"sync"
	"time"

	"github.com/ith5/ith5/internal/hook"
)

// Span 是一次 agent / skill 调用的区间。EndedAt 为零值表示仍在运行。
type Span struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	Detail    string    `json:"detail,omitempty"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitzero"`
	Running   bool      `json:"running"`
	// DurationMS 在运行中时表示「至今已耗时」，由 Snapshot 现算。
	DurationMS int64 `json:"duration_ms"`
}

// Session 是一次 Claude Code 会话。
type Session struct {
	ID        string    `json:"id"`
	CWD       string    `json:"cwd,omitempty"`
	StartedAt time.Time `json:"started_at"`
	LastAt    time.Time `json:"last_at"`
	Spans     []*Span   `json:"spans"`

	// byToolUse 让 end 事件 O(1) 找到自己的 start。
	byToolUse map[string]*Span
}

// State 是全部会话的内存视图。可并发读写。
type State struct {
	mu       sync.Mutex
	sessions map[string]*Session
	now      func() time.Time
}

func NewState() *State {
	return &State{sessions: map[string]*Session{}, now: time.Now}
}

// Apply 吃进一条 trace，返回该事件是否改变了视图。
func (s *State) Apply(tr hook.Trace) bool {
	if tr.SessionID == "" {
		return false
	}
	at, err := time.Parse(time.RFC3339Nano, tr.OccurredAt)
	if err != nil {
		at = s.now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sess := s.sessions[tr.SessionID]
	if sess == nil {
		sess = &Session{ID: tr.SessionID, StartedAt: at, byToolUse: map[string]*Span{}}
		s.sessions[tr.SessionID] = sess
	}
	if tr.CWD != "" {
		sess.CWD = tr.CWD
	}
	if at.After(sess.LastAt) {
		sess.LastAt = at
	}

	switch {
	case tr.Kind == "session":
		sess.StartedAt = at
		return true

	case tr.Stage == "start":
		sp := &Span{
			ID:        spanKey(tr),
			Kind:      tr.Kind,
			Name:      tr.Name,
			Detail:    tr.Detail,
			StartedAt: at,
			Running:   true,
		}
		sess.Spans = append(sess.Spans, sp)
		if tr.ToolUseID != "" {
			sess.byToolUse[tr.ToolUseID] = sp
		}
		return true

	case tr.Stage == "end":
		sp := sess.matchEnd(tr)
		if sp == nil {
			// end 先于 start 到达（或 start 丢了）：补一条零长区间，
			// 至少让看板显示「这件事发生过」，而不是静默吞掉。
			sp = &Span{ID: spanKey(tr), Kind: tr.Kind, Name: tr.Name, Detail: tr.Detail, StartedAt: at}
			sess.Spans = append(sess.Spans, sp)
		}
		sp.Running = false
		sp.EndedAt = at
		if sp.Name == "" {
			sp.Name = tr.Name
		}
		return true
	}
	return false
}

// matchEnd 找到这条 end 对应的 start。
//
// 优先按 tool_use_id 精确配对；SubagentStop 不带 tool_use_id，
// 退化为「关掉最早的那个还在跑的同类区间」—— 并行派 agent 时这不保证
// 配对正确，但保证不会留下永远转圈的条目，后者对看板更致命。
func (sess *Session) matchEnd(tr hook.Trace) *Span {
	if tr.ToolUseID != "" {
		if sp := sess.byToolUse[tr.ToolUseID]; sp != nil {
			delete(sess.byToolUse, tr.ToolUseID)
			return sp
		}
		return nil
	}
	for _, sp := range sess.Spans {
		if sp.Running && sp.Kind == tr.Kind {
			return sp
		}
	}
	return nil
}

func spanKey(tr hook.Trace) string {
	if tr.ToolUseID != "" {
		return tr.ToolUseID
	}
	return tr.ID
}

// Snapshot 是推给前端的整体视图，按最近活动排序。
type Snapshot struct {
	Sessions []SessionView `json:"sessions"`
	At       time.Time     `json:"at"`
}

type SessionView struct {
	Session
	// Running 是当前正在跑的区间，前端据此显示「现在在做什么」。
	Running []*Span `json:"running"`
}

func (s *State) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	out := make([]SessionView, 0, len(s.sessions))
	for _, sess := range s.sessions {
		view := SessionView{Session: *sess}
		// 两个切片都预置成非 nil：nil 切片会序列化成 null，
		// 前端就得到处写 `|| []`，而这只是个序列化细节。
		view.Spans = make([]*Span, 0, len(sess.Spans))
		view.Running = []*Span{}
		for _, sp := range sess.Spans {
			cp := *sp
			end := cp.EndedAt
			if cp.Running {
				end = now
			}
			cp.DurationMS = end.Sub(cp.StartedAt).Milliseconds()
			view.Spans = append(view.Spans, &cp)
			if cp.Running {
				view.Running = append(view.Running, &cp)
			}
		}
		out = append(out, view)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastAt.After(out[j].LastAt) })
	return Snapshot{Sessions: out, At: now}
}
