package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
)

// MaxBodyBytes 是 JSON 请求体上限。blob 上传走单独的限制。
const MaxBodyBytes = 4 << 20

// ErrorBody 是错误响应的固定结构。
type ErrorBody struct {
	Error ErrorPayload `json:"error"`
}

// ErrorPayload 的字段集合是对客户端的承诺，只增不改。
type ErrorPayload struct {
	Code      Code   `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
	Details   any    `json:"details,omitempty"`
}

// WriteJSON 写出 JSON 响应。v 为 nil 时只写状态码。
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// WriteError 把 error 映射成响应。非 *Error 的一律 500，并把原因写日志而不是回给客户端。
func WriteError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	e := AsError(err)
	reqID := middleware.GetReqID(r.Context())
	if e.Status >= 500 && log != nil {
		log.Error("请求失败", "err", err, "request_id", reqID, "path", r.URL.Path)
	}
	if e.Code == CodeRateLimited {
		w.Header().Set("Retry-After", "60")
	}
	WriteJSON(w, e.Status, ErrorBody{Error: ErrorPayload{
		Code: e.Code, Message: e.Message, RequestID: reqID, Details: e.Details,
	}})
}

// Decode 严格解析 JSON：未知字段视为错误。
//
// 用于管理接口——那里的未知字段多半是前端写错了字段名，
// 静默忽略会让管理员以为改动生效了，其实没有。
func Decode(w http.ResponseWriter, r *http.Request, v any) error {
	return decode(w, r, v, true)
}

// DecodeLenient 宽松解析：未知字段直接忽略。
//
// 用于客户端上报接口。滚动升级期客户端与服务端版本必然混杂，
// 新客户端多送一个字段就整批拒绝的话，客户端会无限重试，队列永远排不空。
func DecodeLenient(w http.ResponseWriter, r *http.Request, v any) error {
	return decode(w, r, v, false)
}

func decode(w http.ResponseWriter, r *http.Request, v any, strict bool) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	dec := json.NewDecoder(r.Body)
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(v); err != nil {
		var tooLarge *http.MaxBytesError
		if asErr(err, &tooLarge) {
			return New(http.StatusRequestEntityTooLarge, CodePayloadTooLarge, "请求体过大")
		}
		return Validation("请求体格式非法")
	}
	return nil
}
