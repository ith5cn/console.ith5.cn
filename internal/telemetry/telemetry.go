// Package telemetry 是上报面：接收客户端的计数类事件，去重后落表，聚合成周报与用量。
//
// 隐私边界（docs/设计-知识与上报面.md §4）在这里强制：事件先解析成只含计数与工具名的类型，
// 提示词、输出片段、transcript 路径这类字段根本没有落点，无论客户端发不发都进不了库。
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// EventType 是事件类型。
type EventType string

const (
	TypeVoteDelta      EventType = "vote_delta"
	TypeSessionSummary EventType = "session_summary"
	TypeUsageDaily     EventType = "usage_daily"
	TypeSkillUsage     EventType = "skill_usage"
	TypeToolUse        EventType = "tool_use"
)

// Event 是一条上报。Payload 按 Type 解析。
type Event struct {
	EventID    string         `json:"event_id"`
	Seq        int64          `json:"seq"`
	Type       EventType      `json:"type"`
	OccurredAt time.Time      `json:"occurred_at"`
	Payload    map[string]any `json:"payload"`
}

// VoteDelta 是对某篇知识的召回 / 点赞增量。
type VoteDelta struct {
	BundleID string
	Recalled int
	Upvoted  int
}

// SessionSummary 是一次会话的计数摘要。没有任何自然语言字段。
type SessionSummary struct {
	SessionID     string
	Tool          string
	StartedAt     time.Time
	DurationMs    int64
	PromptTurns   int
	ToolTotal     int
	ToolSequence  []string
	Interventions map[string]int
	Tokens        map[string]int64
	Valuable      bool
}

// UsageDaily 是一天的用量快照（客户端按天累计后整体上报，服务端 upsert）。
type UsageDaily struct {
	Day                      string
	SessionsEnded            int
	SessionsSucceeded        int
	PromptTurns              int
	DurationMs               int64
	SessionsCorrected        int
	PricedRequests           int
	CostMicros               int64
	CacheReadTokens          int64
	CacheEligibleInputTokens int64
}

// SkillUsage 是某个 skill 的使用次数增量。
type SkillUsage struct {
	Skill string
	Count int
	Last  time.Time
}

// ToolUse 是工具调用审计（原 execution_events），严格白名单字段。
type ToolUse struct {
	SessionID    string
	EventType    string // session_start | tool_use
	ToolName     string
	Repo         string
	FilePath     string
	LinesChanged int
	ExitCode     *int
}

// Context 是上报者的身份。
type Context struct {
	OrgID     string
	UserID    string
	MachineID string
}

// Ack 是入库结果。
type Ack struct {
	Accepted []string `json:"accepted"`
	MaxSeq   int64    `json:"max_seq"`
}

var (
	ErrInvalidEvent  = errors.New("telemetry: 事件非法")
	ErrWindowExpired = errors.New("telemetry: 事件窗口已过期")
)

// DedupeWindow 是去重账本保留期；超期的 event_id 返回 ErrWindowExpired。
const DedupeWindow = 30 * 24 * time.Hour

// Store 是上报面的持久化能力。Ingest 必须在一个事务内完成去重与落表。
type Store interface {
	// Ingest 写入一批事件。已在账本中的 event_id 跳过；返回被接受的 id 与该设备最大 seq。
	Ingest(ctx context.Context, c Context, evs []Parsed, now time.Time) (Ack, error)
	Digest(ctx context.Context, orgID string, week time.Time) (Digest, error)
	Usage(ctx context.Context, orgID, userID string, from, to time.Time) ([]UsageRow, error)
}

// Parsed 是解析并脱敏后的事件。
type Parsed struct {
	EventID    string
	Seq        int64
	Type       EventType
	OccurredAt time.Time
	Vote       *VoteDelta
	Session    *SessionSummary
	Usage      *UsageDaily
	Skill      *SkillUsage
	Tool       *ToolUse
}

