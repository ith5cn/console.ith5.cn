package identity

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDCConfig 是某组织配置的 IdP。
type OIDCConfig struct {
	ID           string
	OrgID        string
	Issuer       string
	ClientID     string
	ClientSecret string
	Scopes       []string
	Enabled      bool
}

// GroupMapping 把 IdP 用户组映射到组织 / 团队 / 项目的角色。
type GroupMapping struct {
	ID         string
	ProviderID string
	IdPGroup   string
	// Target: org | team | project
	Target   string
	TargetID string
	Role     string
	Priority int
}

// OIDCStore 是 OIDC 适配器需要的持久化能力：读配置、按身份建档、按用户组落角色。
type OIDCStore interface {
	GetOIDCConfig(ctx context.Context, orgID string) (OIDCConfig, error)
	PutOIDCConfig(ctx context.Context, c OIDCConfig) (string, error)
	ListGroupMappings(ctx context.Context, orgID string) ([]GroupMapping, error)
	PutGroupMappings(ctx context.Context, orgID, providerID string, ms []GroupMapping) error
	// ProvisionOIDCIdentity 创建或更新账号（按 issuer+subject），并按映射结果落成员记录与角色。
	// 返回该组织内的成员记录；没有任何映射命中且成员不存在时返回 ErrNotFound。
	ProvisionOIDCIdentity(ctx context.Context, orgID string, id Identity, orgRole Role, teamRoles, projectRoles map[string]string) (Membership, error)
}

// ErrOIDCNotConfigured 表示该组织没有启用的 IdP。
var ErrOIDCNotConfigured = errors.New("identity: 该组织未配置 OIDC")

// ErrOIDCNoMapping 表示登录成功但没有任何用户组映射到本组织，且此前不是成员。
var ErrOIDCNoMapping = errors.New("identity: 你的账号未被授权进入该组织")

// OIDC 是通用 OIDC 适配器：授权码 + PKCE，只在浏览器端使用；CLI 走设备授权流。
//
// 原始 ID token 只在 CompleteBrowser 内解析一次，业务只拿到 Identity。
type OIDC struct {
	store       OIDCStore
	callbackURL string
	now         func() time.Time

	mu     sync.Mutex
	states map[string]oidcState // state -> 待完成的授权
}

type oidcState struct {
	orgID     string
	verifier  string
	nonce     string
	expiresAt time.Time
}

const oidcStateTTL = 10 * time.Minute

// NewOIDC 构造适配器。callbackURL 形如 https://console/v1/auth/oidc/callback。
func NewOIDC(store OIDCStore, callbackURL string) *OIDC {
	return &OIDC{store: store, callbackURL: callbackURL, now: time.Now, states: map[string]oidcState{}}
}

// Kind 实现 Provider。
func (o *OIDC) Kind() string { return "oidc" }

// VerifyPassword 实现 Provider：OIDC 不支持密码。
func (o *OIDC) VerifyPassword(context.Context, string, string) (Identity, error) {
	return Identity{}, ErrUnsupported
}

// BeginBrowser 生成跳转到 IdP 的地址。state 与 PKCE verifier 暂存在内存里，十分钟内有效。
func (o *OIDC) BeginBrowser(ctx context.Context, orgID string) (redirectURL string, err error) {
	cfg, err := o.store.GetOIDCConfig(ctx, orgID)
	if err != nil || !cfg.Enabled {
		return "", ErrOIDCNotConfigured
	}
	provider, err := gooidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return "", fmt.Errorf("%w: 无法访问 IdP", ErrIdentityUnavailable)
	}
	state, err := RandomToken()
	if err != nil {
		return "", err
	}
	verifier := oauth2.GenerateVerifier()
	nonce, err := RandomToken()
	if err != nil {
		return "", err
	}
	o.gc()
	o.mu.Lock()
	o.states[state] = oidcState{orgID: orgID, verifier: verifier, nonce: nonce, expiresAt: o.now().Add(oidcStateTTL)}
	o.mu.Unlock()

	oc := o.oauthConfig(cfg, provider)
	return oc.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), gooidc.Nonce(nonce)), nil
}

