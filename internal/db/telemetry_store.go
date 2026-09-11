package db

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ith5/ith5/internal/platform/pg"
	"github.com/ith5/ith5/internal/telemetry"
)

// TelemetryStore 实现 telemetry.Store。
type TelemetryStore struct{ db *DB }

// Telemetry 返回上报面仓储。
func (d *DB) Telemetry() *TelemetryStore { return &TelemetryStore{db: d} }

var _ telemetry.Store = (*TelemetryStore)(nil)

// Ingest 在一个事务里去重并落表。已在账本里的 event_id 跳过（不报错：客户端重传是正常行为）。
func (s *TelemetryStore) Ingest(ctx context.Context, c telemetry.Context, evs []telemetry.Parsed, now time.Time) (telemetry.Ack, error) {
	ack := telemetry.Ack{Accepted: []string{}}
	err := pg.WithTx(ctx, s.db.pool, func(tx pgx.Tx) error {
		for _, ev := range evs {
			tag, err := tx.Exec(ctx, `
				INSERT INTO report_events (machine_id, event_id, seq, type, received_at) VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (machine_id, event_id) DO NOTHING`, c.MachineID, ev.EventID, ev.Seq, string(ev.Type), now)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				continue // 重复上报
			}
			if err := s.apply(ctx, tx, c, ev); err != nil {
				return err
			}
			ack.Accepted = append(ack.Accepted, ev.EventID)
		}
		return tx.QueryRow(ctx, `SELECT coalesce(max(seq), 0) FROM report_events WHERE machine_id = $1`, c.MachineID).Scan(&ack.MaxSeq)
	})
	return ack, err
}

