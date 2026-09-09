package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ith5/ith5/internal/cli/link"
	"github.com/ith5/ith5/internal/core"
)

func files(body string) []core.File {
	return []core.File{
		{Path: "SKILL.md", Content: "---\ndescription: t\n---\n\n" + body},
		{Path: "references/api.md", Content: "ref " + body},
	}
}

func sumOf(t *testing.T, fs []core.File) string {
	t.Helper()
	s, err := core.Checksum(fs)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStore_WriteAndHas(t *testing.T) {
	s := NewStore(t.TempDir())
	fs := files("v1")
	sum := sumOf(t, fs)

	if s.Has(core.KindSkill, "corp-a", sum) {
		t.Fatal("写入前不应存在")
	}
	if err := s.Write(core.KindSkill, "corp-a", sum, fs); err != nil {
		t.Fatal(err)
	}
	if !s.Has(core.KindSkill, "corp-a", sum) {
		t.Fatal("写入后应存在")
	}
	got, err := os.ReadFile(filepath.Join(s.Dir(core.KindSkill, "corp-a", sum), "references", "api.md"))
	if err != nil || string(got) != "ref v1" {
		t.Fatalf("支持文件应落盘: %q %v", got, err)
	}
}

// 内容不可变：重复写同一 checksum 是无操作。
func TestStore_WriteIsIdempotent(t *testing.T) {
	s := NewStore(t.TempDir())
	fs := files("v1")
	sum := sumOf(t, fs)
	if err := s.Write(core.KindSkill, "corp-a", sum, fs); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(core.KindSkill, "corp-a", sum, fs); err != nil {
		t.Fatalf("重复写入应无操作: %v", err)
	}
}

// 回滚的核心：内容相同的两个版本命中同一个目录，因此零下载。
func TestStore_SameContentSharesDirectory(t *testing.T) {
	s := NewStore(t.TempDir())
	fs := files("v1")
	sum := sumOf(t, fs)
	if err := s.Write(core.KindSkill, "corp-a", sum, fs); err != nil {
		t.Fatal(err)
	}
	// v13 与回滚产生的 v15 内容一致 → checksum 一致 → 同一目录
	if !s.Has(core.KindSkill, "corp-a", sum) {
		t.Fatal("回滚时应命中已有内容，无需下载")
	}
	entries, _ := os.ReadDir(s.BundleDir(core.KindSkill, "corp-a"))
	if len(entries) != 1 {
		t.Fatalf("内容相同不应产生第二个目录，实际 %d 个", len(entries))
	}
}

// 服务端说什么不算数，落盘前本地必须重算。
func TestStore_RejectsChecksumMismatch(t *testing.T) {
	s := NewStore(t.TempDir())
	fs := files("v1")
	err := s.Write(core.KindSkill, "corp-a", "sha256:"+"0000000000000000000000000000000000000000000000000000000000000000", fs)
	if err == nil {
		t.Fatal("checksum 不匹配必须拒绝落盘")
	}
	entries, _ := os.ReadDir(s.Root())
	for _, e := range entries {
		sub, _ := os.ReadDir(filepath.Join(s.Root(), e.Name()))
		if len(sub) != 0 {
			t.Fatal("拒绝落盘后不应留下任何内容目录")
		}
	}
}

func TestStore_RejectsUnsafeContent(t *testing.T) {
	s := NewStore(t.TempDir())
	bad := []core.File{
		{Path: "SKILL.md", Content: "x"},
		{Path: "../escape.md", Content: "y"},
	}
	if err := s.Write(core.KindSkill, "corp-a", sumOf(t, bad), bad); err == nil {
		t.Fatal("路径穿越必须拒绝")
	}
	if err := s.Write(core.KindSkill, "synced", sumOf(t, files("v1")), files("v1")); err == nil {
		t.Fatal("保留名 synced 必须拒绝")
	}
}

func TestStore_Prune(t *testing.T) {
	s := NewStore(t.TempDir())
	var sums []string
	for _, body := range []string{"v1", "v2", "v3", "v4"} {
		fs := files(body)
		sum := sumOf(t, fs)
		sums = append(sums, sum)
		if err := s.Write(core.KindSkill, "corp-a", sum, fs); err != nil {
			t.Fatal(err)
		}
	}
	// 保留当前在用的那个，总量上限 2
	if err := s.Prune(core.KindSkill, "corp-a", []string{sums[3]}, 2); err != nil {
		t.Fatal(err)
	}
	if !s.Has(core.KindSkill, "corp-a", sums[3]) {
		t.Fatal("在用的内容必须保留")
	}
	entries, _ := os.ReadDir(s.BundleDir(core.KindSkill, "corp-a"))
	if len(entries) > 2 {
		t.Fatalf("应裁剪到 2 个，实际 %d", len(entries))
	}
}

func TestPaths_ScratchIsOutsideSkills(t *testing.T) {
	p := Paths{
		ClaudeHome: "/home/u/.claude",
		Skills:     "/home/u/.claude/skills",
		Staging:    "/home/u/.claude/.ith5-staging",
		Trash:      "/home/u/.claude/.ith5-trash",
	}
	for _, d := range []string{p.Staging, p.Trash} {
		rel, err := filepath.Rel(p.Skills, d)
		if err == nil && rel != ".." && len(rel) > 2 && rel[:2] != ".." {
			t.Fatalf("%s 不得位于 skills 之内，否则会被当成技能", d)
		}
		if filepath.Dir(d) != p.ClaudeHome {
			t.Fatalf("%s 必须在 claude_home 之下，否则 rename 会跨卷", d)
		}
	}
}

func TestPaths_CleanScratch(t *testing.T) {
	root := t.TempDir()
	p := Paths{
		Home: filepath.Join(root, "ith5"), Store: filepath.Join(root, "ith5", "store"),
		ClaudeHome: filepath.Join(root, "claude"),
		Skills:     filepath.Join(root, "claude", "skills"),
		Agents:     filepath.Join(root, "claude", "agents"),
		Staging:    filepath.Join(root, "claude", ".ith5-staging"),
		Trash:      filepath.Join(root, "claude", ".ith5-trash"),
	}
	if err := p.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(p.Staging, "半成品"), 0o755)
	os.MkdirAll(filepath.Join(p.Trash, "旧副本"), 0o755)
	if err := p.CleanScratch(); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{p.Staging, p.Trash} {
		if e, _ := os.ReadDir(d); len(e) != 0 {
			t.Fatalf("%s 中的残留应被无条件清除", d)
		}
	}
	// 目录本身要保留
	if _, err := os.Stat(p.Staging); err != nil {
		t.Fatal("暂存目录本身应保留")
	}
}

