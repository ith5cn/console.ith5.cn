package knowledge

import (
	"strings"
	"testing"
	"time"
)

func TestScanSecrets(t *testing.T) {
	hits := ScanSecrets("intro\nAKIAABCDEFGHIJKLMNOP is my key\nok\npostgres://u:p4ssw0rd@db/x\ntoken = \"abcdefghijklmnop1234\"\n-----BEGIN RSA PRIVATE KEY-----")
	kinds := map[string]int{}
	for _, h := range hits {
		kinds[h.Kind] = h.Line
	}
	if kinds["aws_access_key"] != 2 || kinds["connection_string"] != 4 || kinds["kv_secret"] != 5 || kinds["private_key"] != 6 {
		t.Fatalf("命中不完整: %+v", hits)
	}
	if len(ScanSecrets("用 55432 避开本机 Postgres。\nexport PORT=8080\n")) != 0 {
		t.Fatal("普通内容不应误报")
	}
}

func TestLearningNameAndFrontmatter(t *testing.T) {
	now := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	n, err := LearningName("Port conflict on 5432!", now)
	if err != nil || !strings.HasPrefix(n, "port-conflict-on-5432-2026-09-11-") || len(n) != len("port-conflict-on-5432-2026-09-11-")+4 {
		t.Fatalf("命名: %s %v", n, err)
	}
	n, _ = LearningName("端口冲突", now)
	if !strings.HasPrefix(n, "learning-2026-09-11-") {
		t.Fatalf("中文标题应回落到 learning: %s", n)
	}
	out := ensureFrontmatter("body", "T", "u1", []string{"a", "b"}, now)
	if !strings.HasPrefix(out, "---\ntitle: \"T\"\nauthor: u1\ndate: 2026-09-11\ntags: [a, b]\n---\n\nbody") {
		t.Fatalf("应补 frontmatter: %q", out)
	}
	if ensureFrontmatter("---\ntitle: x\n---\nbody", "T", "u1", nil, now) != "---\ntitle: x\n---\nbody" {
		t.Fatal("已有 frontmatter 应原样保留")
	}
	if stripFrontmatter("---\ntitle: x\n---\n\nbody\n") != "body\n" {
		t.Fatalf("stripFrontmatter: %q", stripFrontmatter("---\ntitle: x\n---\n\nbody\n"))
	}
}

func TestConfidence(t *testing.T) {
	now := time.Now()
	if c := Confidence(0, 0, nil, now); c < 0.49 || c > 0.51 {
		t.Fatalf("无数据时应约 0.5: %f", c)
	}
	recent := now.Add(-24 * time.Hour)
	old := now.AddDate(-1, 0, 0)
	if Confidence(10, 8, &recent, now) <= Confidence(10, 8, &old, now) {
		t.Fatal("久未召回的置信度应更低")
	}
	if Confidence(100, 1, &recent, now) >= Confidence(10, 8, &recent, now) {
		t.Fatal("召回多点赞少应更低")
	}
}
