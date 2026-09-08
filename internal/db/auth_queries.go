package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------
// 设备码（技术方案 §6.1）
// ---------------------------------------------------------------

func (d *DB) CreateDeviceCode(ctx context.Context, codeHash, userCode, fingerprint, hostname, os string, expiresAt time.Time) error {
	_, err := d.pool.Exec(ctx, `
		INSERT INTO device_codes (device_code_hash, user_code, fingerprint, hostname, os, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6)`, codeHash, userCode, fingerprint, hostname, os, expiresAt)
	return err
}

type DeviceCode struct {
	ID          string
	Status      string
	UserID      string
	MachineID   string
	Fingerprint string
	Hostname    string
	OS          string
	ExpiresAt   time.Time
}

func (d *DB) GetDeviceCodeByUserCode(ctx context.Context, userCode string) (DeviceCode, error) {
	var c DeviceCode
	err := d.pool.QueryRow(ctx, `
		SELECT id::text, status::text, coalesce(user_id::text,''), coalesce(machine_id::text,''),
		       fingerprint, hostname, os, expires_at
		FROM device_codes WHERE user_code = $1`, userCode).
		Scan(&c.ID, &c.Status, &c.UserID, &c.MachineID, &c.Fingerprint, &c.Hostname, &c.OS, &c.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// ApproveDeviceCode 以 compare-and-set 批准设备码：只有仍处于 pending
// 且未过期的码才会被改写，避免重放与并发批准（技术方案 §6.2）。
func (d *DB) ApproveDeviceCode(ctx context.Context, userCode, userID, machineID string) error {
	tag, err := d.pool.Exec(ctx, `
		UPDATE device_codes SET status = 'approved', user_id = $2, machine_id = $3
		WHERE user_code = $1 AND status = 'pending' AND expires_at > now()`,
		userCode, userID, machineID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ConsumeDeviceCode 在单个事务中把 approved 的码改为 consumed 并返回归属。
// 同一个码只可能被成功消费一次。
func (d *DB) ConsumeDeviceCode(ctx context.Context, codeHash string) (userID, machineID string, err error) {
	err = d.pool.QueryRow(ctx, `
		UPDATE device_codes SET status = 'consumed'
		WHERE device_code_hash = $1 AND status = 'approved' AND expires_at > now()
		RETURNING user_id::text, machine_id::text`, codeHash).Scan(&userID, &machineID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return userID, machineID, err
}

// DeviceCodeStatus 供轮询判断是 pending 还是已过期。
func (d *DB) DeviceCodeStatus(ctx context.Context, codeHash string) (status string, expired bool, err error) {
	var exp time.Time
	err = d.pool.QueryRow(ctx,
		`SELECT status::text, expires_at FROM device_codes WHERE device_code_hash = $1`, codeHash).
		Scan(&status, &exp)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, ErrNotFound
	}
	return status, time.Now().After(exp), err
}

// ---------------------------------------------------------------
// 刷新令牌
// ---------------------------------------------------------------

func (d *DB) CreateRefreshToken(ctx context.Context, tokenHash, userID, machineID string, expiresAt time.Time) error {
	_, err := d.pool.Exec(ctx, `
		INSERT INTO refresh_tokens (token_hash, user_id, machine_id, expires_at)
		VALUES ($1,$2,$3,$4)`, tokenHash, userID, machineID, expiresAt)
	return err
}

// UseRefreshToken 校验刷新令牌并返回归属。
// 已吊销、已过期，或所属用户已停用的，一律拒绝（D4）。
func (d *DB) UseRefreshToken(ctx context.Context, tokenHash string) (userID, machineID string, err error) {
	var status string
	err = d.pool.QueryRow(ctx, `
		SELECT r.user_id::text, r.machine_id::text, u.status::text
		FROM refresh_tokens r JOIN users u ON u.id = r.user_id
		WHERE r.token_hash = $1 AND r.revoked_at IS NULL AND r.expires_at > now()`,
		tokenHash).Scan(&userID, &machineID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	if status == "suspended" {
		return "", "", ErrSuspended
	}
	return userID, machineID, nil
}

// RevokeMachineTokens 吊销某台设备的全部刷新令牌（logout）。
func (d *DB) RevokeMachineTokens(ctx context.Context, userID, machineID string) error {
	_, err := d.pool.Exec(ctx, `
		UPDATE refresh_tokens SET revoked_at = now()
		WHERE user_id = $1 AND machine_id = $2 AND revoked_at IS NULL`, userID, machineID)
	return err
}

// ---------------------------------------------------------------
// 分发回执（技术方案 §9）
// ---------------------------------------------------------------

type DistributionEvent struct {
	EventID    string
	BundleID   string
	Version    int
	Action     string
	Detail     []byte
	OccurredAt time.Time
}

// InsertDistributionEvents 批量写入回执。
//
// 身份字段一律由服务端从令牌补齐，**不接受客户端提交**。
// event_id 唯一，重复回执 ON CONFLICT DO NOTHING（技术方案 §9）。
func (d *DB) InsertDistributionEvents(ctx context.Context, orgID, userID, machineID, ip string, evs []DistributionEvent) (int, error) {
	if len(evs) == 0 {
		return 0, nil
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	accepted := 0
	for _, e := range evs {
		var ipArg any
		if ip != "" {
			ipArg = ip
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO distribution_logs
			    (event_id, org_id, user_id, machine_id, bundle_id, version, action, detail, ip, occurred_at)
			SELECT $1, $2, $3, $4, b.id, $6, $7, $8, $9, $10
			FROM bundles b WHERE b.id = $5 AND b.org_id = $2
			ON CONFLICT (event_id) DO NOTHING`,
			e.EventID, orgID, userID, machineID, e.BundleID, e.Version, e.Action, e.Detail, ipArg, e.OccurredAt)
		if err != nil {
			return 0, err
		}
		accepted += int(tag.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return accepted, nil
}

// ExecutionEvent 是一条 L0 执行事件。
type ExecutionEvent struct {
	ID         string
	SessionID  string
	EventType  string
	ToolName   string
	Summary    []byte
	OccurredAt time.Time
}

// InsertExecutionEvents 批量写入 L0 事件。
//
// id 由客户端生成，天然幂等：断网重传、批次重复上传都不会产生重复行。
func (d *DB) InsertExecutionEvents(ctx context.Context, orgID, userID, machineID string, evs []ExecutionEvent) (int, error) {
	if len(evs) == 0 {
		return 0, nil
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	n := 0
	for _, e := range evs {
		tag, err := tx.Exec(ctx, `
			INSERT INTO execution_events
			  (id, org_id, user_id, machine_id, session_id, event_type, tool_name, summary, occurred_at)
			VALUES ($1,$2,$3,$4,$5,$6::execution_event_type,$7,$8,$9)
			ON CONFLICT (id) DO NOTHING`,
			e.ID, orgID, userID, machineID, e.SessionID, e.EventType,
			nullIfEmpty(e.ToolName), e.Summary, e.OccurredAt)
		if err != nil {
			return 0, err
		}
		n += int(tag.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return n, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ExecutionRow 是后台展示用的一行。
type ExecutionRow struct {
	Email      string
	Hostname   string
	SessionID  string
	EventType  string
	ToolName   string
	Summary    []byte
	OccurredAt time.Time
}

func (d *DB) ListExecutionEvents(ctx context.Context, orgID, userID, tool string, limit int) ([]ExecutionRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := d.pool.Query(ctx, `
		SELECT u.email, coalesce(m.hostname,''), e.session_id, e.event_type::text,
		       coalesce(e.tool_name,''), e.summary, e.occurred_at
		FROM execution_events e
		JOIN users u ON u.id = e.user_id
		LEFT JOIN machines m ON m.id = e.machine_id
		WHERE e.org_id = $1
		  AND ($2 = '' OR e.user_id::text = $2)
		  AND ($3 = '' OR e.tool_name = $3)
		ORDER BY e.occurred_at DESC LIMIT $4`, orgID, userID, tool, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExecutionRow
	for rows.Next() {
		var r ExecutionRow
		if err := rows.Scan(&r.Email, &r.Hostname, &r.SessionID, &r.EventType,
			&r.ToolName, &r.Summary, &r.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
