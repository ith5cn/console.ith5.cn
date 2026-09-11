package httpx

import (
	"errors"
	"net"
	"net/http"
	"strings"
)

// Bearer 取出 Authorization 头里的令牌；没有则返回空串。
func Bearer(r *http.Request) string {
	if after, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	return ""
}

// ClientIP 返回去掉端口的远端地址。RealIP 中间件已把代理头折算进 RemoteAddr。
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// asErr 是 errors.As 的薄封装，避免在 decode 里引入多余的类型断言噪音。
func asErr(err error, target any) bool { return errors.As(err, target) }
