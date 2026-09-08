package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/ith5/ith5/internal/auth"
	"github.com/ith5/ith5/internal/db"
	"github.com/ith5/ith5/web"
)

const (
	manifestTTLSeconds = 1800
	maxEventsPerBatch  = 200
	maxBodyBytes       = 4 << 20 // 4 MiB
)

type Server struct {
	db      *db.DB
	signer  *auth.Signer
	baseURL string
	distDir string
	log     *slog.Logger

	// authLimit 是粗粒度的 IP 限流，只挡明显异常的速率。
	// 阈值宽，因为整个办公室常共用一个 NAT 出口 IP。
	authLimit *limiter
	// passwordLimit 按「IP + 账号」限流，是防暴力破解的主要防线。
	passwordLimit *limiter
	// pollLimit：轮询本身是正常行为，阈值最宽，只挡异常速率。
	pollLimit *limiter
}

func New(database *db.DB, signer *auth.Signer, baseURL, distDir string, log *slog.Logger) *Server {
	return &Server{
		db: database, signer: signer, baseURL: baseURL, distDir: distDir, log: log,
		authLimit:     newLimiter(300, time.Minute),
		passwordLimit: newLimiter(10, time.Minute),
		pollLimit:     newLimiter(600, time.Minute),
	}
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	// 运维探针：不鉴权、不碰数据库连接池以外的东西
	r.Get("/healthz", s.healthz)

	r.Route("/api/v1", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(s.rateLimit(s.authLimit))
			r.Post("/auth/device/start", s.deviceStart)
			r.Post("/auth/device/activate", s.deviceActivate)
			r.Post("/auth/refresh", s.refresh)
		})
		r.Group(func(r chi.Router) {
			r.Use(s.rateLimit(s.pollLimit))
			r.Post("/auth/device/poll", s.devicePoll)
		})

		r.Group(func(r chi.Router) {
			r.Use(s.rateLimit(s.authLimit))
			r.Post("/auth/login", s.webLogin)
		})

		// CLI 面：令牌用途必须是 cli
		r.Group(func(r chi.Router) {
			r.Use(s.requireCLI)
			r.Get("/manifest", s.manifest)
			r.Get("/bundles/{bundleID}/versions/{version}", s.bundleVersion)
			r.Post("/distribution-events", s.distributionEvents)
			r.Post("/events", s.executionEvents)
		})

		// 管理面：令牌用途必须是 web，且角色为 owner/admin
		r.Route("/admin", func(r chi.Router) {
			r.Use(s.requireAdmin)
			r.Get("/bundles", s.adminListBundles)
			r.Post("/bundles", s.adminCreateBundle)
			r.Get("/bundles/{id}", s.adminGetBundle)
			r.Put("/bundles/{id}/draft", s.adminSaveDraft)
			r.Post("/bundles/{id}/publish", s.adminPublish)
			r.Get("/bundles/{id}/versions", s.adminListVersions)
			r.Post("/bundles/{id}/rollback/{version}", s.adminRollback)
			r.Post("/bundles/{id}/archive", s.adminArchiveBundle)

			r.Get("/groups", s.adminListGroups)
			r.Post("/groups", s.adminCreateGroup)
			r.Put("/groups/{id}/bundles", s.adminSetGroupBundles)
			r.Post("/groups/{id}/archive", s.adminArchiveGroup)

			r.Get("/assignments", s.adminListAssignments)
			r.Post("/assignments", s.adminCreateAssignment)
			r.Delete("/assignments/{id}", s.adminDeleteAssignment)

			r.Get("/members", s.adminListMembers)
			r.Post("/members", s.adminCreateMember)
			r.Post("/members/{id}/status", s.adminSetMemberStatus)
			r.Post("/members/{id}/password", s.adminResetMemberPassword)

			r.Get("/audit/distributions", s.adminAudit)
			r.Get("/audit/executions", s.adminExecutions)
			r.Get("/health/stale-machines", s.adminStaleMachines)
			r.Get("/explain", s.adminExplain)
		})
	})

	s.mountDist(r)
	s.mountWeb(r)
	return r
}

// healthz 供负载均衡与运维探活。
func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.db.Pool().Ping(ctx); err != nil {
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "unhealthy", "detail": "数据库不可用"})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// mountDist 托管客户端二进制与安装脚本。
//
// 这样新人只需要一条 URL，不必访问任何额外的下载站点：
//
//	curl -fsSL https://<服务端>/install.sh | sh
func (s *Server) mountDist(r chi.Router) {
	if s.distDir == "" {
		return
	}
	if _, err := os.Stat(s.distDir); err != nil {
		s.log.Warn("未找到客户端产物目录，install.sh 与二进制下载不可用",
			"dir", s.distDir, "提示", "运行 scripts/build-release.sh")
		return
	}
	r.Handle("/dist/*", http.StripPrefix("/dist/", http.FileServer(http.Dir(s.distDir))))

	// install.sh 里的服务端地址在下发时替换成本实例的真实地址，
	// 免得管理员还要手工改脚本
	r.Get("/install.sh", func(w http.ResponseWriter, req *http.Request) {
		b, err := os.ReadFile(filepath.Join(s.distDir, "..", "scripts", "install.sh"))
		if err != nil {
			http.NotFound(w, req)
			return
		}
		out := strings.ReplaceAll(string(b), "__SERVER__", s.baseURL)
		w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
		w.Write([]byte(out))
	})
}