// lock 重建必须扫两个目录：skills/ 与 agents/。
// 只扫一个的话，另一种形态的条目会被判为「lock 有、目标无」，
// 下次 sync 重装一遍——回滚零下载的收益也就没了。
func TestLock_重建扫两种形态(t *testing.T) {
	root := t.TempDir()
	p := Paths{
		Home: filepath.Join(root, "ith5"), Store: filepath.Join(root, "ith5", "store"),
		ClaudeHome: filepath.Join(root, "claude"),
		Skills:     filepath.Join(root, "claude", "skills"),
		Agents:     filepath.Join(root, "claude", "agents"),
		Staging:    filepath.Join(root, "claude", ".ith5-staging"),
		Trash:      filepath.Join(root, "claude", ".ith5-trash"),
	}
	if err := p.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	st := NewStore(p.Store)
	strategies := NewStrategies("copy", p)

	skillFiles := []core.File{{Path: core.SkillFile, Content: "---\ndescription: d\n---\n"}}
	skillSum, _ := core.Checksum(skillFiles)
	if err := st.Write(core.KindSkill, "sk", skillSum, skillFiles); err != nil {
		t.Fatal(err)
	}
	agentFiles := []core.File{{Path: core.AgentFile, Content: "---\nname: ag\ndescription: d\n---\n"}}
	agentSum, _ := core.Checksum(agentFiles)
	if err := st.Write(core.KindAgent, "ag", agentSum, agentFiles); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name string
		kind core.Kind
		sum  string
	}{{"sk", core.KindSkill, skillSum}, {"ag", core.KindAgent, agentSum}} {
		err := strategies.For(c.kind.Shape()).Materialize(
			st.Dir(c.kind, c.name, c.sum), p.Target(c.kind, c.name),
			link.MarkerData{BundleID: "b-" + c.name, BundleName: c.name, Kind: string(c.kind), Version: 1, Checksum: c.sum},
			p.StoreCtx(c.kind, c.name))
		if err != nil {
			t.Fatalf("物化 %s: %v", c.name, err)
		}
	}

	// 员工自建的，不该被认领
	os.MkdirAll(filepath.Join(p.Skills, "his-own"), 0o755)
	os.WriteFile(filepath.Join(p.Agents, "his-own.md"), []byte("我自己的"), 0o644)

	l := NewLock("srv")
	n, err := l.Rebuild(p, strategies)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("应重建出 2 条（每种形态各一），实际 %d：%v", n, l.Bundles)
	}
	if got := l.Bundles["skill/sk"].Shape; got != core.ShapeDir {
		t.Errorf("sk 的形态应为 dir，实际 %q", got)
	}
	if got := l.Bundles["agent/ag"].Shape; got != core.ShapeFile {
		t.Errorf("ag 的形态应为 file，实际 %q", got)
	}
	if l.Bundles["agent/ag"].ShortSum != core.ShortSum(agentSum) {
		t.Errorf("agent 应由内容比对反推出摘要，实际 %q", l.Bundles["agent/ag"].ShortSum)
	}
	if _, ok := l.Bundles["skill/his-own"]; ok {
		t.Error("员工自建的内容被认领了")
	}
}

func TestOwnerships_按kind探测正确路径(t *testing.T) {
	root := t.TempDir()
	p := Paths{
		Home: filepath.Join(root, "ith5"), Store: filepath.Join(root, "ith5", "store"),
		ClaudeHome: filepath.Join(root, "claude"),
		Skills:     filepath.Join(root, "claude", "skills"),
		Agents:     filepath.Join(root, "claude", "agents"),
		Staging:    filepath.Join(root, "claude", ".ith5-staging"),
		Trash:      filepath.Join(root, "claude", ".ith5-trash"),
	}
	if err := p.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	// 造一个陷阱：agents/ 下有 x.md，skills/ 下没有 x。
	// kind 搞错的话会去 skills/x 探测，得出 absent 然后重装一遍。
	os.WriteFile(filepath.Join(p.Agents, "x.md"), []byte("用户自己的"), 0o644)

	ref := core.MakeRef(core.KindAgent, "x")
	own, err := Ownerships(p, NewStrategies("copy", p), &Merger{Paths: p}, []core.Ref{ref}, NewLock("srv"))
	if err != nil {
		t.Fatal(err)
	}
	if own[ref] != core.OwnForeign {
		t.Fatalf("应探测到 agents/x.md 并判为用户自有，实际 %v", own[ref])
	}
}
