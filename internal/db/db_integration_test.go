package db_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/ith5/ith5/internal/db"
	"github.com/ith5/ith5/internal/identity"
	"github.com/ith5/ith5/internal/platform/pg"
)

// 集成测试需要真实 PostgreSQL：ITH5_TEST_DATABASE_URL 未设置时跳过。
// 每次运行都回滚并重建全部迁移，因此必须指向一个专用的测试库。
func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	dsn := os.Getenv("ITH5_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ITH5_TEST_DATABASE_URL 未设置，跳过集成测试")
	}
	ctx := context.Background()
	if err := pg.Reset(ctx, dsn); err != nil {
		t.Fatalf("重建结构: %v", err)
	}
	d, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	return d
}

func TestDB_SeedAndIdentityRoundTrip(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	hash, _ := identity.HashPassword("demo-password")

	ownerID, err := d.Bootstrap().SeedDemo(ctx, "demo", "demo@example.com", hash)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	// 幂等：再跑一次不报错、不重复
	if _, err := d.Bootstrap().SeedDemo(ctx, "demo", "demo@example.com", hash); err != nil {
		t.Fatalf("seed 第二次: %v", err)
	}

	store := d.Identity()
	acct, gotHash, err := store.FindLocalAccount(ctx, "demo@example.com")
	if err != nil || gotHash != hash || acct.Issuer != identity.IssuerLocal {
		t.Fatalf("FindLocalAccount: %v %+v", err, acct)
	}
	if _, _, err := store.FindLocalAccount(ctx, "ghost@example.com"); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("不存在的账号应 ErrNotFound: %v", err)
	}
	ms, err := store.ListMemberships(ctx, acct.ID)
	if err != nil || len(ms) != 1 || ms[0].UserID != ownerID || ms[0].Role != identity.RoleOwner || ms[0].OrgSlug != "demo" {
		t.Fatalf("ListMemberships: %v %+v", err, ms)
	}

	// 完整走一遍设备授权与刷新，验证 SQL 与 Service 的契约一致
	signer, _ := identity.NewSigner("0123456789abcdef0123456789abcdef")
	svc := identity.NewService(store, signer, "http://localhost")
	start, err := svc.DeviceStart(ctx, "fp-it", "ci", "linux", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DevicePoll(ctx, start.DeviceCode); !errors.Is(err, identity.ErrAuthorizationPending) {
		t.Fatalf("pending: %v", err)
	}
	if err := svc.DeviceApprove(ctx, ownerID, start.UserCode); err != nil {
		t.Fatalf("approve: %v", err)
	}
	toks, err := svc.DevicePoll(ctx, start.DeviceCode)
	if err != nil || toks.RefreshToken == "" {
		t.Fatalf("poll: %v", err)
	}
	if _, err := svc.DevicePoll(ctx, start.DeviceCode); !errors.Is(err, identity.ErrCodeExpired) {
		t.Fatalf("重复消费: %v", err)
	}
	second, err := svc.Refresh(ctx, toks.RefreshToken)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if _, err := svc.Refresh(ctx, toks.RefreshToken); !errors.Is(err, identity.ErrRefreshReused) {
		t.Fatalf("重用检测: %v", err)
	}
	if _, err := svc.Refresh(ctx, second.RefreshToken); err == nil {
		t.Fatal("整族撤销后新凭据也应失效")
	}

	// 设备再次登记（同指纹）应复用同一行并清除撤销标记
	mc, err := store.GetMachine(ctx, toks.MachineID)
	if err != nil || mc.Fingerprint != "fp-it" {
		t.Fatalf("GetMachine: %v %+v", err, mc)
	}
	again, err := store.UpsertMachine(ctx, identity.Machine{OrgID: mc.OrgID, UserID: ownerID, Fingerprint: "fp-it", Hostname: "ci2"})
	if err != nil || again != mc.ID {
		t.Fatalf("同指纹应复用设备行: %v %s != %s", err, again, mc.ID)
	}
	if err := store.TouchMachine(ctx, mc.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestDB_InitOwnerIsIdempotent(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	hash, _ := identity.HashPassword("a-long-password")
	id1, err := d.Bootstrap().InitOwner(ctx, "ACME", "acme", "Boss@Example.com", "Boss", hash)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := d.Bootstrap().InitOwner(ctx, "ACME Inc", "acme", "boss@example.com", "Boss", hash)
	if err != nil || id1 != id2 {
		t.Fatalf("重复初始化应更新同一行: %v %s %s", err, id1, id2)
	}
	acct, _, err := d.Identity().FindLocalAccount(ctx, "boss@example.com")
	if err != nil || acct.Email != "boss@example.com" {
		t.Fatalf("邮箱应规范化为小写: %v %+v", err, acct)
	}
}