// Digest 是团队周报。
type Digest struct {
	WeekStart         string        `json:"week_start"`
	Sessions          int           `json:"sessions"`
	SessionsSucceeded int           `json:"sessions_succeeded"`
	PromptTurns       int           `json:"prompt_turns"`
	ActiveMs          int64         `json:"active_ms"`
	CostMicros        int64         `json:"cost_micros"`
	CacheReadTokens   int64         `json:"cache_read_tokens"`
	Corrections       int           `json:"corrections"`
	ActiveMembers     int           `json:"active_members"`
	Previous          *DigestTotals `json:"previous,omitempty"`
	TopSkills         []SkillCount  `json:"top_skills"`
	Highlights        []Highlight   `json:"highlights"`
}

// DigestTotals 是上一周期的同口径合计，用于对比。
type DigestTotals struct {
	Sessions          int   `json:"sessions"`
	SessionsSucceeded int   `json:"sessions_succeeded"`
	PromptTurns       int   `json:"prompt_turns"`
	ActiveMs          int64 `json:"active_ms"`
	CostMicros        int64 `json:"cost_micros"`
	Corrections       int   `json:"corrections"`
}

// SkillCount 是 skill 使用排行的一行。
type SkillCount struct {
	Skill string `json:"skill"`
	Count int    `json:"count"`
}

// Highlight 是「有价值的会话」：只有计数，没有内容。
type Highlight struct {
	Email         string    `json:"email"`
	Tool          string    `json:"tool"`
	StartedAt     time.Time `json:"started_at"`
	DurationMs    int64     `json:"duration_ms"`
	ToolTotal     int       `json:"tool_total"`
	Interventions int       `json:"interventions"`
}

// UsageRow 是某人某天的用量。
type UsageRow struct {
	Email string `json:"email"`
	UsageDaily
}

// Service 解析并入库。
type Service struct {
	store Store
	now   func() time.Time
}

// NewService 构造。
func NewService(store Store) *Service { return &Service{store: store, now: time.Now} }

// Store 暴露仓储。
func (s *Service) Store() Store { return s.store }

// Ingest 解析一批事件并入库。任何一条解析失败整批拒绝，让客户端修好再发，避免半批次的对账噩梦。
func (s *Service) Ingest(ctx context.Context, c Context, evs []Event) (Ack, error) {
	if len(evs) == 0 {
		return Ack{Accepted: []string{}}, nil
	}
	if len(evs) > 1000 {
		return Ack{}, fmt.Errorf("%w: 一批最多 1000 条", ErrInvalidEvent)
	}
	now := s.now()
	parsed := make([]Parsed, 0, len(evs))
	for i, ev := range evs {
		p, err := Parse(ev)
		if err != nil {
			return Ack{}, fmt.Errorf("%w: 第 %d 条: %v", ErrInvalidEvent, i, err)
		}
		if now.Sub(p.OccurredAt) > DedupeWindow {
			return Ack{}, fmt.Errorf("%w: 第 %d 条", ErrWindowExpired, i)
		}
		parsed = append(parsed, p)
	}
	return s.store.Ingest(ctx, c, parsed, now)
}

