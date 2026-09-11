// Package pg 负责 PostgreSQL 连接池、迁移与事务边界。
//
// 领域包不直接依赖 pgx 的连接池类型，只通过 db 包的仓储访问数据。
package pg

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	migrations "github.com/ith5/ith5/db"
)

// Open 建立连接池并探活。
func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("解析 DSN: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MaxConnLifetime = time.Hour

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("建立连接池: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("连接数据库: %w", err)
	}
	return pool, nil
}

// Migrate 应用所有未执行的迁移。迁移脚本已编译进二进制。
func Migrate(ctx context.Context, dsn string) error {
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("打开迁移连接: %w", err)
	}
	defer sqlDB.Close()

	goose.SetBaseFS(migrations.Migrations)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	if err := goose.UpContext(ctx, sqlDB, "migrations"); err != nil {
		return fmt.Errorf("执行迁移: %w", err)
	}
	return nil
}

// Reset 回滚全部迁移再重新应用。只给测试与本地开发用。
func Reset(ctx context.Context, dsn string) error {
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("打开迁移连接: %w", err)
	}
	defer sqlDB.Close()

	goose.SetBaseFS(migrations.Migrations)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	if err := goose.DownToContext(ctx, sqlDB, "migrations", 0); err != nil {
		return fmt.Errorf("回滚迁移: %w", err)
	}
	if err := goose.UpContext(ctx, sqlDB, "migrations"); err != nil {
		return fmt.Errorf("执行迁移: %w", err)
	}
	return nil
}

// WithTx 在一个事务内执行 fn；fn 返回错误则回滚，否则提交。
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // 提交成功后的 Rollback 是空操作
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

var _ = stdlib.GetDefaultDriver // 确保 pgx 的 database/sql 驱动被注册