func (s *TelemetryStore) apply(ctx context.Context, tx pgx.Tx, c telemetry.Context, ev telemetry.Parsed) error {
	switch {
	case ev.Vote != nil:
		// 只对本组织存在的资源计票；不存在的静默忽略，避免用 bundle_id 探测
		_, err := tx.Exec(ctx, `
			INSERT INTO knowledge_votes (org_id, user_id, bundle_id, recalled_count, upvoted_count, last_recalled_at, last_upvoted_at)
			SELECT $1, $2, b.id, $4, $5,
			       CASE WHEN $4 > 0 THEN $6::timestamptz END, CASE WHEN $5 > 0 THEN $6::timestamptz END
			FROM bundles b WHERE b.org_id = $1 AND b.id::text = $3
			ON CONFLICT (user_id, bundle_id) DO UPDATE SET
				recalled_count = knowledge_votes.recalled_count + EXCLUDED.recalled_count,
				upvoted_count = knowledge_votes.upvoted_count + EXCLUDED.upvoted_count,
				last_recalled_at = coalesce(EXCLUDED.last_recalled_at, knowledge_votes.last_recalled_at),
				last_upvoted_at = coalesce(EXCLUDED.last_upvoted_at, knowledge_votes.last_upvoted_at)`,
			c.OrgID, c.UserID, ev.Vote.BundleID, ev.Vote.Recalled, ev.Vote.Upvoted, ev.OccurredAt)
		return err
	case ev.Session != nil:
		iv, _ := json.Marshal(ev.Session.Interventions)
		tk, _ := json.Marshal(ev.Session.Tokens)
		seq := ev.Session.ToolSequence
		if seq == nil {
			seq = []string{}
		}
		var started *time.Time
		if !ev.Session.StartedAt.IsZero() {
			started = &ev.Session.StartedAt
		}
		// 同一 session 的后续摘要替换前一份（Stop 可能来多次），不累加
		_, err := tx.Exec(ctx, `
			INSERT INTO sessions (org_id, user_id, machine_id, session_id, tool, started_at, duration_ms, prompt_turns, tool_total,
			                      tool_sequence, interventions, tokens, valuable, received_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
			ON CONFLICT (user_id, session_id) DO UPDATE SET
				tool = EXCLUDED.tool, started_at = coalesce(EXCLUDED.started_at, sessions.started_at),
				duration_ms = EXCLUDED.duration_ms, prompt_turns = EXCLUDED.prompt_turns, tool_total = EXCLUDED.tool_total,
				tool_sequence = EXCLUDED.tool_sequence, interventions = EXCLUDED.interventions, tokens = EXCLUDED.tokens,
				valuable = EXCLUDED.valuable, received_at = EXCLUDED.received_at`,
			c.OrgID, c.UserID, c.MachineID, ev.Session.SessionID, ev.Session.Tool, started, ev.Session.DurationMs,
			ev.Session.PromptTurns, ev.Session.ToolTotal, seq, iv, tk, ev.Session.Valuable, ev.OccurredAt)
		return err
	case ev.Usage != nil:
		u := ev.Usage
		_, err := tx.Exec(ctx, `
			INSERT INTO usage_daily (org_id, user_id, day, sessions_ended, sessions_succeeded, prompt_turns, duration_ms,
			                         sessions_corrected, priced_requests, cost_micros, cache_read_tokens, cache_eligible_input_tokens, updated_at)
			VALUES ($1, $2, $3::date, $4, $5, $6, $7, $8, $9, $10, $11, $12, now())
			ON CONFLICT (user_id, day) DO UPDATE SET
				sessions_ended = EXCLUDED.sessions_ended, sessions_succeeded = EXCLUDED.sessions_succeeded,
				prompt_turns = EXCLUDED.prompt_turns, duration_ms = EXCLUDED.duration_ms,
				sessions_corrected = EXCLUDED.sessions_corrected, priced_requests = EXCLUDED.priced_requests,
				cost_micros = EXCLUDED.cost_micros, cache_read_tokens = EXCLUDED.cache_read_tokens,
				cache_eligible_input_tokens = EXCLUDED.cache_eligible_input_tokens, updated_at = now()`,
			c.OrgID, c.UserID, u.Day, u.SessionsEnded, u.SessionsSucceeded, u.PromptTurns, u.DurationMs,
			u.SessionsCorrected, u.PricedRequests, u.CostMicros, u.CacheReadTokens, u.CacheEligibleInputTokens)
		return err
	case ev.Skill != nil:
		_, err := tx.Exec(ctx, `
			INSERT INTO skill_usage (org_id, user_id, skill, count, last_used_at) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (user_id, skill) DO UPDATE SET
				count = skill_usage.count + EXCLUDED.count,
				last_used_at = greatest(skill_usage.last_used_at, EXCLUDED.last_used_at)`,
			c.OrgID, c.UserID, ev.Skill.Skill, ev.Skill.Count, ev.Skill.Last)
		return err
	case ev.Tool != nil:
		t := ev.Tool
		summary, _ := json.Marshal(map[string]any{
			"repo": t.Repo, "file_path": t.FilePath, "lines_changed": t.LinesChanged, "exit_code": t.ExitCode,
		})
		_, err := tx.Exec(ctx, `
			INSERT INTO execution_events (id, org_id, user_id, machine_id, session_id, event_type, tool_name, summary, occurred_at)
			VALUES (md5($5 || $4)::uuid, $1, $2, $3, $4, $6::execution_event_type, NULLIF($7, ''), $8, $9)
			ON CONFLICT (id) DO NOTHING`,
			c.OrgID, c.UserID, c.MachineID, ev.EventID, c.MachineID, t.EventType, t.ToolName, summary, ev.OccurredAt)
		return err
	}
	return nil
}

