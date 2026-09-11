package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// RandomToken 生成一个高熵的不透明令牌（32 字节）。
// 用于 device_code、refresh token 与接入码——它们都不承载信息，只作凭据。
func RandomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成随机令牌: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken 返回令牌的 SHA-256 十六进制摘要。
//
// 这里用 SHA-256 而非 Argon2 是有意的：不透明令牌本身是 256 位随机数，
// 不存在字典攻击面，慢哈希只会拖慢每次 refresh。密码走 Argon2id（password.go）。
func HashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// EqualHash 以恒定时间比较两个摘要。
func EqualHash(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// userCodeAlphabet 刻意剔除易混字符：0/O、1/I/L、U/V。
// 用户要在浏览器里手敲这串码，认错一个字符就是一次失败的入职。
const userCodeAlphabet = "ABCDEFGHJKMNPQRSTWXYZ23456789"

// NewUserCode 生成形如 WXYZ-2345 的短码，供用户在 Web 端输入。
func NewUserCode() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成用户码: %w", err)
	}
	var sb strings.Builder
	for i, x := range b {
		if i == 4 {
			sb.WriteByte('-')
		}
		sb.WriteByte(userCodeAlphabet[int(x)%len(userCodeAlphabet)])
	}
	return sb.String(), nil
}

// NormalizeUserCode 统一大小写并补上分隔符，容忍用户输入 "wxyz2345"。
func NormalizeUserCode(in string) string {
	s := strings.ToUpper(strings.TrimSpace(in))
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, " ", "")
	if len(s) != 8 {
		return s
	}
	return s[:4] + "-" + s[4:]
}
