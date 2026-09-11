// Package config 从环境变量装配服务端配置。
//
// 只在这里读 os.Getenv：其余包通过 Config 取值，密钥不得散落在业务代码里。
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Config 是服务端启动所需的全部外部配置。
type Config struct {
	// DatabaseURL 是 PostgreSQL DSN，唯一的外部依赖。
	DatabaseURL string
	// ListenAddr 是 HTTP 监听地址，默认 :8080。
	ListenAddr string
	// BaseURL 是对外可达的地址，写进设备授权的验证链接。
	BaseURL string
	// JWTSecret 用于签发访问令牌，至少 32 字节。生产环境只从 secret manager 注入。
	JWTSecret string
	// MetricsToken 非空时 GET /metrics 需要 Bearer 该值；为空则不设防，请只在内网暴露。
	MetricsToken string
}

// FromEnv 读取 ITH5_* 环境变量。缺少必填项时返回错误而不是启动一个半残的服务。
func FromEnv() (Config, error) {
	c := Config{
		DatabaseURL:  os.Getenv("ITH5_DATABASE_URL"),
		ListenAddr:   envOr("ITH5_LISTEN_ADDR", ":8080"),
		BaseURL:      strings.TrimRight(envOr("ITH5_BASE_URL", "http://localhost:8080"), "/"),
		JWTSecret:    os.Getenv("ITH5_JWT_SECRET"),
		MetricsToken: os.Getenv("ITH5_METRICS_TOKEN"),
	}
	var missing []string
	if c.DatabaseURL == "" {
		missing = append(missing, "ITH5_DATABASE_URL")
	}
	if c.JWTSecret == "" {
		missing = append(missing, "ITH5_JWT_SECRET")
	}
	if len(missing) > 0 {
		return c, fmt.Errorf("缺少环境变量: %s", strings.Join(missing, ", "))
	}
	if len(c.JWTSecret) < 32 {
		return c, errors.New("ITH5_JWT_SECRET 至少需要 32 字节")
	}
	return c, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
