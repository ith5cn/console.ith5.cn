package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ith5/ith5/internal/cli/link"
	"github.com/ith5/ith5/internal/core"
)

// lockVersion 3 起记录 shape。旧版 lock 会被判为损坏并自动重建
// ——lock 是缓存不是真相，重建本就是设计好的路径（技术方案 §8.8），
// 因此升级不需要迁移代码。
const lockVersion = 3

// Lock 是本地已装内容的账本。
//
// **它是缓存，不是真相**（技术方案 §8.8）：指针本身（或 copy 策略的
// marker）就能反推出「这个入口属于哪个 bundle 的哪份内容」，因此
// lock 丢失或损坏时可自动重建，不再需要人工恢复。
type Lock struct {
	Version    int                       `json:"version"`
	Server     string                    `json:"server"`
	SyncedAt   time.Time                 `json:"synced_at"`
	TTLSeconds int                       `json:"ttl_seconds"`
	Strategy   string                    `json:"strategy"`
	Bundles    map[string]core.LockEntry `json:"bundles"`
}

func NewLock(server string) *Lock {
	return &Lock{Version: lockVersion, Server: server, Bundles: map[string]core.LockEntry{}}
}

// Expired 判断是否超过 TTL，供 SessionStart 决定要不要触发后台 sync。
func (l *Lock) Expired(now time.Time) bool {
	if l.TTLSeconds <= 0 {
		return true
	}
	return now.After(l.SyncedAt.Add(time.Duration(l.TTLSeconds) * time.Second))
}

// LoadLock 读取 lock；文件缺失或损坏时返回空 lock 并标记需要重建。
func LoadLock(path string) (*Lock, bool, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return NewLock(""), true, nil
	}
	if err != nil {
		return nil, false, err
	}
	var l Lock
	if err := json.Unmarshal(b, &l); err != nil || l.Version != lockVersion || l.Bundles == nil {
		// 损坏不是致命错误：由 marker/readlink 重建即可
		return NewLock(""), true, nil
	}
	return &l, false, nil
}

// Save 以「临时文件 + rename」原子替换。
func (l *Lock) Save(path string) error {
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Rebuild 扫描 skills 目录，由指针与 marker 反推出 lock。
//
// 这使得 lock 损坏时**自动恢复**，而不是像 v1.0 设计的那样进入只读
// 保护模式等人工处理（技术方案 §8.8）。
//
// 反推的依据是路径本身：store/<bundle>/<checksum前16位>。
//
// 注意**内容目录不携带版本号**，也不能携带——内容寻址意味着同一个目录
// 被多个版本共享（回滚正是如此：新旧版本 checksum 相同）。版本号由
// sync 在拿到 manifest 后按 checksum 对照补齐。
func (l *Lock) Rebuild(p Paths, s Resolver) (int, error) {
	n := 0
	// 目录形态：skills/<name>/
	dirN, err := l.rebuildShape(p, s, core.ShapeDir, p.Skills, func(e os.DirEntry) (string, bool) {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			return "", false
		}
		return name, true
	})
	if err != nil {
		return n, err
	}
	n += dirN

	// 文件形态：agents/<name>.md
	fileN, err := l.rebuildShape(p, s, core.ShapeFile, p.Agents, func(e os.DirEntry) (string, bool) {
		name := e.Name()
		if strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".md") || e.IsDir() {
			return "", false
		}
		return strings.TrimSuffix(name, ".md"), true
	})
	if err != nil {
		return n, err
	}
	return n + fileN, nil
}

func (l *Lock) rebuildShape(
	p Paths, s Resolver, shape core.Shape, dir string,
	nameOf func(os.DirEntry) (string, bool),
) (int, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		name, ok := nameOf(e)
		if !ok {
			continue
		}
		target := p.Target(shape, name)
		info, err := s.For(shape).Inspect(target, p.Store)
		if err != nil || info.Ownership != core.OwnMine {
			continue
		}
		entry := core.LockEntry{Target: target, Shape: shape}
		switch {
		case info.Marker != nil:
			// copy 策略（目录形态）：marker 是一次物化的记录，信息最全（含版本号）
			entry.BundleID = info.Marker.BundleID
			entry.Version = info.Marker.Version
			entry.Checksum = info.Marker.Checksum
			entry.Store = filepath.Join(p.Store, name, core.ShortSum(info.Marker.Checksum))
		case info.StoreDir != "":
			// link 策略，或文件形态的内容比对命中：
			// 由路径反推 bundle 名与内容摘要。版本号拿不到，
			// 留 0 由 sync 按 checksum 对照 manifest 补齐。
			entry.Store = info.StoreDir
			entry.ShortSum = filepath.Base(info.StoreDir)
		}
		l.Bundles[name] = entry
		n++
	}
	return n, nil
}

// Resolver 按形态给出策略，是 Rebuild 与 Ownerships 需要的最小能力。
// Strategies 实现了它；测试可以塞替身。
type Resolver interface {
	For(shape core.Shape) link.Strategy
}

// Ownerships 逐个探测目标归属，产出 core.Plan 需要的输入。
//
// shapes 给出每个名字的形态。缺失的按目录形态处理——那是 lock 里
// 没记 shape 的旧条目，重建会补上。
func Ownerships(p Paths, s Resolver, names []string, shapes map[string]core.Shape) (map[string]core.Ownership, error) {
	out := make(map[string]core.Ownership, len(names))
	for _, n := range names {
		shape := shapes[n]
		if shape == "" {
			shape = core.ShapeDir
		}
		info, err := s.For(shape).Inspect(p.Target(shape, n), p.Store)
		if err != nil {
			return nil, fmt.Errorf("探测 %s: %w", n, err)
		}
		out[n] = info.Ownership
	}
	return out, nil
}
