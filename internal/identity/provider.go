package identity

import (
	"context"
	"errors"
	"strings"
)

// Provider 是身份适配器。它把某种登录方式验证成标准化的 Identity；
// 原始 IdP 凭据只停留在适配器内部，业务模块只看到 Identity。
//
// 一期实现 local（密码）与通用 OIDC；企业私有协议按此接口另写适配器。
type Provider interface {
	// Kind 是适配器名，也是 accounts.issuer 的取值来源：local 固定为 "local"，
	// OIDC 为 issuer URL。
	Kind() string
	// VerifyPassword 只有 local 实现；其他适配器返回 ErrUnsupported。
	VerifyPassword(ctx context.Context, email, password string) (Identity, error)
}

// ErrUnsupported 表示该适配器不支持这种登录方式。
var ErrUnsupported = errors.New("identity: 该登录方式不支持")

// IssuerLocal 是密码账号的 issuer。
const IssuerLocal = "local"

// LocalProvider 用本地密码表验证身份。
type LocalProvider struct {
	store Store
}

// NewLocalProvider 构造密码适配器。
func NewLocalProvider(store Store) *LocalProvider { return &LocalProvider{store: store} }

func (p *LocalProvider) Kind() string { return IssuerLocal }

// VerifyPassword 校验邮箱与密码。
//
// 任何失败都返回 ErrBadCredentials，不区分「账号不存在」与「密码错误」；
// 且即使账号不存在也走一次哈希比较，避免响应时间泄露账号是否存在。
func (p *LocalProvider) VerifyPassword(ctx context.Context, email, password string) (Identity, error) {
	email = NormalizeEmail(email)
	acct, hash, err := p.store.FindLocalAccount(ctx, email)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Identity{}, err
	}
	if hash == "" {
		hash = dummyHash
	}
	ok, verr := VerifyPassword(password, hash)
	if err != nil || verr != nil || !ok {
		return Identity{}, ErrBadCredentials
	}
	if acct.DisabledAt != nil {
		return Identity{}, ErrDisabled
	}
	return Identity{Issuer: IssuerLocal, Subject: acct.Subject, Email: acct.Email, Name: acct.Name}, nil
}

// dummyHash 是一个合法但无人匹配的 Argon2id 哈希，用于账号不存在时的等时比较。
const dummyHash = "$argon2id$v=19$m=65536,t=2,p=4$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

// NormalizeEmail 统一邮箱形式：去空白、转小写。
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
