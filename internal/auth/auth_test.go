package auth

import (
	"strings"
	"testing"
	"time"
)

const secret = "0123456789abcdef0123456789abcdef"

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
	now := time.Now()
	tok, err := s.Issue("u1", "o1", "member", "m1", PurposeCLI, now)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Verify(tok, PurposeCLI)
	if err != nil {
		t.Fatal(err)
	}
	if c.UserID() != "u1" || c.OrgID != "o1" || c.MachineID != "m1" || c.Role != "member" {
		t.Fatalf("声明丢失: %+v", c)
	}
}

// Web 令牌不得调用分发接口，CLI 令牌不得调用管理接口。
func TestToken_PurposeIsEnforced(t *testing.T) {
	s, _ := NewSigner(secret)
	tok, _ := s.Issue("u1", "o1", "member", "m1", PurposeCLI, time.Now())
	if _, err := s.Verify(tok, PurposeWeb); err == nil {
		t.Fatal("CLI 令牌不得通过 Web 用途校验")
	}
}

func TestToken_ExpiredRejected(t *testing.T) {
	s, _ := NewSigner(secret)
	tok, _ := s.Issue("u1", "o1", "member", "m1", PurposeCLI, time.Now().Add(-2*AccessTokenTTL))
	if _, err := s.Verify(tok, PurposeCLI); err == nil {
		t.Fatal("过期令牌必须拒绝")
	}
}

func TestToken_WrongSecretRejected(t *testing.T) {
	s1, _ := NewSigner(secret)
	s2, _ := NewSigner("ffffffffffffffffffffffffffffffff")
	tok, _ := s1.Issue("u1", "o1", "member", "m1", PurposeCLI, time.Now())
	if _, err := s2.Verify(tok, PurposeCLI); err == nil {
		t.Fatal("换密钥后必须验签失败")
	}
}

// alg=none 是 JWT 的经典攻击面，必须被 WithValidMethods 挡住。
func TestToken_RejectsAlgNone(t *testing.T) {
	s, _ := NewSigner(secret)
	// {"alg":"none","typ":"JWT"}.{"sub":"u1","org":"o1","pur":"cli"}.
	const forged = "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0." +
		"eyJzdWIiOiJ1MSIsIm9yZyI6Im8xIiwicHVyIjoiY2xpIn0."
	if _, err := s.Verify(forged, PurposeCLI); err == nil {
		t.Fatal("alg=none 必须拒绝")
	}
}

func TestPassword(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := VerifyPassword("correct horse battery staple", h)
	if err != nil || !ok {
		t.Fatal("正确密码应通过")
	}
	ok, _ = VerifyPassword("wrong", h)
	if ok {
		t.Fatal("错误密码必须失败")
	}
	if _, err := VerifyPassword("x", "not-a-hash"); err == nil {
		t.Fatal("非法哈希格式必须报错")
	}
}

func TestPassword_SaltIsRandom(t *testing.T) {
	a, _ := HashPassword("same")
	b, _ := HashPassword("same")
	if a == b {
		t.Fatal("相同密码必须产生不同哈希（盐要随机）")
	}
}

func TestRandomToken_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		tok, err := RandomToken()
		if err != nil {
			t.Fatal(err)
		}
		if seen[tok] {
			t.Fatal("令牌重复")
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
	// 剔除易混字符：用户要手敲这串码
	for _, bad := range []string{"0", "O", "1", "I", "L", "U", "V"} {
		if strings.Contains(code, bad) {
			t.Fatalf("不应包含易混字符 %q: %s", bad, code)
		}
	}
}

func TestNormalizeUserCode(t *testing.T) {
	for _, in := range []string{"wxyz2345", "WXYZ-2345", " wxyz-2345 ", "WXYZ 2345"} {
		if got := NormalizeUserCode(in); got != "WXYZ-2345" {
			t.Fatalf("%q → %q", in, got)
		}
	}
}

func TestHashToken(t *testing.T) {
	if HashToken("a") == HashToken("b") {
		t.Fatal("不同令牌应有不同摘要")
	}
	if !EqualHash(HashToken("a"), HashToken("a")) {
		t.Fatal("相同令牌应有相同摘要")
	}
}
