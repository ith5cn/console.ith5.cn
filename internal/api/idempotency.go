package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"

	"github.com/ith5/ith5/internal/idempotency"
	"github.com/ith5/ith5/internal/platform/httpx"
)

// idempotencyMaxCached 是会被缓存的响应体上限；超过的只记状态码，重放时给空体。
const idempotencyMaxCached = 1 << 20

// idempotency 让带 Idempotency-Key 的写请求可以安全重试。
//
// 只对 POST / PUT / PATCH 生效，且必须已通过认证（作用域含 org 与 user）。
// 同 key 但方法、路径或请求体不同，返回 409 IDEMPOTENCY_CONFLICT；
// 首个请求尚未完成时收到重试，同样 409，让客户端稍后再试而不是并发执行两次。
// 重放的响应带 Idempotency-Replayed: true 头，便于客户端与日志区分。
func (s *Server) idempotency(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" || s.Idempotency == nil || !isWriteMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		if len(key) > 200 {
			httpx.WriteError(w, r, s.log, httpx.Validation("Idempotency-Key 过长"))
			return
		}
		p := principalFrom(r)

		body, err := io.ReadAll(io.LimitReader(r.Body, httpx.MaxBodyBytes+1))
		if err != nil {
			httpx.WriteError(w, r, s.log, httpx.Validation("请求体读取失败"))
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		sum := sha256.Sum256(body)

		rec := idempotency.Record{Method: r.Method, Route: r.URL.Path, BodySHA256: hex.EncodeToString(sum[:])}
		existing, created, err := s.Idempotency.Begin(r.Context(), p.OrgID, p.UserID, key, rec, idempotency.TTL)
		if err != nil {
			httpx.WriteError(w, r, s.log, err)
			return
		}
		if !created {
			replay(w, r, s, rec, existing)
			return
		}

		rw := &recordingWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)

		var resp []byte
		if rw.body.Len() <= idempotencyMaxCached {
			resp = rw.body.Bytes()
		}
		// 写回失败不影响本次响应：最坏情况是下一次重试再执行一遍并撞上 STATE_CONFLICT。
		if err := s.Idempotency.Complete(r.Context(), p.OrgID, p.UserID, key, rw.status, resp); err != nil {
			s.log.Warn("幂等结果写回失败", "err", err, "key", key)
		}
	})
}

func replay(w http.ResponseWriter, r *http.Request, s *Server, want idempotency.Record, got *idempotency.Record) {
	switch {
	case got == nil:
		httpx.WriteError(w, r, s.log, errors.New("幂等记录缺失"))
	case got.Method != want.Method || got.Route != want.Route || got.BodySHA256 != want.BodySHA256:
		httpx.WriteError(w, r, s.log, httpx.New(http.StatusConflict, httpx.CodeIdempotencyConflict, "同一个 Idempotency-Key 不能用于不同的请求"))
	case got.Status == 0:
		httpx.WriteError(w, r, s.log, httpx.New(http.StatusConflict, httpx.CodeIdempotencyConflict, "上一次同样的请求还在处理中，请稍后重试"))
	default:
		w.Header().Set("Idempotency-Replayed", "true")
		if len(got.Response) > 0 {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
		}
		w.WriteHeader(got.Status)
		_, _ = w.Write(got.Response)
	}
}

func isWriteMethod(m string) bool {
	return m == http.MethodPost || m == http.MethodPut || m == http.MethodPatch
}

// recordingWriter 在透传响应的同时留一份副本。
type recordingWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (w *recordingWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *recordingWriter) Write(b []byte) (int, error) {
	if w.body.Len() <= idempotencyMaxCached {
		w.body.Write(b)
	}
	return w.ResponseWriter.Write(b)
}
