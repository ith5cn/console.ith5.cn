package link

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Symlink 是 macOS / Linux / WSL 的策略：一个符号链接。
//
// 替换是原子的：POSIX 的 rename() 覆盖一个已存在的符号链接不会出现
// 中间态，因此**没有空窗期**。
type Symlink struct{}

func (Symlink) ID() string { return "symlink" }

func (s Symlink) Materialize(storeDir, target string, _ MarkerData, _ StoreCtx) error {
	abs, err := filepath.Abs(storeDir)
	if err != nil {
		return err
	}
	// 在 skills/ 之外的同卷位置建临时链接，再 rename 进来。
	// 不在 skills/ 里建，是为了避免 Claude Code 瞬时把 .tmp 当成一个技能。
	tmp := filepath.Join(filepath.Dir(filepath.Dir(target)), ".ith5-staging",
		fmt.Sprintf(".link-%s", filepath.Base(target)))
	_ = os.Remove(tmp)
	if err := os.MkdirAll(filepath.Dir(tmp), 0o755); err != nil {
		return err
	}
	if err := os.Symlink(abs, tmp); err != nil {
		return fmt.Errorf("创建符号链接: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("原子替换入口: %w", err)
	}
	return nil
}

func (Symlink) Inspect(target string, sc StoreCtx) (Info, error) {
	return inspectPointer(target, sc)
}

// Release 删除链接本身，绝不跟随链接删除目标。
func (Symlink) Release(target string, _ StoreCtx) error {
	fi, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("拒绝释放非链接的目标 %s", target)
	}
	// Windows 的 junction 在 API 层是目录，必须用 Remove 之外的语义处理；
	// Go 的 os.Remove 会对二者分别调用正确的系统调用。
	if runtime.GOOS == "windows" {
		return os.Remove(target)
	}
	return os.Remove(target)
}
