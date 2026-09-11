// Package api 是 HTTP 层：路由、认证中间件、请求解析与响应映射。
// 业务规则与事务不进入本包。
package api

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/ith5/ith5/internal/audit"
	"github.com/ith5/ith5/internal/changesets"
	"github.com/ith5/ith5/internal/db"
	"github.com/ith5/ith5/internal/idempotency"
	"github.com/ith5/ith5/internal/identity"
	"github.com/ith5/ith5/internal/knowledge"
	"github.com/ith5/ith5/internal/maintenance"
	"github.com/ith5/ith5/internal/organizations"
	"github.com/ith5/ith5/internal/platform/httpx"
	"github.com/ith5/ith5/internal/platform/metrics"
	"github.com/ith5/ith5/internal/platform/ratelimit"
	"github.com/ith5/ith5/internal/projects"
	"github.com/ith5/ith5/internal/releases"
	"github.com/ith5/ith5/internal/sync"
	"github.com/ith5/ith5/internal/telemetry"
	"github.com/ith5/ith5/web"
)

// Version 是协议版本，随 /v1/capabilities 下发。
const Version = "1"

// MinClientVersion 是服务端接受的最低客户端版本（X-Client-Version 头）。
// 客户端不带头时放行：浏览器与旧脚本不受影响；带了且过低返回 426，让客户端提示升级。
const MinClientVersion = "0.1.0"

// Deps 是 Server 需要的全部领域服务。
type Deps struct {
	Identity    *identity.Service
	Enrollments *identity.EnrollmentService
	Revoker     *identity.Revoker
	// OIDC 可为 nil：未配置时相关路由返回 422。
	OIDC       *identity.OIDC
	OIDCStore  identity.OIDCStore
	Orgs       *organizations.Service
	Projects   *projects.Service
	Sync       *sync.Service
	Changesets *changesets.Service
	Releases   *releases.Service
	Resources  *db.ResourcesStore
	Grants     *db.GrantsStore
	Knowledge  *knowledge.Service
	Telemetry  *telemetry.Service
	Audit      audit.Store
	// Reconcile 与 Janitor 可为 nil：分别关闭 IdP 对账与维护任务接口。
	Reconcile identity.ReconcileStore
	Janitor   *maintenance.Janitor
	// Metrics 可为 nil：为 nil 时不统计也不暴露 /metrics。MetricsToken 非空时 /metrics 需要 Bearer。
	Metrics      *metrics.Registry
	MetricsToken string
	// Idempotency 可为 nil：为 nil 时忽略 Idempotency-Key 头。
	Idempotency idempotency.Store
	BaseURL     string
	Log         *slog.Logger
}

// Server 装配全部 handler 的依赖。
type Server struct {
	Deps
	log *slog.Logger

	// authLimit 是粗粒度的 IP 限流，只挡明显异常的速率。阈值宽，因为整个办公室常共用一个出口 IP。
	authLimit *ratelimit.Limiter
	// passwordLimit 按「IP + 账号」限流，是防暴力破解的主要防线。
	passwordLimit *ratelimit.Limiter
	// pollLimit：轮询本身是正常行为，阈值最宽。
	pollLimit *ratelimit.Limiter
}

// New 构造 Server。
func New(d Deps) *Server {
	if d.Audit == nil {
		d.Audit = audit.Nop{}
	}
	return &Server{
		Deps:          d,
		log:           d.Log,
		authLimit:     ratelimit.New(300, time.Minute),
		passwordLimit: ratelimit.New(10, time.Minute),
		pollLimit:     ratelimit.New(600, time.Minute),
	}
}

