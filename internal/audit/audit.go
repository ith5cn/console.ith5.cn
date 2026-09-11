// Package audit 记录管理动作：谁在何时对什么做了什么。
//
// 它只记动作与目标 id，不记内容正文、不记提示词。领域服务在写操作成功后调用 Record；
// 写审计失败时领域操作也失败，避免出现「改了但没记」的高权限变更。
package audit

import (
	"context"
	"errors"
	"time"
)

// Event 是一条审计记录。
type Event struct {
	ID          string
	OrgID       string
	ActorUserID string
	// Type 形如 team.create、changeset.publish，点号前是模块。
	Type       string
	TargetType string
	TargetID   string
	Detail     map[string]any
	RequestID  string
	OccurredAt time.Time
	// 以下只在查询结果里填充
	ActorEmail string
}

// Recorder 写入审计。
type Recorder interface {
	Record(ctx context.Context, e Event) error
}

// Query 是后台查询条件。
type Query struct {
	OrgID  string
	Type   string
	Actor  string
	From   time.Time
	To     time.Time
	Limit  int
	Cursor string
}

// Reader 读取审计。
type Reader interface {
	List(ctx context.Context, q Query) (events []Event, nextCursor string, err error)
}

// ErrBadCursor 表示分页游标无法解析或已失效。
var ErrBadCursor = errors.New("audit: 游标无效")

// Store 同时支持读写。
type Store interface {
	Recorder
	Reader
}

// Nop 是不记录的实现，供测试使用。
type Nop struct{}

func (Nop) Record(context.Context, Event) error { return nil }

func (Nop) List(context.Context, Query) ([]Event, string, error) { return nil, "", nil }
