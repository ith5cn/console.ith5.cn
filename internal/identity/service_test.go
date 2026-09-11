package identity_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ith5/ith5/internal/identity"
	"github.com/ith5/ith5/internal/identity/identitytest"
)

const secret = "0123456789abcdef0123456789abcdef"

type fixture struct {
	store   *identitytest.Store
	svc     *identity.Service
	acctID  string
	userID  string
	clock   time.Time
	ctx     context.Context
	hashed  string
	tick    func(d time.Duration)
	signer  *identity.Signer
	orgSlug string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	store := identitytest.New()
	hash, err := identity.HashPassword("pw-123456")
	if err != nil {
		t.Fatal(err)
	}
	acct, user := store.AddLocalUser("alice@example.com", hash, "org-1", "acme", identity.RoleMember)
	signer, _ := identity.NewSigner(secret)
	f := &fixture{
		store: store, acctID: acct, userID: user, hashed: hash, ctx: context.Background(),
		clock: time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC), signer: signer, orgSlug: "acme",
	}
	f.svc = identity.NewService(store, signer, "https://console.example").WithClock(func() time.Time { return f.clock })
	f.tick = func(d time.Duration) { f.clock = f.clock.Add(d) }
	return f
}

func TestLogin_ListsActiveMembershipsOnly(t *testing.T) {
	f := newFixture(t)
	// 同一账号在第二个组织的成员记录，且已停用
	m := f.store.Memberships[f.userID]
	m.UserID, m.OrgID, m.OrgSlug, m.Suspended = "user-x", "org-2", "other", true
	f.store.Memberships["user-x"] = m

	res, err := f.svc.LoginWithPassword(f.ctx, " Alice@Example.com ", "pw-123456")
	if err != nil {
		t.Fatal(err)
	}
	if res.Account.ID != f.acctID || len(res.Memberships) != 1 || res.Memberships[0].UserID != f.userID {
		t.Fatalf("应只列出未停用的成员记录: %+v", res)
	}
}

func TestLogin_BadCredentialsAreIndistinguishable(t *testing.T) {
	f := newFixture(t)
	_, err1 := f.svc.LoginWithPassword(f.ctx, "alice@example.com", "wrong")
	_, err2 := f.svc.LoginWithPassword(f.ctx, "nobody@example.com", "wrong")
	if !errors.Is(err1, identity.ErrBadCredentials) || !errors.Is(err2, identity.ErrBadCredentials) {
		t.Fatalf("密码错误与账号不存在必须返回同一错误: %v / %v", err1, err2)
	}
}

func TestIssueSession_RejectsOtherAccountsMembership(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.IssueSession(f.ctx, "someone-else", f.userID); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("登录凭据不能进入别人的成员身份: %v", err)
	}
	toks, err := f.svc.IssueSession(f.ctx, f.acctID, f.userID)
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.signer.Verify(toks.AccessToken, identity.TokenKindSession)
	if err != nil || c.UserID() != f.userID || c.OrgID != "org-1" {
		t.Fatalf("会话令牌错误: %v %+v", err, c)
	}
	if toks.RefreshToken != "" {
		t.Fatal("会话令牌不发刷新凭据")
	}
}

func TestDeviceFlow_EndToEnd(t *testing.T) {
	f := newFixture(t)
	start, err := f.svc.DeviceStart(f.ctx, "fp-1", "laptop", "darwin", "")
	if err != nil {
		t.Fatal(err)
	}
	if start.VerificationURL != "https://console.example/activate" {
		t.Fatalf("验证链接错误: %s", start.VerificationURL)
	}
	if _, err := f.svc.DevicePoll(f.ctx, start.DeviceCode); !errors.Is(err, identity.ErrAuthorizationPending) {
		t.Fatalf("未批准时应返回 pending: %v", err)
	}
	if err := f.svc.DeviceApprove(f.ctx, f.userID, start.UserCode); err != nil {
		t.Fatal(err)
	}
	toks, err := f.svc.DevicePoll(f.ctx, start.DeviceCode)
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.signer.Verify(toks.AccessToken, identity.TokenKindDevice)
	if err != nil || c.MachineID == "" || c.MachineID != toks.MachineID {
		t.Fatalf("设备令牌应携带 machine_id: %v %+v", err, c)
	}
	if toks.RefreshToken == "" {
		t.Fatal("设备令牌必须附带刷新凭据")
	}
	// 同一个码只能消费一次
	if _, err := f.svc.DevicePoll(f.ctx, start.DeviceCode); !errors.Is(err, identity.ErrCodeExpired) {
		t.Fatalf("重复消费应失败: %v", err)
	}
}