// Parse 把一条上报解析成只含允许字段的结构。未知字段被丢弃，这就是禁运清单的实现方式。
func Parse(ev Event) (Parsed, error) {
	if ev.EventID == "" || len(ev.EventID) > 128 {
		return Parsed{}, errors.New("event_id 必填且不超过 128 字符")
	}
	if ev.OccurredAt.IsZero() {
		return Parsed{}, errors.New("occurred_at 必填")
	}
	p := Parsed{EventID: ev.EventID, Seq: ev.Seq, Type: ev.Type, OccurredAt: ev.OccurredAt}
	m := ev.Payload
	switch ev.Type {
	case TypeVoteDelta:
		v := &VoteDelta{BundleID: str(m, "bundle_id"), Recalled: num(m, "recalled_delta"), Upvoted: num(m, "upvoted_delta")}
		if v.BundleID == "" {
			return Parsed{}, errors.New("vote_delta 需要 bundle_id")
		}
		if v.Recalled < 0 || v.Upvoted < 0 || v.Recalled > 10000 || v.Upvoted > 10000 {
			return Parsed{}, errors.New("vote_delta 增量非法")
		}
		p.Vote = v
	case TypeSessionSummary:
		ss := &SessionSummary{
			SessionID: str(m, "session_id"), Tool: str(m, "tool"), DurationMs: num64(m, "duration_ms"),
			PromptTurns: num(m, "prompt_turns"), ToolTotal: num(m, "tool_total"), Valuable: boolean(m, "valuable"),
			Interventions: map[string]int{}, Tokens: map[string]int64{},
		}
		if ss.SessionID == "" {
			return Parsed{}, errors.New("session_summary 需要 session_id")
		}
		if t, ok := m["started_at"].(string); ok {
			ss.StartedAt, _ = time.Parse(time.RFC3339, t)
		}
		if seq, ok := m["tool_sequence"].([]any); ok {
			for _, x := range seq {
				if s, ok := x.(string); ok && len(ss.ToolSequence) < 500 {
					ss.ToolSequence = append(ss.ToolSequence, s)
				}
			}
		}
		for _, k := range []string{"interrupt", "tool_reject", "tool_error"} {
			if iv, ok := m["interventions"].(map[string]any); ok {
				ss.Interventions[k] = num(iv, k)
			}
		}
		for _, k := range []string{"input", "output", "cache_read", "cache_write"} {
			if tk, ok := m["tokens"].(map[string]any); ok {
				ss.Tokens[k] = num64(tk, k)
			}
		}
		p.Session = ss
	case TypeUsageDaily:
		u := &UsageDaily{
			Day: str(m, "day"), SessionsEnded: num(m, "sessions_ended"), SessionsSucceeded: num(m, "sessions_succeeded"),
			PromptTurns: num(m, "prompt_turns"), DurationMs: num64(m, "duration_ms"), SessionsCorrected: num(m, "sessions_corrected"),
			PricedRequests: num(m, "priced_requests"), CostMicros: num64(m, "cost_micros"),
			CacheReadTokens: num64(m, "cache_read_tokens"), CacheEligibleInputTokens: num64(m, "cache_eligible_input_tokens"),
		}
		if _, err := time.Parse("2006-01-02", u.Day); err != nil {
			return Parsed{}, errors.New("usage_daily 的 day 必须是 YYYY-MM-DD")
		}
		p.Usage = u
	case TypeSkillUsage:
		su := &SkillUsage{Skill: str(m, "skill"), Count: num(m, "count"), Last: ev.OccurredAt}
		if su.Skill == "" || su.Count <= 0 || su.Count > 10000 {
			return Parsed{}, errors.New("skill_usage 需要 skill 与正数 count")
		}
		p.Skill = su
	case TypeToolUse:
		tu := &ToolUse{SessionID: str(m, "session_id"), EventType: str(m, "event_type"), ToolName: str(m, "tool_name"),
			Repo: str(m, "repo"), FilePath: str(m, "file_path"), LinesChanged: num(m, "lines_changed")}
		if tu.SessionID == "" || (tu.EventType != "session_start" && tu.EventType != "tool_use") {
			return Parsed{}, errors.New("tool_use 需要 session_id 与合法 event_type")
		}
		if ec, ok := m["exit_code"].(float64); ok {
			n := int(ec)
			tu.ExitCode = &n
		}
		p.Tool = tu
	default:
		return Parsed{}, fmt.Errorf("未知类型 %q", ev.Type)
	}
	return p, nil
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	if len(s) > 512 {
		s = s[:512]
	}
	return s
}

func num(m map[string]any, k string) int {
	f, _ := m[k].(float64)
	return int(f)
}

func num64(m map[string]any, k string) int64 {
	f, _ := m[k].(float64)
	return int64(f)
}

func boolean(m map[string]any, k string) bool {
	b, _ := m[k].(bool)
	return b
}
