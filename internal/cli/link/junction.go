package link

import (
	"fmt"
	"os"
	"path/filepath"
)

// Junction 是 Windows 的策略：目录联接。
//
// **不需要管理员权限或开发者模式**——需要提权的是目录符号链接
// （mklink /D），而这里用的是目录联接（mklink /J）。
//
// 与 symlink 的差别只有两点：
//   - 目标路径必须是绝对路径
//   - 不支持 rename 覆盖，需先释放再创建，存在微秒级空窗
type Junction struct{}

func (Junction) ID() string { return "junction" }

func (j Junction) Materialize(storeDir, target string, _ MarkerData) error {
	abs, err := filepath.Abs(storeDir)
	if err != nil {
		return err
	}
	// junction 无法 rename 覆盖，先释放旧的
	if fi, err := os.Lstat(target); err == nil {
		if fi.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("拒绝覆盖非链接的目标 %s", target)
		}
		if err := os.Remove(target); err != nil {
			return fmt.Errorf("释放旧入口: %w", err)
		}
	}
	if err := makeJunction(abs, target); err != nil {
		return fmt.Errorf("创建目录联接: %w", err)
	}
	return nil
}

func (Junction) Inspect(target, storeRoot string) (Info, error) {
	return inspectPointer(target, storeRoot)
}

// Release 删除联接本身。
//
// ⚠️ Windows 上 junction 在 API 层被视为目录。用错 API 可能**递归删入
// 目标**，即清空 ~/.ith5/store。os.Remove 对 junction 会调用
// RemoveDirectory，只摘掉重解析点，不触碰目标内容。
func (Junction) Release(target string) error {
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
	return os.Remove(target)
}
