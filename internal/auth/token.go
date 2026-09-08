package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Purpose 区分令牌的使用面。
//
// Web 令牌不得调用分发接口，CLI 令牌不得调用管理接口（技术方案 §6.2）。
// 一个被偷到的 CLI 令牌因此打不开管理后台。
type Purpose string

const (
	PurposeCLI Purpose = "cli"
	PurposeWeb Purpose = "web"
)

const (
	AccessTokenTTL  = 24 * time.Hour
	RefreshTokenTTL = 30 * 24 * time.Hour
	DeviceCodeTTL   = 10 * time.Minute
)

var (
	ErrInvalidToken = errors.New("令牌无效")
	ErrWrongPurpose = errors.New("令牌用途不匹配")
)

// Claims 是访问令牌承载的信息。
type Claims struct {
	OrgID     string  `json:"org"`
	Role      string  `json:"role"`
	MachineID string  `json:"mid,omitempty"` // 仅 CLI 令牌有
	Purpose   Purpose `json:"pur"`
	jwt.RegisteredClaims
}

func (c Claims) UserID() string { return c.Subject }

// Signer 签发与校验访问令牌。
type Signer struct {
	secret []byte
}

func NewSigner(secret string) (*Signer, error) {
	if len(secret) < 32 {
		return nil, fmt.Errorf("JWT 密钥至少需要 32 字节，当前 %d", len(secret))
	}
	return &Signer{secret: []byte(secret)}, nil
}

// Issue 签发一个访问令牌。machineID 只在 CLI 用途下有意义。
func (s *Signer) Issue(userID, orgID, role, machineID string, purpose Purpose, now time.Time) (string, error) {
	c := Claims{
		OrgID:     orgID,
		Role:      role,
		MachineID: machineID,
		Purpose:   purpose,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			ID:        uuid.NewString(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(AccessTokenTTL)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(s.secret)
}

// Verify 校验令牌并确认用途。
//
// 注意：这里只验签与过期。用户是否仍然 active 必须在业务层实时查库
// —— 尚未过期的令牌不能让一个已停用的账号继续访问（D4，技术方案 §6.2）。
func (s *Signer) Verify(tokenStr string, want Purpose) (*Claims, error) {
	var c Claims
	_, err := jwt.ParseWithClaims(tokenStr, &c, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("非预期的签名算法: %v", t.Header["alg"])
		}
		return s.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if c.Purpose != want {
		return nil, fmt.Errorf("%w: 需要 %s，实际 %s", ErrWrongPurpose, want, c.Purpose)
	}
	if c.Subject == "" || c.OrgID == "" {
		return nil, fmt.Errorf("%w: 缺少必要声明", ErrInvalidToken)
	}
	return &c, nil
}
