package identity

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// TokenKind 区分令牌的来源面。
//
// 设备令牌来自设备授权流，带 machine_id，供 CLI 同步与上报；
// 会话令牌来自浏览器登录，供管理后台。一个被偷到的设备令牌打不开管理后台。
type TokenKind string

const (
	TokenKindDevice  TokenKind = "device"
	TokenKindSession TokenKind = "session"
	// TokenKindLogin 是密码登录后、选组织前的短期凭据，Subject 是账号 id 而非成员 id。
	TokenKindLogin TokenKind = "login"
)

// LoginTokenTTL 是选组织的窗口。
const LoginTokenTTL = 5 * time.Minute

const (
	// AccessTokenTTL 短，配合 refresh 轮换；IdP 不可用时它就是离线租约的长度。
	AccessTokenTTL  = 15 * time.Minute
	RefreshTokenTTL = 30 * 24 * time.Hour
	DeviceCodeTTL   = 10 * time.Minute
	// DevicePollInterval 是 CLI 轮询设备码的建议间隔（秒）。
	DevicePollInterval = 5
)

var (
	ErrInvalidToken = errors.New("identity: 令牌无效")
	ErrWrongKind    = errors.New("identity: 令牌类型不匹配")
)

// Claims 是访问令牌承载的信息。Subject 是成员记录 id（users.id）。
type Claims struct {
	OrgID     string    `json:"org"`
	Role      Role      `json:"role"`
	MachineID string    `json:"mid,omitempty"`
	Kind      TokenKind `json:"kind"`
	jwt.RegisteredClaims
}

// UserID 返回成员记录 id。
func (c Claims) UserID() string { return c.Subject }

// Signer 签发与校验访问令牌。
type Signer struct {
	secret []byte
	// now 用于校验过期；生产是 time.Now，测试注入固定时钟，让签发与校验用同一个时间源。
	now func() time.Time
}

// NewSigner 要求至少 32 字节密钥。
func NewSigner(secret string) (*Signer, error) {
	if len(secret) < 32 {
		return nil, fmt.Errorf("JWT 密钥至少需要 32 字节，当前 %d", len(secret))
	}
	return &Signer{secret: []byte(secret), now: time.Now}, nil
}

// WithClock 替换校验用的时钟。
func (s *Signer) WithClock(now func() time.Time) *Signer {
	s.now = now
	return s
}

// Issue 签发一个访问令牌。machineID 只在设备令牌下有意义。
func (s *Signer) Issue(m Membership, machineID string, kind TokenKind, now time.Time) (string, error) {
	c := Claims{
		OrgID:     m.OrgID,
		Role:      m.Role,
		MachineID: machineID,
		Kind:      kind,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   m.UserID,
			ID:        uuid.NewString(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(AccessTokenTTL)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(s.secret)
}

// IssueLogin 签发登录凭据：只证明「你是这个账号」，尚未进入任何组织。
func (s *Signer) IssueLogin(accountID string, now time.Time) (string, error) {
	c := Claims{
		Kind: TokenKindLogin,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   accountID,
			ID:        uuid.NewString(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(LoginTokenTTL)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(s.secret)
}

// Verify 校验令牌并确认类型。
//
// 这里只验签与过期。成员是否仍然 active 必须在业务层实时查库：
// 尚未过期的令牌不能让一个已停用的账号继续访问。
func (s *Signer) Verify(tokenStr string, want TokenKind) (*Claims, error) {
	var c Claims
	_, err := jwt.ParseWithClaims(tokenStr, &c, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("非预期的签名算法: %v", t.Header["alg"])
		}
		return s.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithTimeFunc(s.now))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if c.Kind != want {
		return nil, fmt.Errorf("%w: 需要 %s，实际 %s", ErrWrongKind, want, c.Kind)
	}
	if c.Subject == "" || (c.Kind != TokenKindLogin && c.OrgID == "") {
		return nil, fmt.Errorf("%w: 缺少必要声明", ErrInvalidToken)
	}
	return &c, nil
}