// Routes 组装路由。
func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))
	r.Use(requestIDHeader, clientVersionGate)
	if s.Metrics != nil {
		// 路由模板在 handler 执行完后才可取，所以中间件在结束时读 chi 的 RoutePattern
		r.Use(s.Metrics.Middleware(func(req *http.Request) string {
			if rc := chi.RouteContext(req.Context()); rc != nil {
				if p := rc.RoutePattern(); p != "" {
					return p
				}
			}
			return "unmatched"
		}))
		r.Get("/metrics", s.metricsEndpoint)
	}

	r.Get("/healthz", s.healthz)

	r.Route("/v1", func(r chi.Router) {
		r.Get("/capabilities", s.capabilities)

		// 未登录：认证入口
		r.Group(func(r chi.Router) {
			r.Use(s.authLimit.ByIP)
			r.Post("/auth/login", s.login)
			r.Post("/auth/session", s.session)
			r.Post("/organizations", s.createOrganization)
			r.Post("/auth/device/start", s.deviceStart)
			r.Post("/auth/token", s.refresh)
			r.Get("/auth/oidc/authorize", s.oidcAuthorize)
			r.Get("/auth/oidc/callback", s.oidcCallback)
		})
		r.Group(func(r chi.Router) {
			r.Use(s.pollLimit.ByIP)
			r.Post("/auth/device/poll", s.devicePoll)
		})

		// 任一类令牌
		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth(identity.TokenKindSession, identity.TokenKindDevice), s.idempotency)
			r.Get("/me", s.me)
			r.Post("/auth/revocations", s.revoke)
			r.Get("/organizations", s.listOrganizations)
			r.Get("/organizations/{id}", s.getOrganization)
			r.Get("/organizations/{id}/policy", s.getPolicy)
			r.Get("/teams", s.listTeams)
			r.Get("/teams/{id}", s.getTeam)
			r.Get("/projects", s.listProjects)
			r.Get("/projects/{id}", s.getProject)
			r.Get("/members", s.listMembers)
			r.Get("/members/{id}", s.getMember)
			r.Get("/devices", s.listDevices)
			r.Patch("/devices/{id}", s.renameDevice)
			r.Delete("/devices/{id}", s.revokeDevice)

			r.Post("/bindings", s.createBinding)
			r.Get("/bindings", s.listBindings)
			r.Get("/bindings/{id}", s.getBinding)
			r.Patch("/bindings/{id}", s.updateBinding)
			r.Delete("/bindings/{id}", s.deleteBinding)

			// 同步：快照、内容、回执
			r.Get("/bindings/{id}/snapshot", s.snapshot)
			r.Post("/bindings/{id}/sync-results", s.syncResults)
			r.Get("/blobs/{sha256}", s.blob)

			// 内容与变更：设备令牌也能用（teamai push / contribute 走的就是它）
			r.Post("/blobs", s.uploadBlob)
			r.Get("/resources", s.listResources)
			r.Get("/resources/{id}", s.getResource)
			r.Get("/resource-versions/{id}", s.getResourceVersion)
			r.Get("/change-sets", s.listChangesets)
			r.Post("/change-sets", s.createChangeset)
			r.Get("/change-sets/{id}", s.getChangeset)
			r.Patch("/change-sets/{id}", s.updateChangeset)
			r.Post("/change-sets/{id}/submit", s.submitChangeset)
			r.Post("/change-sets/{id}/cancel", s.cancelChangeset)
			r.Get("/change-sets/{id}/diff", s.diffChangeset)
			r.Get("/releases", s.listReleases)
			r.Get("/releases/{id}", s.getRelease)

			// 知识与上报（teamai contribute / recall votes / session save 走这里）
			r.Post("/learnings", s.contribute)
			r.Get("/learnings", s.listLearnings)
			r.Get("/learnings/{id}", s.getLearning)
			r.Post("/learnings/{id}/archive", s.archiveLearning)
			r.Post("/learnings/{id}/promote", s.promoteLearning)
			r.Post("/reports/events", s.reportEvents)
			r.Get("/reports/digest", s.digest)
			r.Get("/reports/kb-health", s.kbHealth)
			r.Get("/reports/usage", s.usage)

			// 权限组与授权：读任何成员可看（explain 需要），写在下面的会话组
			r.Get("/groups", s.listGroups)
			r.Get("/assignments", s.listAssignments)
		})

		// 审核与发布只允许浏览器会话：这是人的决定，不该由脚本持设备令牌代做
		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth(identity.TokenKindSession), s.rejectViewerWrites, s.idempotency)
			r.Post("/change-sets/{id}/reviews", s.reviewChangeset)
			r.Post("/change-sets/{id}/publish", s.publishChangeset)
			r.Post("/releases/{id}/rollback", s.rollbackRelease)
			r.Put("/resources/{id}/tags", s.setResourceTags)
		})

		// 只允许浏览器会话：管理写操作与设备批准
		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth(identity.TokenKindSession), s.rejectViewerWrites, s.idempotency)
			r.Get("/auth/device/peek", s.devicePeek)
			r.Post("/auth/device/activate", s.deviceActivate)

			r.Patch("/organizations/{id}", s.updateOrganization)
			r.Put("/organizations/{id}/policy", s.putPolicy)

			r.Post("/teams", s.createTeam)
			r.Patch("/teams/{id}", s.updateTeam)
			r.Delete("/teams/{id}", s.archiveTeam)
			r.Put("/teams/{id}/members/{userID}", s.putTeamMember)
			r.Delete("/teams/{id}/members/{userID}", s.deleteTeamMember)
			r.Get("/teams/{id}/reviewers", s.listTeamReviewers)
			r.Put("/teams/{id}/reviewers", s.putTeamReviewers)

			r.Post("/projects", s.createProject)
			r.Patch("/projects/{id}", s.updateProject)
			r.Put("/projects/{id}/members/{userID}", s.putProjectMember)
			r.Delete("/projects/{id}/members/{userID}", s.deleteProjectMember)
			r.Get("/projects/{id}/reviewers", s.listProjectReviewers)
			r.Put("/projects/{id}/reviewers", s.putProjectReviewers)

			r.Post("/members", s.createMember)
			r.Post("/members/{id}/status", s.setMemberStatus)
			r.Post("/members/{id}/role", s.setMemberRole)
			r.Post("/members/{id}/password", s.resetMemberPassword)

			r.Get("/enrollments", s.listEnrollments)
			r.Post("/enrollments", s.createEnrollment)
			r.Delete("/enrollments/{id}", s.revokeEnrollment)

			r.Get("/idp/config", s.getIdPConfig)
			r.Put("/idp/config", s.putIdPConfig)
			r.Get("/idp/mappings", s.listIdPMappings)
			r.Put("/idp/mappings", s.putIdPMappings)
			r.Get("/idp/reconcile", s.getIdPReconcile)
			r.Post("/idp/reconcile/apply", s.applyIdPReconcile)
			r.Post("/maintenance/run", s.runMaintenance)

			r.Get("/audit/events", s.listAudit)
			r.Get("/audit/distributions", s.listDistributions)
			r.Get("/audit/executions", s.listExecutions)

			r.Post("/groups", s.createGroup)
			r.Patch("/groups/{id}", s.updateGroup)
			r.Put("/groups/{id}/bundles", s.setGroupBundles)
			r.Post("/assignments", s.createAssignment)
			r.Delete("/assignments/{id}", s.deleteAssignment)
		})
	})

	s.mountWeb(r)
	return r
}

