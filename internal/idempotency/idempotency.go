// Package idempotency 定义写请求幂等记录的模型与仓储接口。
//
// HTTP 中间件在 internal/api，PostgreSQL 实现在 internal/db；
// 这里只放两边共用的类型，避免仓储层反向依赖 HTTP 层。
package idempotency

import (
	"context"
	"time"
)

// Record 是一次带 Idempotency-Key 的写请求留下的记录。
//
// Status 为 0 表示请求还在处理中（首个请求尚未返回，或处理途中崩溃）。
type Record struct {
	Method     string
	Route      string
	BodySHA256 string
	Status     int
	Response   []byte
}

// Store 记录 (org, user, key) 维度的写请求结果。
//
// Begin 尝试登记一次新请求：登记成功返回 created=true；键已存在则返回已有记录。
// Complete 在 handler 返回后写回状态码与响应体。
type Store interface {
	Begin(ctx context.Context, orgID, userID, key string, rec Record, ttl time.Duration) (existing *Record, created bool, err error)
	Complete(ctx context.Context, orgID, userID, key string, status int, response []byte) error
}

// TTL 是结果缓存时长；客户端重试窗口远小于它。
const TTL = 24 * time.Hour