// CompleteBrowser 处理回调：换 token、验 ID token、按用户组映射建档，返回该组织的成员记录。
func (o *OIDC) CompleteBrowser(ctx context.Context, state, code string) (Membership, error) {
	o.mu.Lock()
	st, ok := o.states[state]
	delete(o.states, state)
	o.mu.Unlock()
	if !ok || !st.expiresAt.After(o.now()) {
		return Membership{}, errors.New("identity: 登录状态无效或已过期，请重新发起")
	}
	cfg, err := o.store.GetOIDCConfig(ctx, st.orgID)
	if err != nil || !cfg.Enabled {
		return Membership{}, ErrOIDCNotConfigured
	}
	provider, err := gooidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return Membership{}, fmt.Errorf("%w: 无法访问 IdP", ErrIdentityUnavailable)
	}
	oc := o.oauthConfig(cfg, provider)
	tok, err := oc.Exchange(ctx, code, oauth2.VerifierOption(st.verifier))
	if err != nil {
		return Membership{}, fmt.Errorf("%w: 换取令牌失败", ErrIdentityUnavailable)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok {
		return Membership{}, errors.New("identity: IdP 未返回 id_token")
	}
	idTok, err := provider.Verifier(&gooidc.Config{ClientID: cfg.ClientID}).Verify(ctx, raw)
	if err != nil {
		return Membership{}, fmt.Errorf("identity: id_token 校验失败: %w", err)
	}
	if idTok.Nonce != st.nonce {
		return Membership{}, errors.New("identity: nonce 不匹配")
	}
	var claims struct {
		Email  string   `json:"email"`
		Name   string   `json:"name"`
		Groups []string `json:"groups"`
	}
	if err := idTok.Claims(&claims); err != nil {
		return Membership{}, fmt.Errorf("identity: 读取 claims: %w", err)
	}
	id := Identity{Issuer: idTok.Issuer, Subject: idTok.Subject, Email: NormalizeEmail(claims.Email), Name: claims.Name, Groups: claims.Groups}

	mappings, err := o.store.ListGroupMappings(ctx, st.orgID)
	if err != nil {
		return Membership{}, err
	}
	orgRole, teamRoles, projectRoles := ResolveGroupMappings(mappings, id.Groups)
	m, err := o.store.ProvisionOIDCIdentity(ctx, st.orgID, id, orgRole, teamRoles, projectRoles)
	if errors.Is(err, ErrNotFound) {
		return Membership{}, ErrOIDCNoMapping
	}
	return m, err
}

// ResolveGroupMappings 把用户组按映射表折算成角色。
//
// 组织角色取优先级最高的一条；同优先级取更高的角色。团队与项目角色同理，按目标分别折算。
// 没有任何组织级映射命中时返回空角色，由仓储决定是保留既有角色还是拒绝建档。
func ResolveGroupMappings(mappings []GroupMapping, groups []string) (orgRole Role, teamRoles, projectRoles map[string]string) {
	in := make(map[string]bool, len(groups))
	for _, g := range groups {
		in[g] = true
	}
	teamRoles, projectRoles = map[string]string{}, map[string]string{}
	bestPrio := map[string]int{}
	for _, m := range mappings {
		if !in[m.IdPGroup] {
			continue
		}
		key := m.Target + ":" + m.TargetID
		prev, seen := bestPrio[key]
		switch {
		case !seen || m.Priority > prev:
			bestPrio[key] = m.Priority
		case m.Priority < prev:
			continue
		}
		switch m.Target {
		case "org":
			if !seen || m.Priority > prev || roleRank(Role(m.Role)) > roleRank(orgRole) {
				orgRole = Role(m.Role)
			}
		case "team":
			if !seen || m.Priority > prev || m.Role == "admin" {
				teamRoles[m.TargetID] = m.Role
			}
		case "project":
			if !seen || m.Priority > prev || m.Role == "admin" {
				projectRoles[m.TargetID] = m.Role
			}
		}
	}
	return orgRole, teamRoles, projectRoles
}

func roleRank(r Role) int {
	switch r {
	case RoleOwner:
		return 4
	case RoleAdmin:
		return 3
	case RoleMember:
		return 2
	case RoleViewer:
		return 1
	}
	return 0
}

func (o *OIDC) oauthConfig(cfg OIDCConfig, p *gooidc.Provider) *oauth2.Config {
	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = []string{gooidc.ScopeOpenID, "profile", "email"}
	}
	return &oauth2.Config{
		ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret,
		Endpoint: p.Endpoint(), RedirectURL: o.callbackURL, Scopes: scopes,
	}
}

func (o *OIDC) gc() {
	o.mu.Lock()
	defer o.mu.Unlock()
	now := o.now()
	for k, v := range o.states {
		if !v.expiresAt.After(now) {
			delete(o.states, k)
		}
	}
}

// ErrIdentityUnavailable 表示 IdP 不可达；此时新登录、刷新与写操作全部拒绝。
var ErrIdentityUnavailable = errors.New("identity: 身份服务不可用")