// healthz 供负载均衡与运维探活。
func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// capabilities 让客户端发现协议版本与限制，不硬编码另一套值。
func (s *Server) capabilities(w http.ResponseWriter, r *http.Request) {
	auth := []string{"password", "device"}
	if s.OIDC != nil {
		auth = append(auth, "oidc")
	}
	httpx.WriteJSON(w, http.StatusOK, CapabilitiesResp{
		Version: Version, APIVersion: "v1", MinClientVersion: MinClientVersion,
		Auth: auth,
		Limits: Limits{
			MaxBlobBytes:     32 << 20,
			MaxSnapshotBytes: 1 << 30,
			MaxEntries:       5000,
		},
	})
}

// mountWeb 托管管理后台的静态产物。
//
// 前端是单页应用：任何非 /v1 的路径都回 index.html，由前端路由接管
// （/activate 是设备授权流的一环，必须能直接访问）。
func (s *Server) mountWeb(r chi.Router) {
	assets := web.Dist()
	if assets == nil {
		s.log.Warn("未找到管理后台构建产物，仅提供 API（在 web/ 执行 npm run build）")
		return
	}
	files := http.FileServer(http.FS(assets))
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasPrefix(req.URL.Path, "/v1/") {
			httpx.WriteError(w, req, s.log, httpx.ErrNotFound)
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
// 认证中间件与上下文
// ---------------------------------------------------------------

type ctxKey int

const principalKey ctxKey = iota

// Principal 是经过认证且实时校验过账号状态的调用方，附带一次加载好的角色关系。
type Principal struct {
	identity.Membership
	MachineID string
	TokenKind identity.TokenKind
	Subject   organizations.Subject
}

// requireAuth 校验访问令牌并实时读取成员状态。
//
// 只验 JWT 是不够的：尚未过期的令牌不能让一个已停用的账号继续访问。
func (s *Server) requireAuth(kinds ...identity.TokenKind) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok := httpx.Bearer(r)
			if tok == "" {
				httpx.WriteError(w, r, s.log, httpx.ErrAuthRequired)
				return
			}
			var claims *identity.Claims
			for _, k := range kinds {
				if c, err := s.Identity.Signer().Verify(tok, k); err == nil {
					claims = c
					break
				}
			}
			if claims == nil {
				httpx.WriteError(w, r, s.log, httpx.New(http.StatusUnauthorized, httpx.CodeAuthRequired, "访问令牌无效或已过期"))
				return
			}
			m, err := s.Identity.Store().GetMembership(r.Context(), claims.UserID())
			if err != nil || m.Suspended || m.OrgID != claims.OrgID {
				httpx.WriteError(w, r, s.log, httpx.New(http.StatusUnauthorized, httpx.CodeAuthRequired, "账号不可用"))
				return
			}
			p := Principal{Membership: m, MachineID: claims.MachineID, TokenKind: claims.Kind}
			if p.MachineID != "" {
				mc, err := s.Identity.Store().GetMachine(r.Context(), p.MachineID)
				if err != nil || mc.RevokedAt != nil {
					httpx.WriteError(w, r, s.log, httpx.New(http.StatusUnauthorized, httpx.CodeAuthRequired, "设备已撤销"))
					return
				}
				_ = s.Identity.Store().TouchMachine(r.Context(), p.MachineID, time.Now())
			}
			sub, err := s.Orgs.Store().LoadSubject(r.Context(), m.UserID)
			if err != nil {
				httpx.WriteError(w, r, s.log, err)
				return
			}
			p.Subject = sub
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, p)))
		})
	}
}

