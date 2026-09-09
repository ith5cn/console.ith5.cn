package link

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ith5/ith5/internal/core"
)

// fixture 造一个 store：storeRoot/<name>/<sum>/AGENT.md
func fixture(t *testing.T) (storeRoot, staging, agents string) {
	t.Helper()
	root := t.TempDir()
	storeRoot = filepath.Join(root, "store")
	staging = filepath.Join(root, "claude", ".ith5-staging")
	agents = filepath.Join(root, "claude", "agents")
	for _, d := range []string{storeRoot, staging, agents} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return
}

// sc 组装该 bundle 的 store 侧信息。
func sc(storeRoot, name string) StoreCtx {
	return StoreCtx{
		Root:      storeRoot,
		BundleDir: filepath.Join(storeRoot, name),
		EntryFile: core.AgentFile,
	}
}

func writeVersion(t *testing.T, storeRoot, name, sum, content string) string {
	t.Helper()
	dir := filepath.Join(storeRoot, name, sum)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, core.AgentFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestFileCopy_物化后归属为我方(t *testing.T) {
	storeRoot, staging, agents := fixture(t)
	dir := writeVersion(t, storeRoot, "ag", "aaaa", "---\nname: ag\n---\nv1\n")
	c := FileCopy{Staging: staging, StoreRoot: storeRoot}
	target := filepath.Join(agents, "ag.md")

	if err := c.Materialize(dir, target, MarkerData{}, sc(storeRoot, "ag")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(target)
	if err != nil || string(b) != "---\nname: ag\n---\nv1\n" {
		t.Fatalf("内容不对: %q %v", b, err)
	}
	info, err := c.Inspect(target, sc(storeRoot, "ag"))
	if err != nil || info.Ownership != OwnMine {
		t.Fatalf("应判为我方所有，实际 %v %v", info.Ownership, err)
	}
	if info.StoreDir != dir {
		t.Fatalf("StoreDir 应指向命中的版本目录（lock 重建靠它）：%q", info.StoreDir)
	}
	// staging 不留残留
	if e, _ := os.ReadDir(staging); len(e) != 0 {
		t.Fatalf("staging 应为空，实际 %v", e)
	}
}

// 覆盖普通文件的 rename 是原子的，因此更新不需要 trash、没有空窗。
func TestFileCopy_更新直接覆盖且无需trash(t *testing.T) {
	storeRoot, staging, agents := fixture(t)
	v1 := writeVersion(t, storeRoot, "ag", "aaaa", "v1")
	v2 := writeVersion(t, storeRoot, "ag", "bbbb", "v2")
	c := FileCopy{Staging: staging, StoreRoot: storeRoot}
	target := filepath.Join(agents, "ag.md")

	if err := c.Materialize(v1, target, MarkerData{}, sc(storeRoot, "ag")); err != nil {
		t.Fatal(err)
	}
	if err := c.Materialize(v2, target, MarkerData{}, sc(storeRoot, "ag")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(target)
	if string(b) != "v2" {
		t.Fatalf("应更新为 v2，实际 %q", b)
	}
	info, _ := c.Inspect(target, sc(storeRoot, "ag"))
	if info.StoreDir != v2 {
		t.Fatalf("应命中 v2 的版本目录，实际 %q", info.StoreDir)
	}
}

// 回滚：内容退回 v1 时命中 store 里已有的目录，零下载。
func TestFileCopy_回滚命中已有内容(t *testing.T) {
	storeRoot, staging, agents := fixture(t)
	v1 := writeVersion(t, storeRoot, "ag", "aaaa", "v1")
	v2 := writeVersion(t, storeRoot, "ag", "bbbb", "v2")
	c := FileCopy{Staging: staging, StoreRoot: storeRoot}
	target := filepath.Join(agents, "ag.md")

	_ = c.Materialize(v1, target, MarkerData{}, sc(storeRoot, "ag"))
	_ = c.Materialize(v2, target, MarkerData{}, sc(storeRoot, "ag"))
	if err := c.Materialize(v1, target, MarkerData{}, sc(storeRoot, "ag")); err != nil {
		t.Fatal(err)
	}
	info, _ := c.Inspect(target, sc(storeRoot, "ag"))
	if info.Ownership != OwnMine || info.StoreDir != v1 {
		t.Fatalf("回滚后应命中 v1：%v %q", info.Ownership, info.StoreDir)
	}
}

// 员工自建的同名 agent：我们从没为这个名字下过内容 -> 重名。
func TestFileCopy_用户自建判为重名(t *testing.T) {
	storeRoot, staging, agents := fixture(t)
	c := FileCopy{Staging: staging, StoreRoot: storeRoot}
	target := filepath.Join(agents, "mine.md")
	os.WriteFile(target, []byte("我自己写的"), 0o644)

	info, err := c.Inspect(target, sc(storeRoot, "ag"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Ownership != OwnForeign || info.Reason != ReasonNameTaken {
		t.Fatalf("应为重名受阻，实际 %v/%q", info.Ownership, info.Reason)
	}
}

// 员工改过我方下发的 agent：内容对不上任何版本 -> 本地改动。
// 这两种成因的处置一样（都不碰），但排查方向完全不同，必须区分开。
func TestFileCopy_本地改动判为改动而非重名(t *testing.T) {
	storeRoot, staging, agents := fixture(t)
	dir := writeVersion(t, storeRoot, "ag", "aaaa", "原文")
	c := FileCopy{Staging: staging, StoreRoot: storeRoot}
	target := filepath.Join(agents, "ag.md")
	_ = c.Materialize(dir, target, MarkerData{}, sc(storeRoot, "ag"))

	os.WriteFile(target, []byte("原文\n员工加的一行"), 0o644)

	info, _ := c.Inspect(target, sc(storeRoot, "ag"))
	if info.Ownership != OwnForeign {
		t.Fatal("改过的文件必须判为用户自有，绝不能被覆盖")
	}
	if info.Reason != ReasonLocallyModified {
		t.Fatalf("成因应为本地改动，实际 %q —— 报成重名会让管理员去查一个不存在的重名", info.Reason)
	}
}

// Release 是删文件的路径，必须只删我方的。
func TestFileCopy_Release只删我方(t *testing.T) {
	storeRoot, staging, agents := fixture(t)
	dir := writeVersion(t, storeRoot, "ag", "aaaa", "原文")
	c := FileCopy{Staging: staging, StoreRoot: storeRoot}

	mine := filepath.Join(agents, "ag.md")
	_ = c.Materialize(dir, mine, MarkerData{}, sc(storeRoot, "ag"))
	if err := c.Release(mine, sc(storeRoot, "ag")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mine); !os.IsNotExist(err) {
		t.Fatal("我方的文件应被删除")
	}

	// 用户自建的
	theirs := filepath.Join(agents, "theirs.md")
	os.WriteFile(theirs, []byte("别动我"), 0o644)
	if err := c.Release(theirs, sc(storeRoot, "ag")); err == nil {
		t.Fatal("必须拒绝删除用户自有文件")
	}
	if b, _ := os.ReadFile(theirs); string(b) != "别动我" {
		t.Fatal("用户文件被动了")
	}

	// 被改过的我方文件：撤权也不擦除（PRD：切断续期，不保证擦除）
	modified := filepath.Join(agents, "ag.md")
	_ = c.Materialize(dir, modified, MarkerData{}, sc(storeRoot, "ag"))
	os.WriteFile(modified, []byte("原文+改动"), 0o644)
	if err := c.Release(modified, sc(storeRoot, "ag")); err == nil {
		t.Fatal("被改过的文件不该被删除")
	}
	if b, _ := os.ReadFile(modified); string(b) != "原文+改动" {
		t.Fatal("员工的改动被抹掉了")
	}
}

// Release 绝不能顺着链接删进 store —— 这是目录形态那个陷阱的文件版。
func TestFileCopy_Release不碰store(t *testing.T) {
	storeRoot, staging, agents := fixture(t)
	dir := writeVersion(t, storeRoot, "ag", "aaaa", "原文")
	c := FileCopy{Staging: staging, StoreRoot: storeRoot}
	target := filepath.Join(agents, "ag.md")

	if err := os.Symlink(filepath.Join(dir, core.AgentFile), target); err != nil {
		t.Skipf("本平台不支持符号链接: %v", err)
	}
	info, _ := c.Inspect(target, sc(storeRoot, "ag"))
	if info.Ownership != OwnMine || info.StoreDir != dir {
		t.Fatalf("软链应判为我方并反推出版本目录：%v %q", info.Ownership, info.StoreDir)
	}
	if err := c.Release(target, sc(storeRoot, "ag")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, core.AgentFile)); err != nil {
		t.Fatal("store 中的内容被删掉了")
	}
}
