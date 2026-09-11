// Package db 是仓储层：把领域包声明的 Store 接口落到 PostgreSQL。
//
// 所有业务查询都必须显式带 org_id，禁止仅凭 UUID 查询。
// 跨模块的表不在这里互相 JOIN 业务语义，只做各自模块的读写。
package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ith5/ith5/internal/platform/pg"
)

// DB 持有连接池；各模块的 Store 实现都是它的方法集。
type DB struct {
	pool *pgxpool.Pool
}

// Open 建立连接池。
func Open(ctx context.Context, dsn string) (*DB, error) {
	pool, err := pg.Open(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &DB{pool: pool}, nil
}

// New 用已有连接池构造，供测试复用。
func New(pool *pgxpool.Pool) *DB { return &DB{pool: pool} }

// Close 关闭连接池。
func (d *DB) Close() { d.pool.Close() }

// Pool 暴露连接池，只给健康检查与测试用。
func (d *DB) Pool() *pgxpool.Pool { return d.pool }

// ErrNotFound 表示记录不存在。各模块的 Store 把它翻译成自己的哨兵错误。
var ErrNotFound = errors.New("db: 记录不存在")

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
