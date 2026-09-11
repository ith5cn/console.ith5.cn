// Package httpx 是 HTTP 层的公共件：错误码、JSON 读写、请求上下文。
//
// 业务包返回 *Error 或普通 error，由 handler 统一映射成响应；
// 业务包不 import net/http 之外的任何 HTTP 细节。
package httpx

import (
	"errors"
	"fmt"
	"net/http"
)

// Code 是稳定错误码（docs/开发规格.md §1.9）。客户端按 code 分支，不按 message。
type Code string

const (
	CodeAuthRequired          Code = "AUTH_REQUIRED"
	CodeAccessDenied          Code = "ACCESS_DENIED"
	CodeNotFound              Code = "NOT_FOUND"
	CodePreconditionRequired  Code = "PRECONDITION_REQUIRED"
	CodeRevisionMismatch      Code = "REVISION_MISMATCH"
	CodeIdempotencyConflict   Code = "IDEMPOTENCY_CONFLICT"
	CodeStateConflict         Code = "STATE_CONFLICT"
	CodeValidationFailed      Code = "VALIDATION_FAILED"
	CodeSecretDetected        Code = "SECRET_DETECTED"
	CodePayloadTooLarge       Code = "PAYLOAD_TOO_LARGE"
	CodeCursorExpired         Code = "CURSOR_EXPIRED"
	CodeEventWindowExpired    Code = "EVENT_WINDOW_EXPIRED"
	CodeRateLimited           Code = "RATE_LIMITED"
	CodeIdentityUnavailable   Code = "IDENTITY_UNAVAILABLE"
	CodeClientUpgradeRequired Code = "CLIENT_UPGRADE_REQUIRED"
	// CodeAuthorizationPending 是设备授权流的等待信号（RFC 8628）。
	CodeAuthorizationPending Code = "AUTHORIZATION_PENDING"
	CodeInternal             Code = "INTERNAL"
)

// Error 是可直接映射为 HTTP 响应的错误。
type Error struct {
	Status  int
	Code    Code
	Message string
	// Details 是可选的结构化补充，如冲突的资源键。不放栈、不放路径、不放密钥。
	Details any
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// New 构造一个业务错误。
func New(status int, code Code, msg string) *Error {
	return &Error{Status: status, Code: code, Message: msg}
}

// WithDetails 附加结构化细节，返回副本。
func (e *Error) WithDetails(d any) *Error {
	cp := *e
	cp.Details = d
	return &cp
}

// 常用错误。message 面向调用方，故意不说明账号是否存在等敏感事实。
var (
	ErrAuthRequired = New(http.StatusUnauthorized, CodeAuthRequired, "需要登录")
	ErrAccessDenied = New(http.StatusForbidden, CodeAccessDenied, "无权访问")
	ErrNotFound     = New(http.StatusNotFound, CodeNotFound, "对象不存在")
	ErrRateLimited  = New(http.StatusTooManyRequests, CodeRateLimited, "请求过于频繁，请稍后再试")
)

// Validation 构造 422。
func Validation(msg string) *Error {
	return New(http.StatusUnprocessableEntity, CodeValidationFailed, msg)
}

// Validationf 是带格式化的 Validation。
func Validationf(format string, args ...any) *Error {
	return Validation(fmt.Sprintf(format, args...))
}

// StateConflict 构造 409：对象当前状态不允许该操作。
func StateConflict(msg string) *Error {
	return New(http.StatusConflict, CodeStateConflict, msg)
}

// AsError 把任意 error 转成 *Error；不是 *Error 的一律视为 500，并隐藏细节。
func AsError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return New(http.StatusInternalServerError, CodeInternal, "服务内部错误")
}