// mountWeb 托管管理后台的静态产物。
//
// 前端是单页应用：任何非 /api 的路径都回 index.html，由前端路由接管
// （/activate 是 CLI 登录流程的一环，必须能直接访问）。
func (s *Server) mountWeb(r chi.Router) {
	assets := web.Dist()
	if assets == nil {
		s.log.Warn("未找到管理后台构建产物，仅提供 API（在 web/ 执行 npm run build）")
		return
	}
	files := http.FileServer(http.FS(assets))
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasPrefix(req.URL.Path, "/api/") {
			s.fail(w, req, http.StatusNotFound, "not_found", "接口不存在")
			return
		}
		if f, err := assets.Open(strings.TrimPrefix(req.URL.Path, "/")); err == nil {
			f.Close()
			files.ServeHTTP(w, req)
			return
		}
		req2 := req.Clone(req.Context())
		req2.URL.Path = "/"
		files.ServeHTTP(w, req2)
	})
}

// ---------------------------------------------------------------
// 中间件与上下文
// ---------------------------------------------------------------

type ctxKey int

const principalKey ctxKey = iota

// principal 是经过认证且**实时校验过账号状态**的调用方。
type principal struct {
	UserID    string
	OrgID     string
	Role      string
	MachineID string
}

// requireCLI 校验 CLI 用途的访问令牌，并实时读取用户状态。
//
// 只验 JWT 是不够的：尚未过期的令牌不能让一个已停用的账号继续访问（D4）。
func (s *Server) requireCLI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		if tok == "" {
			s.fail(w, r, http.StatusUnauthorized, "unauthorized", "缺少访问令牌")
			return
		}
		claims, err := s.signer.Verify(tok, auth.PurposeCLI)
		if err != nil {
			s.fail(w, r, http.StatusUnauthorized, "unauthorized", "访问令牌无效或已过期")
			return
		}
		u, err := s.db.GetUser(r.Context(), claims.UserID())
		if err != nil || u.Suspended {
			s.fail(w, r, http.StatusUnauthorized, "unauthorized", "账号不可用")
			return
		}
		// 令牌里的 org 必须与库里一致，防止令牌被跨租户重用。
		if u.OrgID != claims.OrgID {
			s.fail(w, r, http.StatusForbidden, "forbidden", "无权访问")
			return
		}
		p := principal{UserID: u.ID, OrgID: u.OrgID, Role: u.Role, MachineID: claims.MachineID}
		if p.MachineID != "" {
			_ = s.db.TouchMachine(r.Context(), p.MachineID)
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, p)))
	})
}

func withPrincipal(r *http.Request, p principal) context.Context {
	return context.WithValue(r.Context(), principalKey, p)
}

func mustPrincipal(r *http.Request) principal {
	p, _ := r.Context().Value(principalKey).(principal)
	return p
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if after, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	return ""
}

// ---------------------------------------------------------------
// 响应helper
// ---------------------------------------------------------------

func (s *Server) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// fail 返回固定结构的错误。生产环境不返回栈或数据库错误细节（技术方案 §16.1）。
func (s *Server) fail(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	s.writeJSON(w, status, ErrorResp{
		Error:     code,
		Message:   msg,
		RequestID: middleware.GetReqID(r.Context()),
	})
}

// decode 严格解析：未知字段视为错误。
//
// 用于管理接口——那里的未知字段多半是前端写错了字段名，
// 静默忽略会让管理员以为改动生效了，其实没有。
func (s *Server) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	return s.decodeWith(w, r, v, true)
}

// decodeLenient 宽松解析：未知字段直接忽略。
//
// 用于客户端上报接口。滚动升级期客户端与服务端版本必然混杂，
// 新客户端多送一个字段就整批 400 的话，客户端会无限重试，
// **队列永远排不空、审计链断掉** —— 那比忽略一个字段糟得多。
//
// 隐私保证来自 handler 里对 summary 的显式重建，不来自这里的严格性
// （技术方案 §10.4）。
func (s *Server) decodeLenient(w http.ResponseWriter, r *http.Request, v any) bool {
	return s.decodeWith(w, r, v, false)
}

func (s *Server) decodeWith(w http.ResponseWriter, r *http.Request, v any, strict bool) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(v); err != nil {
		s.fail(w, r, http.StatusBadRequest, "bad_request", "请求体格式非法")
		return false
	}
	return true
}

func (s *Server) internal(w http.ResponseWriter, r *http.Request, err error, what string) {
	s.log.Error(what, "err", err, "request_id", middleware.GetReqID(r.Context()))
	s.fail(w, r, http.StatusInternalServerError, "internal", "服务内部错误")
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

var errNotFound = errors.New("not found")
