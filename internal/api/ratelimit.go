package api

import (
	"net/http"
	"sync"
	"time"
)

// limiter 是一个简单的滑动窗口限流器。
//
// 刻意不引 Redis：V1 的规模下单实例内存计数足够（技术方案 §3.1）。
// 多实例部署时每个实例各自计数，等效阈值放大 N 倍——对「防暴力破解」
// 这个目的仍然有效，因为攻击者不知道自己会落到哪个实例。
type limiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	limit  int
	window time.Duration
	lastGC time.Time
}

func newLimiter(limit int, window time.Duration) *limiter {
	return &limiter{hits: map[string][]time.Time{}, limit: limit, window: window, lastGC: time.Now()}
}

func (l *limiter) allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Sub(l.lastGC) > l.window {
		for k, ts := range l.hits {
			if len(ts) == 0 || now.Sub(ts[len(ts)-1]) > l.window {
				delete(l.hits, k)
			}
		}
		l.lastGC = now
	}

	cutoff := now.Add(-l.window)
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.limit {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	return true
}

// rateLimit 按客户端 IP 做粗粒度限流。
//
// **它不是暴力破解的主要防线。** 企业环境里整个办公室常在同一个
// NAT 出口 IP 后面，按 IP 收紧会让同事互相挤占配额 —— 周一早上
// 一批新人同时入职就会被自己人挡住。
//
// 因此这里的阈值定得宽，只挡明显异常的速率；真正防暴力破解的是
// allowPassword 的「IP + 账号」双维度限流。
// allowPassword 按「IP + 账号」限流密码尝试。
//
// 这是防暴力破解的主要防线（技术方案 §6.2 的「IP + code 双维度」）：
// 针对单个账号的猜测很快会被挡住，而同一出口 IP 下的其他同事不受影响。
// 攻击者轮换邮箱可以绕过这一层，但会撞上更宽的 IP 限流。
//
// 放在 handler 内部而非中间件，因为账号在请求体里，
// 中间件读它就得先消费 body。
func (s *Server) allowPassword(r *http.Request, account string) bool {
	return s.passwordLimit.allow(clientIP(r) + "|" + account)
}

func (s *Server) rateLimit(l *limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !l.allow(clientIP(r)) {
				w.Header().Set("Retry-After", "60")
				s.fail(w, r, http.StatusTooManyRequests, "rate_limited",
					"请求过于频繁，请稍后再试")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
