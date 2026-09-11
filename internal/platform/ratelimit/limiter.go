// Package ratelimit 是单实例内存里的滑动窗口限流。
//
// 刻意不引 Redis：当前规模下单实例计数足够。多实例部署时每个实例各自计数，
// 等效阈值放大 N 倍——对「防暴力破解」这个目的仍然有效，攻击者不知道自己会落到哪个实例。
package ratelimit

import (
	"net/http"
	"sync"
	"time"

	"github.com/ith5/ith5/internal/platform/httpx"
)

// Limiter 按 key 限制窗口内的命中次数。
type Limiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	limit  int
	window time.Duration
	lastGC time.Time
	now    func() time.Time
}

// New 创建限流器：window 内最多 limit 次。
func New(limit int, window time.Duration) *Limiter {
	return &Limiter{
		hits: map[string][]time.Time{}, limit: limit, window: window,
		lastGC: time.Now(), now: time.Now,
	}
}

// Allow 记一次命中并返回是否放行。
func (l *Limiter) Allow(key string) bool {
	now := l.now()
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

// ByIP 返回按客户端 IP 限流的中间件。
//
// 它不是暴力破解的主要防线：企业里整个办公室常在同一个 NAT 出口后面，
// 按 IP 收紧会让同事互相挤占配额。阈值宽，只挡明显异常的速率；
// 针对账号的限流由认证 handler 用 Allow(ip + "|" + account) 单独做。
func (l *Limiter) ByIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.Allow(httpx.ClientIP(r)) {
			httpx.WriteError(w, r, nil, httpx.ErrRateLimited)
			return
		}
		next.ServeHTTP(w, r)
	})
}
