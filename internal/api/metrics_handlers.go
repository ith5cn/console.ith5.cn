package api

import (
	"crypto/subtle"
	"net/http"

	"github.com/ith5/ith5/internal/platform/httpx"
)

// metricsEndpoint 暴露 Prometheus 文本指标。配置了 MetricsToken 就要求 Bearer；
// 指标里只有计数与耗时，没有租户数据，但路由模板会暴露接口面，默认仍建议只对内网开放。
func (s *Server) metricsEndpoint(w http.ResponseWriter, r *http.Request) {
	if s.MetricsToken != "" && subtle.ConstantTimeCompare([]byte(httpx.Bearer(r)), []byte(s.MetricsToken)) != 1 {
		httpx.WriteError(w, r, s.log, httpx.ErrAuthRequired)
		return
	}
	s.Metrics.Handler().ServeHTTP(w, r)
}