func TestDeviceFlow_ExpiredCode(t *testing.T) {
	f := newFixture(t)
	start, _ := f.svc.DeviceStart(f.ctx, "fp-1", "", "", "")
	f.tick(identity.DeviceCodeTTL + time.Second)
	if err := f.svc.DeviceApprove(f.ctx, f.userID, start.UserCode); !errors.Is(err, identity.ErrCodeExpired) {
		t.Fatalf("过期码不能批准: %v", err)
	}
	if _, err := f.svc.DevicePoll(f.ctx, start.DeviceCode); !errors.Is(err, identity.ErrCodeExpired) {
		t.Fatalf("过期码轮询应返回失效: %v", err)
	}
	if _, err := f.svc.DevicePoll(f.ctx, "no-such-code"); !errors.Is(err, identity.ErrCodeExpired) {
		t.Fatalf("不存在的码: %v", err)
	}
}

func TestDeviceFlow_SuspendedCannotApproveOrPoll(t *testing.T) {
	f := newFixture(t)
	start, _ := f.svc.DeviceStart(f.ctx, "fp-1", "", "", "")
	if err := f.svc.DeviceApprove(f.ctx, f.userID, start.UserCode); err != nil {
		t.Fatal(err)
	}
	m := f.store.Memberships[f.userID]
	m.Suspended = true
	f.store.Memberships[f.userID] = m
	if _, err := f.svc.DevicePoll(f.ctx, start.DeviceCode); !errors.Is(err, identity.ErrSuspended) {
		t.Fatalf("停用成员不得拿到令牌: %v", err)
	}
}

func deviceTokens(t *testing.T, f *fixture) identity.Tokens {
	t.Helper()
	start, _ := f.svc.DeviceStart(f.ctx, "fp-1", "", "", "")
	if err := f.svc.DeviceApprove(f.ctx, f.userID, start.UserCode); err != nil {
		t.Fatal(err)
	}
	toks, err := f.svc.DevicePoll(f.ctx, start.DeviceCode)
	if err != nil {
		t.Fatal(err)
	}
	return toks
}

func TestRefresh_RotatesAndDetectsReuse(t *testing.T) {
	f := newFixture(t)
	first := deviceTokens(t, f)

	second, err := f.svc.Refresh(f.ctx, first.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if second.RefreshToken == first.RefreshToken {
		t.Fatal("刷新必须轮换出新凭据")
	}
	if f.store.ActiveRefreshCount() != 1 {
		t.Fatalf("旧凭据应被撤销，活跃数 = %d", f.store.ActiveRefreshCount())
	}
	// 旧凭据再次出现 = 泄露：整族撤销
	if _, err := f.svc.Refresh(f.ctx, first.RefreshToken); !errors.Is(err, identity.ErrRefreshReused) {
		t.Fatalf("重用应被识别: %v", err)
	}
	if f.store.ActiveRefreshCount() != 0 {
		t.Fatal("重用后整族必须撤销")
	}
	if _, err := f.svc.Refresh(f.ctx, second.RefreshToken); err == nil {
		t.Fatal("同族的新凭据也应失效")
	}
}

func TestRefresh_ExpiredAndUnknown(t *testing.T) {
	f := newFixture(t)
	toks := deviceTokens(t, f)
	f.tick(identity.RefreshTokenTTL + time.Second)
	if _, err := f.svc.Refresh(f.ctx, toks.RefreshToken); !errors.Is(err, identity.ErrInvalidGrant) {
		t.Fatalf("过期凭据: %v", err)
	}
	if _, err := f.svc.Refresh(f.ctx, "nope"); !errors.Is(err, identity.ErrInvalidGrant) {
		t.Fatalf("未知凭据: %v", err)
	}
}

func TestRefresh_RevokedMachineBlocked(t *testing.T) {
	f := newFixture(t)
	toks := deviceTokens(t, f)
	m := f.store.Machines[toks.MachineID]
	now := f.clock
	m.RevokedAt = &now
	f.store.Machines[toks.MachineID] = m
	if _, err := f.svc.Refresh(f.ctx, toks.RefreshToken); !errors.Is(err, identity.ErrMachineRevoked) {
		t.Fatalf("已撤销设备不得续期: %v", err)
	}
}

func TestLogout_RevokesMachineTokens(t *testing.T) {
	f := newFixture(t)
	toks := deviceTokens(t, f)
	if err := f.svc.Logout(f.ctx, f.userID, toks.MachineID); err != nil {
		t.Fatal(err)
	}
	if f.store.ActiveRefreshCount() != 0 {
		t.Fatal("登出后该设备的刷新凭据应全部撤销")
	}
}
