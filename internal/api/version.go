package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/ith5/ith5/internal/platform/httpx"
)

// clientVersionGate 拒绝低于 MinClientVersion 的客户端。
//
// 只看 X-Client-Version 头；没带就放行。版本号按点分整数比较，非数字段视为 0。
func clientVersionGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := strings.TrimSpace(r.Header.Get("X-Client-Version")); v != "" && compareVersions(v, MinClientVersion) < 0 {
			w.Header().Set("X-Min-Client-Version", MinClientVersion)
			httpx.WriteError(w, r, nil, httpx.New(http.StatusUpgradeRequired, httpx.CodeClientUpgradeRequired,
				"客户端版本过低，至少需要 "+MinClientVersion))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// compareVersions 比较两个点分版本号：a<b 返回 -1，相等 0，a>b 返回 1。前导 v 与预发布后缀忽略。
func compareVersions(a, b string) int {
	pa, pb := versionParts(a), versionParts(b)
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func versionParts(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out []int
	for _, p := range strings.Split(v, ".") {
		n, _ := strconv.Atoi(p)
		out = append(out, n)
	}
	return out
}
