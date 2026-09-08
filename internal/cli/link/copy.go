package link

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Copy 是降级策略：把内容目录复制到位，用双 rename 换目录。
//
// 适用于 junction 不可用的场景（家目录在网络盘、非 NTFS 卷等）。
//
// 与 link 策略共享同一个不变量——store 仍是真相，目标仍是一次性的：
// 中断后不需要按日志回滚，清掉残留从 store 重做即可。因此**这条路径
// 同样不需要事务日志与备份**。
//
// 唯一的不对称是归属判定：复制出来的目录与用户自建的目录长得一样，
// 因此必须靠 marker 文件标记。
type Copy struct {
	// Staging、Trash 必须与目标同卷（<claude_home> 之下）且在 skills 之外。
	Staging string
	Trash   string
}

func (Copy) ID() string { return "copy" }

func (c Copy) Materialize(storeDir, target string, md MarkerData) error {
	if c.Staging == "" || c.Trash == "" {
		return fmt.Errorf("copy 策略需要配置 Staging 与 Trash 路径")
	}
	name := filepath.Base(target)
	stage := filepath.Join(c.Staging, name)

	// 1. 复制到 staging 并写 marker（慢，但完全无副作用；失败即删掉重来）
	_ = os.RemoveAll(stage)
	if err := os.MkdirAll(c.Staging, 0o755); err != nil {
		return err
	}
	if err := copyTree(storeDir, stage); err != nil {
		os.RemoveAll(stage)
		return fmt.Errorf("复制内容到暂存区: %w", err)
	}
	md.Strategy = "copy"
	if err := writeMarker(stage, md); err != nil {
		os.RemoveAll(stage)
		return err
	}

	// 2. 把旧副本挪出 skills（原子）
	if _, err := os.Lstat(target); err == nil {
		if err := os.MkdirAll(c.Trash, 0o755); err != nil {
			return err
		}
		grave := filepath.Join(c.Trash, fmt.Sprintf("%s-%d", name, time.Now().UnixNano()))
		if err := os.Rename(target, grave); err != nil {
			os.RemoveAll(stage)
			return fmt.Errorf("移走旧副本: %w", err)
		}
		defer os.RemoveAll(grave)
	}

	// 3. 把新副本移入（原子）
	//    2 与 3 之间存在微秒级空窗，期间该技能短暂不存在。
	//    不为此加锁；真正的风险是并发 sync，由 sync.lock 互斥保证。
	if err := os.Rename(stage, target); err != nil {
		return fmt.Errorf("移入新副本: %w", err)
	}
	return nil
}

func (Copy) Inspect(target, storeRoot string) (Info, error) {
	return inspectPointer(target, storeRoot)
}

// Release 把目录挪进 trash 后删除。
// 只有带我方 marker 的目录才允许释放——这是不碰用户文件的最后一道闸。
func (c Copy) Release(target string) error {
	fi, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return os.Remove(target)
	}
	if _, ok := readMarker(target); !ok {
		return fmt.Errorf("拒绝释放不含 ITH5 标记的目录 %s", target)
	}
	if err := os.MkdirAll(c.Trash, 0o755); err != nil {
		return err
	}
	grave := filepath.Join(c.Trash, fmt.Sprintf("%s-%d", filepath.Base(target), time.Now().UnixNano()))
	if err := os.Rename(target, grave); err != nil {
		return err
	}
	return os.RemoveAll(grave)
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		if !info.Mode().IsRegular() {
			// store 中只应有普通文件；遇到其他类型说明内容被污染
			return fmt.Errorf("内容目录中出现非普通文件: %s", rel)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		w, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		defer w.Close()
		_, err = io.Copy(w, in)
		return err
	})
}
