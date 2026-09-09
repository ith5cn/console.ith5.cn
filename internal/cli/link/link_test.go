package link

import (
	"os"
	"path/filepath"
	"testing"
)

// 测试环境：模拟 ~/.ith5/store 与 <claude_home>/skills 的布局。
type env struct {
	root      string
	storeRoot string
	skills    string
	staging   string
	trash     string
}

func newEnv(t *testing.T) env {
	t.Helper()
	root := t.TempDir()
	e := env{
		root:      root,
		storeRoot: filepath.Join(root, "ith5", "store"),
		skills:    filepath.Join(root, "claude", "skills"),
		staging:   filepath.Join(root, "claude", ".ith5-staging"),
		trash:     filepath.Join(root, "claude", ".ith5-trash"),
	}
	for _, d := range []string{e.storeRoot, e.skills, e.staging, e.trash} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

// content 在 store 里造一个内容目录，返回其绝对路径。
func (e env) content(t *testing.T, bundle, sum, body string) string {
	t.Helper()
	dir := filepath.Join(e.storeRoot, bundle, sum)
	if err := os.MkdirAll(filepath.Join(dir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "SKILL.md"), body)
	write(t, filepath.Join(dir, "references", "api.md"), "ref-"+body)
	return dir
}

func (e env) target(name string) string { return filepath.Join(e.skills, name) }

// sc 组装归属判定所需的 store 侧信息。测试里的内容目录布局是
// store/<bundle>/<sum>，与生产的 store/<kind>/<bundle>/<sum> 只差一层，
// 对策略而言没有区别——它只认 BundleDir。
func (e env) sc(bundle string) StoreCtx {
	return StoreCtx{
		Root:      e.storeRoot,
		BundleDir: filepath.Join(e.storeRoot, bundle),
		EntryFile: "SKILL.md",
	}
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// strategies 返回本平台上可测的全部策略。
// 三种实现必须在同一套用例下产生一致的最终状态（技术方案 §19.1）。
func strategies(e env) []Strategy {
	return []Strategy{
		Symlink{},
		Copy{Staging: e.staging, Trash: e.trash},
	}
}

func md(name, sum string, ver int) MarkerData {
	return MarkerData{BundleID: "b-" + name, BundleName: name, Version: ver, Checksum: "sha256:" + sum}
}

// ---- 三策略共用的行为契约 ----

func TestStrategies_InstallUpdateRelease(t *testing.T) {
	for _, s := range strategies(newEnv(t)) {
		t.Run(s.ID(), func(t *testing.T) {
			e := newEnv(t)
			if c, ok := s.(Copy); ok {
				c.Staging, c.Trash = e.staging, e.trash
				s = c
			}
			v1 := e.content(t, "corp-a", "aaaaaaaaaaaaaaaa", "v1")
			v2 := e.content(t, "corp-a", "bbbbbbbbbbbbbbbb", "v2")
			tgt := e.target("corp-a")

			// 安装
			if err := s.Materialize(v1, tgt, md("corp-a", "aaaaaaaaaaaaaaaa", 1), e.sc("corp-a")); err != nil {
				t.Fatal(err)
			}
			if got := read(t, filepath.Join(tgt, "SKILL.md")); got != "v1" {
				t.Fatalf("内容应为 v1，实际 %q", got)
			}
			if got := read(t, filepath.Join(tgt, "references", "api.md")); got != "ref-v1" {
				t.Fatal("支持文件应可读")
			}
			info, err := s.Inspect(tgt, e.sc("corp-a"))
			if err != nil || info.Ownership != OwnMine {
				t.Fatalf("安装后应判定为我方所有: %+v %v", info, err)
			}

			// 更新
			if err := s.Materialize(v2, tgt, md("corp-a", "bbbbbbbbbbbbbbbb", 2), e.sc("corp-a")); err != nil {
				t.Fatal(err)
			}
			if got := read(t, filepath.Join(tgt, "SKILL.md")); got != "v2" {
				t.Fatalf("更新后应为 v2，实际 %q", got)
			}

			// 回滚（指回旧内容，零下载）
			if err := s.Materialize(v1, tgt, md("corp-a", "aaaaaaaaaaaaaaaa", 3), e.sc("corp-a")); err != nil {
				t.Fatal(err)
			}
			if got := read(t, filepath.Join(tgt, "SKILL.md")); got != "v1" {
				t.Fatalf("回滚后应为 v1，实际 %q", got)
			}

			// 释放
			if err := s.Release(tgt, e.sc("corp-a")); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(tgt); !os.IsNotExist(err) {
				t.Fatal("释放后入口应消失")
			}
			// store 必须完好 —— 这是最危险的一条：
			// Windows 上用错删除 API 会递归删入目标，清空整个 store
			if read(t, filepath.Join(v1, "SKILL.md")) != "v1" {
				t.Fatal("释放入口后 store 内容必须完好")
			}
			if read(t, filepath.Join(v2, "SKILL.md")) != "v2" {
				t.Fatal("释放入口后 store 内容必须完好")
			}
		})
	}
}

// 用户自有目录：判定为 foreign，且释放被拒绝，内容零改动。
func TestStrategies_ForeignDirectoryIsNeverTouched(t *testing.T) {
	for _, s := range strategies(newEnv(t)) {
		t.Run(s.ID(), func(t *testing.T) {
			e := newEnv(t)
			if c, ok := s.(Copy); ok {
				c.Staging, c.Trash = e.staging, e.trash
				s = c
			}
			tgt := e.target("corp-a")
			if err := os.MkdirAll(tgt, 0o755); err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(tgt, "SKILL.md"), "用户自己写的")
			write(t, filepath.Join(tgt, "notes.md"), "用户的笔记")

			info, err := s.Inspect(tgt, e.sc("corp-a"))
			if err != nil {
				t.Fatal(err)
			}
			if info.Ownership != OwnForeign {
				t.Fatalf("无 marker 的真实目录必须判为 foreign，got %v", info.Ownership)
			}
			if err := s.Release(tgt, e.sc("corp-a")); err == nil {
				t.Fatal("释放用户自有目录必须被拒绝")
			}
			if read(t, filepath.Join(tgt, "SKILL.md")) != "用户自己写的" {
				t.Fatal("用户内容被改动了")
			}
			if read(t, filepath.Join(tgt, "notes.md")) != "用户的笔记" {
				t.Fatal("用户内容被改动了")
			}
		})
	}
}

func TestStrategies_AbsentTarget(t *testing.T) {
	for _, s := range strategies(newEnv(t)) {
		t.Run(s.ID(), func(t *testing.T) {
			e := newEnv(t)
			info, err := s.Inspect(e.target("nope"), e.sc("nope"))
			if err != nil || info.Ownership != OwnAbsent {
				t.Fatalf("不存在的目标应为 absent: %+v %v", info, err)
			}
			// 释放不存在的目标应当是无操作，不报错
			if err := s.Release(e.target("nope"), e.sc("nope")); err != nil {
				t.Fatalf("释放不存在的目标不应报错: %v", err)
			}
		})
	}
}

// ---- symlink 专有 ----

// 指向 store 之外的链接是用户自己建的，不得当作我方所有。
func TestSymlink_ForeignLinkOutsideStore(t *testing.T) {
	e := newEnv(t)
	outside := filepath.Join(e.root, "elsewhere")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	tgt := e.target("corp-a")
	if err := os.Symlink(outside, tgt); err != nil {
		t.Fatal(err)
	}
	info, err := Symlink{}.Inspect(tgt, e.sc("corp-a"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Ownership != OwnForeign {
		t.Fatalf("指向 store 之外的链接必须判为 foreign，got %v", info.Ownership)
	}
}

func TestSymlink_InspectReturnsStoreDir(t *testing.T) {
	e := newEnv(t)
	v1 := e.content(t, "corp-a", "aaaaaaaaaaaaaaaa", "v1")
	tgt := e.target("corp-a")
	if err := (Symlink{}).Materialize(v1, tgt, md("corp-a", "aaaaaaaaaaaaaaaa", 1), e.sc("corp-a")); err != nil {
		t.Fatal(err)
	}
	info, _ := Symlink{}.Inspect(tgt, e.sc("corp-a"))
	if info.StoreDir != v1 {
		t.Fatalf("应能反推 store 目录用于 lock 重建\n got: %s\nwant: %s", info.StoreDir, v1)
	}
}

// 替换过程不得在 skills/ 下留下临时条目污染 / 菜单
func TestSymlink_NoTempEntryInSkills(t *testing.T) {
	e := newEnv(t)
	v1 := e.content(t, "corp-a", "aaaaaaaaaaaaaaaa", "v1")
	if err := (Symlink{}).Materialize(v1, e.target("corp-a"), md("corp-a", "aaaaaaaaaaaaaaaa", 1), e.sc("corp-a")); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(e.skills)
	if len(entries) != 1 || entries[0].Name() != "corp-a" {
		var got []string
		for _, x := range entries {
			got = append(got, x.Name())
		}
		t.Fatalf("skills 下只应有正式条目，实际 %v", got)
	}
}

// ---- copy 专有 ----

func TestCopy_MarkerIdentifiesOwnership(t *testing.T) {
	e := newEnv(t)
	c := Copy{Staging: e.staging, Trash: e.trash}
	v1 := e.content(t, "corp-a", "aaaaaaaaaaaaaaaa", "v1")
	tgt := e.target("corp-a")
	if err := c.Materialize(v1, tgt, md("corp-a", "aaaaaaaaaaaaaaaa", 7), e.sc("corp-a")); err != nil {
		t.Fatal(err)
	}
	info, err := c.Inspect(tgt, e.sc("corp-a"))
	if err != nil || info.Ownership != OwnMine || info.Marker == nil {
		t.Fatalf("copy 策略靠 marker 判定归属: %+v %v", info, err)
	}
	if info.Marker.Version != 7 || info.Marker.BundleName != "corp-a" {
		t.Fatalf("marker 应可用于重建 lock: %+v", info.Marker)
	}
}

func TestCopy_LeavesNoResidue(t *testing.T) {
	e := newEnv(t)
	c := Copy{Staging: e.staging, Trash: e.trash}
	v1 := e.content(t, "corp-a", "aaaaaaaaaaaaaaaa", "v1")
	v2 := e.content(t, "corp-a", "bbbbbbbbbbbbbbbb", "v2")
	tgt := e.target("corp-a")
	if err := c.Materialize(v1, tgt, md("corp-a", "aaaaaaaaaaaaaaaa", 1), e.sc("corp-a")); err != nil {
		t.Fatal(err)
	}
	if err := c.Materialize(v2, tgt, md("corp-a", "bbbbbbbbbbbbbbbb", 2), e.sc("corp-a")); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{e.staging, e.trash} {
		entries, _ := os.ReadDir(d)
		if len(entries) != 0 {
			t.Fatalf("%s 应无残留，实际 %d 项", d, len(entries))
		}
	}
}
