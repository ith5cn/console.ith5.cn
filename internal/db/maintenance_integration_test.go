package db_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ith5/ith5/internal/identity"
	"github.com/ith5/ith5/internal/maintenance"
)

// 保留期清理只删过期行；对账能发现映射改动后的角色漂移并应用。
func TestDB_MaintenanceAndReconcile(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	hash, _ := identity.HashPassword("demo-password")
	if _, err := d.Bootstrap().SeedDemo(ctx, "demo", "demo@example.com", hash); err != nil {
		t.Fatal(err)
	}
	var orgID, userID, projectID string
	if err := d.Pool().QueryRow(ctx, `SELECT o.id::text, u.id::text FROM organizations o JOIN users u ON u.org_id = o.id WHERE o.slug = 'demo' AND u.email = 'member@example.com'`).Scan(&orgID, &userID); err != nil {
		t.Fatal(err)
	}
	if err := d.Pool().QueryRow(ctx, `SELECT id::text FROM projects WHERE org_id = $1 AND slug = 'billing'`, orgID).Scan(&projectID); err != nil {
		t.Fatal(err)
	}

	var machineID string
	if err := d.Pool().QueryRow(ctx, `INSERT INTO machines (org_id, user_id, fingerprint) VALUES ($1, $2, 'fp-test') RETURNING id::text`, orgID, userID).Scan(&machineID); err != nil {
		t.Fatal(err)
	}
	// --- 清理：一条 20 个月前的会话、一条 2 个月前的会话；组织保留期 13 个月
	old := time.Now().AddDate(0, -20, 0)
	recent := time.Now().AddDate(0, -2, 0)
	for _, ts := range []time.Time{old, recent} {
		if _, err := d.Pool().Exec(ctx, `INSERT INTO sessions (org_id, user_id, machine_id, session_id, tool, started_at) VALUES ($1, $2, $3, gen_random_uuid()::text, 'claude', $4)`, orgID, userID, machineID, ts); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.Pool().Exec(ctx, `INSERT INTO report_events (machine_id, event_id, seq, type, received_at) VALUES ($1, 'ev-old', 1, 'vote_delta', now() - interval '40 days')`, machineID); err != nil {
		t.Fatal(err)
	}
	j := maintenance.New(d.Maintenance(), d.OIDC(), discardLogger())
	rep := j.RunOnce(ctx, "")
	if len(rep.Errors) > 0 {
		t.Fatalf("清理出错: %v", rep.Errors)
	}
	if rep.Deleted["sessions"] != 1 {
		t.Fatalf("应删 1 条过期会话，删了 %d", rep.Deleted["sessions"])
	}
	var left int
	_ = d.Pool().QueryRow(ctx, `SELECT count(*) FROM sessions WHERE org_id = $1`, orgID).Scan(&left)
	if left != 1 {
		t.Fatalf("应留 1 条近期会话，剩 %d", left)
	}
	if rep.Deleted["report_events"] != 1 {
		t.Fatalf("应删 1 条过期账本，删了 %d", rep.Deleted["report_events"])
	}

	// --- 对账：OIDC 首登按映射进 billing；之后映射改成 admin，对账应发现并应用
	store := d.OIDC()
	pid, err := store.PutOIDCConfig(ctx, identity.OIDCConfig{OrgID: orgID, Issuer: "https://idp.example", ClientID: "c", Scopes: []string{"openid"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	mappings := []identity.GroupMapping{
		{IdPGroup: "eng", Target: "org", Role: "member", Priority: 1},
		{IdPGroup: "billing", Target: "project", TargetID: projectID, Role: "member", Priority: 1},
	}
	if err := store.PutGroupMappings(ctx, orgID, pid, mappings); err != nil {
		t.Fatal(err)
	}
	id := identity.Identity{Issuer: "https://idp.example", Subject: "sub-1", Email: "sso@example.com", Name: "SSO", Groups: []string{"eng", "billing"}}
	orgRole, tr, pr := identity.ResolveGroupMappings(mappings, id.Groups)
	m, err := store.ProvisionOIDCIdentity(ctx, orgID, id, orgRole, tr, pr)
	if err != nil {
		t.Fatal(err)
	}
	if diffs := mustReconcile(t, store, orgID); len(diffs) != 0 {
		t.Fatalf("刚登录不该有差异: %+v", diffs)
	}
	mappings[1].Role = "admin"
	if err := store.PutGroupMappings(ctx, orgID, pid, mappings); err != nil {
		t.Fatal(err)
	}
	diffs := mustReconcile(t, store, orgID)
	if len(diffs) != 1 || diffs[0].UserID != m.UserID || diffs[0].Changes[0].Expected != "admin" {
		t.Fatalf("应发现 billing 角色漂移: %+v", diffs)
	}
	if err := store.ApplyMembership(ctx, orgID, m.UserID, diffs[0].OrgRole, diffs[0].TeamRoles, diffs[0].ProjectRoles); err != nil {
		t.Fatal(err)
	}
	if diffs := mustReconcile(t, store, orgID); len(diffs) != 0 {
		t.Fatalf("应用后仍有差异: %+v", diffs)
	}
	var role string
	_ = d.Pool().QueryRow(ctx, `SELECT role::text FROM project_members WHERE user_id = $1 AND project_id = $2`, m.UserID, projectID).Scan(&role)
	if role != "admin" {
		t.Fatalf("项目角色 = %q", role)
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func mustReconcile(t *testing.T, store interface {
	ListGroupMappings(context.Context, string) ([]identity.GroupMapping, error)
	ListOIDCMembers(context.Context, string) ([]identity.OIDCMember, error)
}, orgID string) []identity.ReconcileDiff {
	t.Helper()
	ctx := context.Background()
	ms, err := store.ListGroupMappings(ctx, orgID)
	if err != nil {
		t.Fatal(err)
	}
	members, err := store.ListOIDCMembers(ctx, orgID)
	if err != nil {
		t.Fatal(err)
	}
	return identity.Reconcile(ms, members)
}
