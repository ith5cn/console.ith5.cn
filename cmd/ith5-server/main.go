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
	"github.com/ith5/ith5/internal/auth"
	"github.com/ith5/ith5/internal/db"
)

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
	initOrgSlug := flag.String("init-org-slug", envOr("ITH5_INIT_ORG_SLUG", "ith5"), "初始化 owner 的组织 slug")
	initOrgName := flag.String("init-org-name", envOr("ITH5_INIT_ORG_NAME", "ITH5"), "初始化 owner 的组织名称")
	initEmail := flag.String("init-email", os.Getenv("ITH5_INIT_EMAIL"), "初始化 owner 邮箱")
	initName := flag.String("init-name", envOr("ITH5_INIT_NAME", "Admin"), "初始化 owner 显示名")
	initPassword := flag.String("init-password", os.Getenv("ITH5_INIT_PASSWORD"), "初始化 owner 密码；生产建议使用 ITH5_INIT_PASSWORD 环境变量")
	flag.Parse()

	dsn := os.Getenv("ITH5_DATABASE_URL")
	if dsn == "" {
		return errors.New("ITH5_DATABASE_URL 未设置")
	}
	addr := envOr("ITH5_LISTEN_ADDR", ":8080")
	baseURL := envOr("ITH5_BASE_URL", "http://localhost:8080")
	distDir := envOr("ITH5_DIST_DIR", "dist")
	secret := os.Getenv("ITH5_JWT_SECRET")

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx := context.Background()

	if err := db.Migrate(ctx, dsn); err != nil {
		return err
	}
	if *migrateOnly {
		fmt.Println("迁移完成")
		return nil
	}

	if *seed {
		database, err := db.Open(ctx, dsn)
		if err != nil {
			return err
		}
		defer database.Close()
		pw := envOr("ITH5_SEED_PASSWORD", "demo-password")
		hash, err := auth.HashPassword(pw)
		if err != nil {
			return err
		}
		userID, err := database.SeedDemo(ctx, "demo", "demo@example.com", hash)
		if err != nil {
			return err
		}
		fmt.Printf("演示数据就绪\n  org  : demo\n  用户 : demo@example.com / %s\n  id   : %s\n", pw, userID)
		return nil
	}

	if *initOwner {
		email := strings.TrimSpace(*initEmail)
		password := *initPassword
		if email == "" {
			return errors.New("-init-owner 需要设置 -init-email 或 ITH5_INIT_EMAIL")
		}
		if strings.TrimSpace(*initOrgSlug) == "" {
			return errors.New("-init-owner 需要设置 -init-org-slug 或 ITH5_INIT_ORG_SLUG")
		}
		if password == "" {
			return errors.New("-init-owner 需要设置 -init-password 或 ITH5_INIT_PASSWORD")
		}
		if len(password) < 12 {
			return errors.New("初始化 owner 密码至少 12 个字符")
		}

		database, err := db.Open(ctx, dsn)
		if err != nil {
			return err
		}
		defer database.Close()

		hash, err := auth.HashPassword(password)
		if err != nil {
			return err
		}
		userID, err := database.InitOwner(ctx, strings.TrimSpace(*initOrgName), strings.TrimSpace(*initOrgSlug), email, strings.TrimSpace(*initName), hash)
		if err != nil {
			return err
		}
		fmt.Printf("owner 账号就绪\n  org  : %s\n  用户 : %s\n  id   : %s\n", strings.TrimSpace(*initOrgSlug), email, userID)
		return nil
	}

	signer, err := auth.NewSigner(secret)
	if err != nil {
		return fmt.Errorf("ITH5_JWT_SECRET: %w", err)
	}
	database, err := db.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer database.Close()

	srv := &http.Server{
		Addr:              addr,
		Handler:           api.New(database, signer, baseURL, distDir, log).Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("ith5-server 启动", "addr", addr)
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

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
