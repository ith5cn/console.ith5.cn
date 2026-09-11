package api

import (
	"errors"
	"net/http"

	"github.com/ith5/ith5/internal/audit"
	"github.com/ith5/ith5/internal/changesets"
	"github.com/ith5/ith5/internal/identity"
	"github.com/ith5/ith5/internal/organizations"
	"github.com/ith5/ith5/internal/platform/httpx"
	"github.com/ith5/ith5/internal/projects"
	"github.com/ith5/ith5/internal/releases"
	"github.com/ith5/ith5/internal/resources"
)

// mapErr 把各领域的哨兵错误翻译成稳定错误码。
//
// 密码错误与账号不存在共用一条文案，不泄露账号是否存在。
// 未识别的错误原样返回，由 WriteError 视为 500 并隐藏细节。
func mapErr(err error) error {
	if mapped, ok := mapKnowledgeErr(err); ok {
		return mapped
	}
	// 密钥命中对所有 kind 一视同仁：422 + 命中位置，不回传内容
	var secret *resources.SecretError
	if errors.As(err, &secret) {
		return httpx.New(http.StatusUnprocessableEntity, httpx.CodeSecretDetected, "内容疑似包含密钥，请移除后重试").WithDetails(secret.Hits)
	}
	switch {
	// identity
	case errors.Is(err, identity.ErrBadCredentials), errors.Is(err, identity.ErrDisabled):
		return httpx.New(http.StatusUnauthorized, httpx.CodeAuthRequired, "账号或密码不正确")
	case errors.Is(err, identity.ErrSuspended):
		return httpx.New(http.StatusForbidden, httpx.CodeAccessDenied, "账号已停用")
	case errors.Is(err, identity.ErrAuthorizationPending):
		return httpx.New(http.StatusPreconditionRequired, httpx.CodeAuthorizationPending, "等待用户在浏览器中批准")
	case errors.Is(err, identity.ErrCodeExpired):
		return httpx.New(http.StatusGone, httpx.CodeCursorExpired, "设备码不存在或已失效，请重新登录")
	case errors.Is(err, identity.ErrRefreshReused), errors.Is(err, identity.ErrInvalidGrant):
		return httpx.New(http.StatusUnauthorized, httpx.CodeAuthRequired, "刷新凭据无效，请重新登录")
	case errors.Is(err, identity.ErrMachineRevoked):
		return httpx.New(http.StatusUnauthorized, httpx.CodeAuthRequired, "设备已撤销")
	case errors.Is(err, identity.ErrEnrollmentUnusable):
		return httpx.Validation("接入码无效、已过期或已用尽")
	case errors.Is(err, identity.ErrOIDCNotConfigured):
		return httpx.Validation("该组织未配置单点登录")
	case errors.Is(err, identity.ErrOIDCNoMapping):
		return httpx.New(http.StatusForbidden, httpx.CodeAccessDenied, "你的账号未被授权进入该组织")
	case errors.Is(err, identity.ErrIdentityUnavailable):
		return httpx.New(http.StatusServiceUnavailable, httpx.CodeIdentityUnavailable, "身份服务暂时不可用")
	case errors.Is(err, identity.ErrNotFound):
		return httpx.ErrNotFound

	// organizations
	case errors.Is(err, organizations.ErrForbidden), errors.Is(err, projects.ErrForbidden):
		return httpx.ErrAccessDenied
	case errors.Is(err, organizations.ErrNotFound), errors.Is(err, projects.ErrNotFound):
		return httpx.ErrNotFound
	case errors.Is(err, organizations.ErrSlugTaken), errors.Is(err, projects.ErrSlugTaken):
		return httpx.StateConflict("slug 已被占用")
	case errors.Is(err, organizations.ErrEmailTaken):
		return httpx.StateConflict("该邮箱已是本组织成员")
	case errors.Is(err, organizations.ErrLastOwner):
		return httpx.StateConflict("不能停用或降级最后一个 owner")
	case errors.Is(err, projects.ErrArchived):
		return httpx.StateConflict("项目已归档")
	case errors.Is(err, projects.ErrETagMismatch):
		return httpx.New(http.StatusPreconditionFailed, httpx.CodeRevisionMismatch, "对象已被修改，请重新获取后再试")
	case errors.Is(err, organizations.ErrInvalidInput), errors.Is(err, projects.ErrInvalidInput),
		errors.Is(err, organizations.ErrReviewerLevel):
		return httpx.Validation(trimPrefix(err.Error()))
	// changesets / releases / resources
	case errors.Is(err, changesets.ErrForbidden), errors.Is(err, releases.ErrForbidden):
		return httpx.ErrAccessDenied
	case errors.Is(err, changesets.ErrNotFound), errors.Is(err, releases.ErrNotFound), errors.Is(err, resources.ErrNotFound):
		return httpx.ErrNotFound
	case errors.Is(err, changesets.ErrState), errors.Is(err, releases.ErrState):
		return httpx.StateConflict("当前状态不允许该操作")
	case errors.Is(err, changesets.ErrSelfReview):
		return httpx.New(http.StatusForbidden, httpx.CodeAccessDenied, "不能审核自己的变更")
	case errors.Is(err, changesets.ErrDigest):
		return httpx.New(http.StatusPreconditionFailed, httpx.CodeRevisionMismatch, "内容已变化，请重新查看后再审核")
	case errors.Is(err, changesets.ErrETagMismatch):
		return httpx.New(http.StatusPreconditionFailed, httpx.CodeRevisionMismatch, "对象已被修改，请重新获取后再试")
	case errors.Is(err, changesets.ErrMissingBlob), errors.Is(err, changesets.ErrScopeNotFound), errors.Is(err, changesets.ErrInvalidInput):
		return httpx.Validation(trimPrefix(err.Error()))
	case errors.Is(err, audit.ErrBadCursor):
		return httpx.New(http.StatusGone, httpx.CodeCursorExpired, "分页游标无效")
	}
	return err
}

// trimPrefix 去掉 "organizations: " 这类包名前缀，留给用户看的部分。
func trimPrefix(s string) string {
	for _, p := range []string{"organizations: ", "projects: ", "identity: ", "changesets: ", "releases: ", "knowledge: ", "telemetry: "} {
		if len(s) > len(p) && s[:len(p)] == p {
			return s[len(p):]
		}
	}
	return s
}