// Digest 聚合某周（周一起）的团队数据，并附上一周的合计做对比。
func (s *TelemetryStore) Digest(ctx context.Context, orgID string, week time.Time) (telemetry.Digest, error) {
	start := weekStart(week)
	end := start.AddDate(0, 0, 7)
	prevStart := start.AddDate(0, 0, -7)
	d := telemetry.Digest{WeekStart: start.Format("2006-01-02"), TopSkills: []telemetry.SkillCount{}, Highlights: []telemetry.Highlight{}}

	cur, err := s.totals(ctx, orgID, start, end)
	if err != nil {
		return d, err
	}
	d.Sessions, d.SessionsSucceeded, d.PromptTurns, d.ActiveMs, d.CostMicros, d.Corrections =
		cur.Sessions, cur.SessionsSucceeded, cur.PromptTurns, cur.ActiveMs, cur.CostMicros, cur.Corrections
	prev, err := s.totals(ctx, orgID, prevStart, start)
	if err != nil {
		return d, err
	}
	d.Previous = &prev

	if err := s.db.pool.QueryRow(ctx, `
		SELECT coalesce(sum(cache_read_tokens), 0), count(DISTINCT user_id) FROM usage_daily
		WHERE org_id = $1 AND day >= $2::date AND day < $3::date`, orgID, start, end).Scan(&d.CacheReadTokens, &d.ActiveMembers); err != nil {
		return d, err
	}

	rows, err := s.db.pool.Query(ctx, `
		SELECT skill, sum(count) FROM skill_usage WHERE org_id = $1 AND last_used_at >= $2 AND last_used_at < $3
		GROUP BY skill ORDER BY 2 DESC LIMIT 10`, orgID, start, end)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var sc telemetry.SkillCount
		if err := rows.Scan(&sc.Skill, &sc.Count); err != nil {
			rows.Close()
			return d, err
		}
		d.TopSkills = append(d.TopSkills, sc)
	}
	rows.Close()

	hl, err := s.db.pool.Query(ctx, `
		SELECT u.email, s.tool, coalesce(s.started_at, s.received_at), s.duration_ms, s.tool_total,
		       coalesce((s.interventions->>'interrupt')::int, 0) + coalesce((s.interventions->>'tool_reject')::int, 0)
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.org_id = $1 AND s.valuable AND s.received_at >= $2 AND s.received_at < $3
		ORDER BY s.tool_total DESC LIMIT 10`, orgID, start, end)
	if err != nil {
		return d, err
	}
	defer hl.Close()
	for hl.Next() {
		var h telemetry.Highlight
		if err := hl.Scan(&h.Email, &h.Tool, &h.StartedAt, &h.DurationMs, &h.ToolTotal, &h.Interventions); err != nil {
			return d, err
		}
		d.Highlights = append(d.Highlights, h)
	}
	return d, hl.Err()
}

func (s *TelemetryStore) totals(ctx context.Context, orgID string, from, to time.Time) (telemetry.DigestTotals, error) {
	var t telemetry.DigestTotals
	err := s.db.pool.QueryRow(ctx, `
		SELECT coalesce(sum(sessions_ended), 0), coalesce(sum(sessions_succeeded), 0), coalesce(sum(prompt_turns), 0),
		       coalesce(sum(duration_ms), 0), coalesce(sum(cost_micros), 0), coalesce(sum(sessions_corrected), 0)
		FROM usage_daily WHERE org_id = $1 AND day >= $2::date AND day < $3::date`, orgID, from, to).
		Scan(&t.Sessions, &t.SessionsSucceeded, &t.PromptTurns, &t.ActiveMs, &t.CostMicros, &t.Corrections)
	return t, err
}

func (s *TelemetryStore) Usage(ctx context.Context, orgID, userID string, from, to time.Time) ([]telemetry.UsageRow, error) {
	rows, err := s.db.pool.Query(ctx, `
		SELECT u.email, to_char(d.day, 'YYYY-MM-DD'), d.sessions_ended, d.sessions_succeeded, d.prompt_turns, d.duration_ms,
		       d.sessions_corrected, d.priced_requests, d.cost_micros, d.cache_read_tokens, d.cache_eligible_input_tokens
		FROM usage_daily d JOIN users u ON u.id = d.user_id
		WHERE d.org_id = $1 AND ($2 = '' OR d.user_id::text = $2) AND d.day >= $3::date AND d.day < $4::date
		ORDER BY d.day DESC, u.email LIMIT 1000`, orgID, userID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []telemetry.UsageRow{}
	for rows.Next() {
		var r telemetry.UsageRow
		if err := rows.Scan(&r.Email, &r.Day, &r.SessionsEnded, &r.SessionsSucceeded, &r.PromptTurns, &r.DurationMs,
			&r.SessionsCorrected, &r.PricedRequests, &r.CostMicros, &r.CacheReadTokens, &r.CacheEligibleInputTokens); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// weekStart 取所在周的周一 00:00 UTC。
func weekStart(t time.Time) time.Time {
	t = t.UTC()
	wd := int(t.Weekday())
	if wd == 0 {
		wd = 7
	}
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	return d.AddDate(0, 0, -(wd - 1))
}
