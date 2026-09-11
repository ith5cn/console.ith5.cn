// Package metrics 是零依赖的进程内指标：请求计数、延迟直方图、进行中请求数，
// 以 Prometheus 文本格式暴露。不引 client_golang，保持依赖面最小；需要更多时再换。
package metrics

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"
)

// 延迟桶（秒）：覆盖从缓存命中到大 blob 上传的量级。
var buckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type key struct{ method, route, status string }

// Registry 收集指标；一个进程一个。
type Registry struct {
	mu        sync.Mutex
	requests  map[key]uint64
	latency   map[key]*histogram
	inFlight  int64
	startedAt time.Time
}

type histogram struct {
	counts []uint64
	sum    float64
	total  uint64
}

// New 构造注册表。
func New() *Registry {
	return &Registry{requests: map[key]uint64{}, latency: map[key]*histogram{}, startedAt: time.Now()}
}

// Observe 记一次请求。route 应是路由模板（/v1/change-sets/{id}）而不是实际路径，避免基数爆炸。
func (r *Registry) Observe(method, route string, status int, d time.Duration) {
	k := key{method, route, strconv.Itoa(status)}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests[k]++
	h := r.latency[key{method, route, ""}]
	if h == nil {
		h = &histogram{counts: make([]uint64, len(buckets))}
		r.latency[key{method, route, ""}] = h
	}
	sec := d.Seconds()
	for i, b := range buckets {
		if sec <= b {
			h.counts[i]++
		}
	}
	h.sum += sec
	h.total++
}

// Middleware 统计经过的每个请求。
func (r *Registry) Middleware(routeOf func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			r.mu.Lock()
			r.inFlight++
			r.mu.Unlock()
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, req)
			r.mu.Lock()
			r.inFlight--
			r.mu.Unlock()
			r.Observe(req.Method, routeOf(req), sw.status, time.Since(start))
		})
	}
}

// Handler 以 Prometheus 文本格式输出。
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		r.Dump(w)
	})
}

// Dump 写出全部指标。
func (r *Registry) Dump(w io.Writer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fmt.Fprintf(w, "# HELP ith5_up 进程存活。\n# TYPE ith5_up gauge\nith5_up 1\n")
	fmt.Fprintf(w, "# HELP ith5_uptime_seconds 进程运行秒数。\n# TYPE ith5_uptime_seconds gauge\nith5_uptime_seconds %.0f\n", time.Since(r.startedAt).Seconds())
	fmt.Fprintf(w, "# HELP ith5_http_in_flight 正在处理的请求数。\n# TYPE ith5_http_in_flight gauge\nith5_http_in_flight %d\n", r.inFlight)

	fmt.Fprintf(w, "# HELP ith5_http_requests_total 按方法、路由模板与状态码计数。\n# TYPE ith5_http_requests_total counter\n")
	ks := make([]key, 0, len(r.requests))
	for k := range r.requests {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool {
		return ks[i].route+ks[i].method+ks[i].status < ks[j].route+ks[j].method+ks[j].status
	})
	for _, k := range ks {
		fmt.Fprintf(w, "ith5_http_requests_total{method=%q,route=%q,status=%q} %d\n", k.method, k.route, k.status, r.requests[k])
	}

	fmt.Fprintf(w, "# HELP ith5_http_request_duration_seconds 请求耗时。\n# TYPE ith5_http_request_duration_seconds histogram\n")
	hs := make([]key, 0, len(r.latency))
	for k := range r.latency {
		hs = append(hs, k)
	}
	sort.Slice(hs, func(i, j int) bool { return hs[i].route+hs[i].method < hs[j].route+hs[j].method })
	for _, k := range hs {
		h := r.latency[k]
		for i, b := range buckets {
			fmt.Fprintf(w, "ith5_http_request_duration_seconds_bucket{method=%q,route=%q,le=%q} %d\n", k.method, k.route, strconv.FormatFloat(b, 'g', -1, 64), h.counts[i])
		}
		fmt.Fprintf(w, "ith5_http_request_duration_seconds_bucket{method=%q,route=%q,le=\"+Inf\"} %d\n", k.method, k.route, h.total)
		fmt.Fprintf(w, "ith5_http_request_duration_seconds_sum{method=%q,route=%q} %g\n", k.method, k.route, h.sum)
		fmt.Fprintf(w, "ith5_http_request_duration_seconds_count{method=%q,route=%q} %d\n", k.method, k.route, h.total)
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
