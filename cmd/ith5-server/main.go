// ith5-server 是唯一的服务端二进制：自带迁移、自带管理后台，只依赖 PostgreSQL。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ith5/ith5/internal/api"
	"github.com/ith5/ith5/internal/changesets"
	"github.com/ith5/ith5/internal/db"
	"github.com/ith5/ith5/internal/identity"
	"github.com/ith5/ith5/internal/knowledge"
	"github.com/ith5/ith5/internal/maintenance"
	"github.com/ith5/ith5/internal/organizations"
	"github.com/ith5/ith5/internal/platform/config"
	"github.com/ith5/ith5/internal/platform/metrics"
	"github.com/ith5/ith5/internal/platform/pg"
	"github.com/ith5/ith5/internal/projects"
	"github.com/ith5/ith5/internal/releases"
	"github.com/ith5/ith5/internal/sync"
	"github.com/ith5/ith5/internal/telemetry"
)

// version 由构建时 -ldflags 注入。
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func run() error {
	migrateOnly := flag.Bool("migrate", false, "只执行数据库迁移然后退出")
	seed := flag.Bool("seed", false, "写入本地开发用的演示数据然后退出（勿在生产使用）")
	initOwner := flag.Bool("init-owner", false, "创建或更新生产首个 owner 账号然后退出")
	showVersion := flag.Bool("version", false, "打印版本")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return nil
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx := context.Background()

	dsn := os.Getenv("ITH5_DATABASE_URL")
	if dsn == "" {
		return errors.New("ITH5_DATABASE_URL 未设置")
	}
	if err := pg.Migrate(ctx, dsn); err != nil {
		return err
	}
	if *migrateOnly {
		fmt.Println("迁移完成")
		return nil
	}

	if *seed || *initOwner {
		database, err := db.Open(ctx, dsn)
		if err != nil {
			return err
		}
		defer database.Close()
		if *seed {
			return runSeed(ctx, database)
		}
		return runInitOwner(ctx, database)
	}

	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	signer, err := identity.NewSigner(cfg.JWTSecret)
	if err != nil {
		return fmt.Errorf("ITH5_JWT_SECRET: %w", err)
	}
	database, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer database.Close()

	auditStore := database.Audit()
	orgs := organizations.NewService(database.Organizations(), auditStore)
	csSvc := changesets.NewService(database.Changesets(), orgs, auditStore)
	relSvc := releases.NewService(database.Releases(), csSvc, auditStore)
	janitor := maintenance.New(database.Maintenance(), database.OIDC(), log)
	deps := api.Deps{
		Identity:     identity.NewService(database.Identity(), signer, cfg.BaseURL).WithEnrollments(database.Enrollments()),
		Enrollments:  identity.NewEnrollmentService(database.Enrollments()),
		Revoker:      identity.NewRevoker(database.Suspend()),
		OIDC:         identity.NewOIDC(database.OIDC(), cfg.BaseURL+"/v1/auth/oidc/callback"),
		OIDCStore:    database.OIDC(),
		Orgs:         orgs,
		Projects:     projects.NewService(database.Projects(), auditStore),
		Sync:         sync.NewService(database.Sync()),
		Changesets:   csSvc,
		Releases:     relSvc,
		Resources:    database.Resources(),
		Grants:       database.Grants(),
		Knowledge:    knowledge.NewService(database.Knowledge(), csSvc, relSvc, database.Resources(), orgs, auditStore),
		Telemetry:    telemetry.NewService(database.Telemetry()),
		Audit:        auditStore,
		Idempotency:  database.Idempotency(),
		Reconcile:    database.OIDC(),
		Metrics:      metrics.New(),
		MetricsToken: cfg.MetricsToken,
		Janitor:      janitor,
		BaseURL:      cfg.BaseURL,
		Log:          log,
	}
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           api.New(deps).Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// 维护任务与服务同生命周期：每天一轮保留期清理与对账统计
	jobCtx, cancelJobs := context.WithCancel(ctx)
	defer cancelJobs()
	go janitor.Run(jobCtx)

	errCh := make(chan error, 1)
	go func() {
		log.Info("ith5-server 启动", "addr", cfg.ListenAddr, "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		return err
	case <-stop:
		log.Info("收到退出信号，正在关闭")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func runSeed(ctx context.Context, database *db.DB) error {
	pw := envOr("ITH5_SEED_PASSWORD", "demo-password")
	hash, err := identity.HashPassword(pw)
	if err != nil {
		return err
	}
	ownerID, err := database.Bootstrap().SeedDemo(ctx, "demo", "demo@example.com", hash)
	if err != nil {
		return err
	}
	fmt.Printf("演示数据就绪\n  org   : demo\n  owner : demo@example.com / %s\n  member: member@example.com / %s\n  id    : %s\n", pw, pw, ownerID)
	return nil
}

func runInitOwner(ctx context.Context, database *db.DB) error {
	orgSlug := strings.TrimSpace(envOr("ITH5_INIT_ORG_SLUG", "ith5"))
	orgName := strings.TrimSpace(envOr("ITH5_INIT_ORG_NAME", "ITH5"))
	email := strings.TrimSpace(os.Getenv("ITH5_INIT_EMAIL"))
	name := strings.TrimSpace(envOr("ITH5_INIT_NAME", "Admin"))
	password := os.Getenv("ITH5_INIT_PASSWORD")
	switch {
	case email == "":
		return errors.New("-init-owner 需要设置 ITH5_INIT_EMAIL")
	case orgSlug == "":
		return errors.New("-init-owner 需要设置 ITH5_INIT_ORG_SLUG")
	case password == "":
		return errors.New("-init-owner 需要设置 ITH5_INIT_PASSWORD")
	case len(password) < 12:
		return errors.New("初始化 owner 密码至少 12 个字符")
	}
	hash, err := identity.HashPassword(password)
	if err != nil {
		return err
	}
	userID, err := database.Bootstrap().InitOwner(ctx, orgName, orgSlug, email, name, hash)
	if err != nil {
		return err
	}
	fmt.Printf("owner 账号就绪\n  org  : %s\n  用户 : %s\n  id   : %s\n", orgSlug, email, userID)
	return nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
