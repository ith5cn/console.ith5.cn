package identity

import (
	"strings"
	"testing"
	"time"
)

const secret = "0123456789abcdef0123456789abcdef"

func member() Membership {
	return Membership{UserID: "u1", AccountID: "a1", OrgID: "o1", Role: RoleMember}
}

func TestSigner_RequiresStrongSecret(t *testing.T) {
	if _, err := NewSigner("short"); err == nil {
		t.Fatal("过短的密钥必须拒绝")
	}
	if _, err := NewSigner(secret); err != nil {
		t.Fatal(err)
	}
}

func TestToken_RoundTrip(t *testing.T) {
	s, _ := NewSigner(secret)
	tok, err := s.Issue(member(), "m1", TokenKindDevice, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Verify(tok, TokenKindDevice)
	if err != nil {
		t.Fatal(err)
	}
	if c.UserID() != "u1" || c.OrgID != "o1" || c.MachineID != "m1" || c.Role != RoleMember {
		t.Fatalf("声明丢失: %+v", c)
	}
}

// 设备令牌不得当会话令牌用，反之亦然。
func TestToken_KindIsEnforced(t *testing.T) {
	s, _ := NewSigner(secret)
	tok, _ := s.Issue(member(), "m1", TokenKindDevice, time.Now())
	if _, err := s.Verify(tok, TokenKindSession); err == nil {
		t.Fatal("设备令牌不得通过会话校验")
	}
}

func TestToken_LoginKindCarriesAccount(t *testing.T) {
	s, _ := NewSigner(secret)
	tok, err := s.IssueLogin("acct-1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Verify(tok, TokenKindLogin)
	if err != nil {
		t.Fatal(err)
	}
	if c.Subject != "acct-1" || c.OrgID != "" {
		t.Fatalf("登录凭据只应携带账号 id: %+v", c)
	}
	if _, err := s.Verify(tok, TokenKindSession); err == nil {
		t.Fatal("登录凭据不得当会话令牌用")
	}
}

func TestToken_ExpiredAndWrongSecretRejected(t *testing.T) {
	s1, _ := NewSigner(secret)
	s2, _ := NewSigner("ffffffffffffffffffffffffffffffff")
	expired, _ := s1.Issue(member(), "", TokenKindSession, time.Now().Add(-2*AccessTokenTTL))
	if _, err := s1.Verify(expired, TokenKindSession); err == nil {
		t.Fatal("过期令牌必须拒绝")
	}
	tok, _ := s1.Issue(member(), "", TokenKindSession, time.Now())
	if _, err := s2.Verify(tok, TokenKindSession); err == nil {
		t.Fatal("换密钥后必须验签失败")
	}
}

// alg=none 是 JWT 的经典攻击面，必须被 WithValidMethods 挡住。
func TestToken_RejectsAlgNone(t *testing.T) {
	s, _ := NewSigner(secret)
	const forged = "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0." +
		"eyJzdWIiOiJ1MSIsIm9yZyI6Im8xIiwia2luZCI6InNlc3Npb24ifQ."
	if _, err := s.Verify(forged, TokenKindSession); err == nil {
		t.Fatal("alg=none 必须拒绝")
	}
}

func TestPassword(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := VerifyPassword("correct horse battery staple", h); err != nil || !ok {
		t.Fatal("正确密码应通过")
	}
	if ok, _ := VerifyPassword("wrong", h); ok {
		t.Fatal("错误密码必须失败")
	}
	if _, err := VerifyPassword("x", "not-a-hash"); err == nil {
		t.Fatal("非法哈希格式必须报错")
	}
	a, _ := HashPassword("same")
	b, _ := HashPassword("same")
	if a == b {
		t.Fatal("相同密码必须产生不同哈希（盐要随机）")
	}
}

func TestDummyHashIsWellFormed(t *testing.T) {
	// 账号不存在时用它做等时比较，它必须是合法格式，否则 VerifyPassword 会提前返回、时间泄露。
	if _, err := VerifyPassword("anything", dummyHash); err != nil {
		t.Fatalf("dummyHash 格式非法: %v", err)
	}
}

func TestRandomToken_Unique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		tok, err := RandomToken()
		if err != nil || seen[tok] {
			t.Fatal("令牌重复或出错")
		}
		seen[tok] = true
	}
}

func TestUserCode(t *testing.T) {
	code, err := NewUserCode()
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 9 || code[4] != '-' {
		t.Fatalf("格式应为 XXXX-XXXX: %q", code)
	}
	for _, bad := range []string{"0", "O", "1", "I", "L", "U", "V"} {
		if strings.Contains(code, bad) {
			t.Fatalf("不应包含易混字符 %q: %s", bad, code)
		}
	}
	for _, in := range []string{"wxyz2345", "WXYZ-2345", " wxyz-2345 ", "WXYZ 2345"} {
		if got := NormalizeUserCode(in); got != "WXYZ-2345" {
			t.Fatalf("%q → %q", in, got)
		}
	}
}

func TestHashToken(t *testing.T) {
	if HashToken("a") == HashToken("b") || !EqualHash(HashToken("a"), HashToken("a")) {
		t.Fatal("摘要行为错误")
	}
}
