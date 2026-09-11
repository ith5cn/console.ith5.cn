package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/ith5/ith5/internal/platform/httpx"
)

// ---------------------------------------------------------------
// 分页
// ---------------------------------------------------------------

// 列表接口统一接受 limit 与 cursor；cursor 是不透明串，客户端原样带回。
// 默认 100 条、最多 500 条。大多数列表规模很小（一个组织几十个团队、几百个成员），
// 用偏移量做游标已经够用，也让所有列表的契约一致。审计事件量大，单独用时间游标。
const (
	defaultPageLimit = 100
	maxPageLimit     = 500
)

type pageParams struct {
	limit  int
	offset int
}

func parsePage(r *http.Request) pageParams {
	q := r.URL.Query()
	p := pageParams{limit: defaultPageLimit}
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 {
		p.limit = min(n, maxPageLimit)
	}
	if c := q.Get("cursor"); c != "" {
		if raw, err := base64.RawURLEncoding.DecodeString(c); err == nil && strings.HasPrefix(string(raw), "o:") {
			if n, err := strconv.Atoi(strings.TrimPrefix(string(raw), "o:")); err == nil && n >= 0 {
				p.offset = n
			}
		}
	}
	return p
}

// dbLimit 是交给仓储层的取数上限：多取一页之前的，再在内存里切。
func (p pageParams) dbLimit() int { return p.offset + p.limit + 1 }

// pageSlice 按分页参数切片，并在还有更多时给出下一页游标。
func pageSlice[T any](items []T, p pageParams) Page[T] {
	if items == nil {
		items = []T{}
	}
	if p.offset >= len(items) {
		return Page[T]{Items: []T{}}
	}
	end := min(p.offset+p.limit, len(items))
	out := Page[T]{Items: items[p.offset:end]}
	if end < len(items) {
		out.NextCursor = base64.RawURLEncoding.EncodeToString([]byte("o:" + strconv.Itoa(end)))
	}
	return out
}

// ---------------------------------------------------------------
// 乐观锁
// ---------------------------------------------------------------

// etagOf 对响应体的 JSON 取哈希作为弱 ETag。
//
// 变更集与绑定有自己的 ETag 列；其余可写对象（组织、策略、团队、项目、身份源、标签、审核人）
// 没有版本列，用内容哈希就够：内容没变，ETag 就不变。
func etagOf(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// checkIfMatch 处理可选的 If-Match：没带就放行；带了但与当前内容不符返回 412。
//
// 变更集与绑定的编辑强制要求 If-Match（并发编辑代价高）；其他管理对象是可选的，
// 让谨慎的客户端能避免覆盖别人的改动，同时不逼着每个脚本都先 GET 一次。
func (s *Server) checkIfMatch(w http.ResponseWriter, r *http.Request, current any) bool {
	want := unquote(r.Header.Get("If-Match"))
	if want == "" || want == "*" {
		return true
	}
	if want != etagOf(current) {
		httpx.WriteError(w, r, s.log, httpx.New(http.StatusPreconditionFailed, httpx.CodeRevisionMismatch, "对象已被修改，请重新读取后再提交"))
		return false
	}
	return true
}

func setETag(w http.ResponseWriter, v any) { w.Header().Set("ETag", quote(etagOf(v))) }
