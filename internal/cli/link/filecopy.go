package link

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// FileCopy 是**文件形态**的物化策略（技术方案 §8.5.3b）。
//
// 它比目录形态的 Copy 简单，而且是本质上的简单：POSIX 的 rename()
// 在目标为非空目录时返回 ENOTEMPTY，所以目录形态必须走
// staging → trash → 目标的两步 rename 并留下一个微秒级空窗；
// 而覆盖一个**已存在的普通文件**是原子且无条件成功的。因此这里
// 只有一次 rename：**没有空窗、不需要 trash**。
//
// 唯一的不对称在归属判定：单文件没有内部空间放 marker，改用内容比对。
type FileCopy struct {
	// Staging 必须与目标同卷（<claude_home> 之下）且在 agents/ 之外。
	Staging string
	// StoreRoot 是 ~/.ith5/store。保留它是为了构造上的对称，
	// 归属判定所需的信息现在由调用方通过 StoreCtx 传入。
	StoreRoot string
}

func (FileCopy) ID() string { return "copy-file" }

// Materialize 把 store 里的入口文件复制成 <root>/<name><ext>。
func (c FileCopy) Materialize(storeDir, target string, _ MarkerData, sc StoreCtx) error {
	if c.Staging == "" {
		return fmt.Errorf("copy-file 策略需要配置 Staging 路径")
	}
	src := filepath.Join(storeDir, sc.EntryFile)
	stage := filepath.Join(c.Staging, filepath.Base(target))

	if err := os.MkdirAll(c.Staging, 0o755); err != nil {
		return err
	}
	_ = os.Remove(stage)
	if err := copyFile(src, stage); err != nil {
		os.Remove(stage)
		return fmt.Errorf("复制内容到暂存区: %w", err)
	}
	// 覆盖已存在的普通文件是原子的，不需要先挪走旧的
	if err := os.Rename(stage, target); err != nil {
		os.Remove(stage)
		return fmt.Errorf("原子替换入口: %w", err)
	}
	return nil
}

// Inspect 判定文件形态入口的归属（技术方案 §8.5.1）。
//
// 符号链接走与目录形态相同的 readlink 铁证；普通文件则**逐字节比对**
// store 中该 bundle 的各个版本。命中即我方所有，且命中的目录名就是
// checksum——这顺带满足了 §8.8 的 lock 重建，信息量与 readlink 反推相同。
//
// 候选集是有界的：按 D11 每个 bundle 最多保留 3 个版本目录，
// 每个里面只有一个几 KB 的文件。
func (c FileCopy) Inspect(target string, sc StoreCtx) (Info, error) {
	fi, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return Info{Ownership: OwnAbsent}, nil
	}
	if err != nil {
		return Info{}, err
	}

	if fi.Mode()&os.ModeSymlink != 0 {
		dest, err := os.Readlink(target)
		if err != nil {
			return Info{Ownership: OwnForeign, Reason: ReasonNameTaken}, nil
		}
		if !filepath.IsAbs(dest) {
			dest = filepath.Join(filepath.Dir(target), dest)
		}
		if underRoot(dest, sc.Root) {
			// 链接指向的是版本目录里的入口文件，store 目录是它的父级
			return Info{Ownership: OwnMine, StoreDir: filepath.Dir(dest)}, nil
		}
		return Info{Ownership: OwnForeign, Reason: ReasonNameTaken}, nil
	}

	if !fi.Mode().IsRegular() {
		return Info{Ownership: OwnForeign, Reason: ReasonNameTaken}, nil
	}

	have, err := os.ReadFile(target)
	if err != nil {
		return Info{}, err
	}
	bundleDir := sc.BundleDir
	entries, err := os.ReadDir(bundleDir)
	if os.IsNotExist(err) {
		// 我们从没为这个 bundle 下过内容 —— 这是用户自己的文件
		return Info{Ownership: OwnForeign, Reason: ReasonNameTaken}, nil
	}
	if err != nil {
		return Info{}, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		want, err := os.ReadFile(filepath.Join(bundleDir, e.Name(), sc.EntryFile))
		if err != nil {
			continue
		}
		if bytes.Equal(have, want) {
			return Info{Ownership: OwnMine, StoreDir: filepath.Join(bundleDir, e.Name())}, nil
		}
	}

	// store 里有这个 bundle 的内容、但本机这份对不上任何一版：
	// 绝大多数情况是员工手工改过我方下发的 agent。
	//
	// 这是**启发式而非证明**——也可能是他自己写了个同名 agent，而我们恰好
	// 也发了一个同名的。两者的处置完全一样（都不碰），区分只影响回执里
	// 给管理员看的成因。宁可猜「本地改动」：那是更常见、也更需要人工跟进的那个。
	return Info{Ownership: OwnForeign, Reason: ReasonLocallyModified}, nil
}

// Release 删除文件本身。
//
// 只有 Inspect 判定为我方所有才真删——用户自建的、以及被用户改过的，
// 一律留着。这与 PRD「切断续期，不保证擦除」的立场一致：
// 撤权停止的是后续更新，不是强制清除他手上的东西。
func (c FileCopy) Release(target string, sc StoreCtx) error {
	if _, err := os.Lstat(target); os.IsNotExist(err) {
		return nil
	}
	info, err := c.Inspect(target, sc)
	if err != nil {
		return err
	}
	if info.Ownership != OwnMine {
		return fmt.Errorf("拒绝删除非我方所有的文件 %s（%s）", target, info.Reason)
	}
	return os.Remove(target)
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}