// rejectViewerWrites 让 viewer 只能读：演示账号不能改任何东西。
func (s *Server) rejectViewerWrites(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		if p.Role == identity.RoleViewer && r.Method != http.MethodGet && r.Method != http.MethodHead {
			httpx.WriteError(w, r, s.log, httpx.New(http.StatusForbidden, httpx.CodeAccessDenied, "只读账号不能修改数据"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) oidcStore() identity.OIDCStore { return s.OIDCStore }

func principalFrom(r *http.Request) Principal {
	p, _ := r.Context().Value(principalKey).(Principal)
	return p
}

func (p Principal) orgActor(r *http.Request) organizations.Actor {
	return organizations.Actor{UserID: p.UserID, OrgID: p.OrgID, RequestID: middleware.GetReqID(r.Context()), Subject: p.Subject}
}

func (p Principal) projectActor(r *http.Request) projects.Actor {
	return projects.Actor{UserID: p.UserID, OrgID: p.OrgID, MachineID: p.MachineID, RequestID: middleware.GetReqID(r.Context()), Subject: p.Subject}
}

// requestIDHeader 把请求 ID 回写到响应头，便于用户报障时对应日志。
func requestIDHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := middleware.GetReqID(r.Context()); id != "" {
			w.Header().Set("X-Request-Id", id)
		}
		next.ServeHTTP(w, r)
	})
}
