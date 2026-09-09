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

// lockVersion 4 起以 kind/name 为键（3 只用 name，同名不同 kind 会互相覆盖）。
// 旧版 lock 会被判为损坏并自动重建 ——lock 是缓存不是真相，重建本就是
// 设计好的路径（技术方案 §8.8），因此升级不需要迁移代码。
const lockVersion = 4

// Lock 是本地已装内容的账本。
//
// **它是缓存，不是真相**（技术方案 §8.8）：指针本身（或 copy 策略的
// marker）就能反推出「这个入口属于哪个 bundle 的哪份内容」，因此
// lock 丢失或损坏时可自动重建，不再需要人工恢复。
type Lock struct {
	Version    int                         `json:"version"`
	Server     string                      `json:"server"`
	SyncedAt   time.Time                   `json:"synced_at"`
	TTLSeconds int                         `json:"ttl_seconds"`
	Strategy   string                      `json:"strategy"`
	Bundles    map[core.Ref]core.LockEntry `json:"bundles"`
}

func NewLock(server string) *Lock {
	return &Lock{Version: lockVersion, Server: server, Bundles: map[core.Ref]core.LockEntry{}}
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

// Rebuild 扫描各 kind 的根目录，由指针与 marker 反推出 lock。
//
// 这使得 lock 损坏时**自动恢复**，而不是像 v1.0 设计的那样进入只读
// 保护模式等人工处理（技术方案 §8.8）。
//
// 反推的依据是路径本身：store/<kind>/<bundle>/<checksum前16位>。
//
// 注意**内容目录不携带版本号**，也不能携带——内容寻址意味着同一个目录
// 被多个版本共享（回滚正是如此：新旧版本 checksum 相同）。版本号由
// sync 在拿到 manifest 后按 checksum 对照补齐。
func (l *Lock) Rebuild(p Paths, s Resolver) (int, error) {
	n := 0
	// 按**根目录**扫，而不是按 kind 扫。
	//
	// skill 与 command 共用 skills/ 且落盘形态完全相同——按 kind 扫会
	// 把同一个入口认领两次，产生两条互相矛盾的 lock 记录：manifest 里
	// 只有其中一个 kind，另一个就成了「lock 有、manifest 无」，
	// 下一次 sync 会先装好再把它删掉。
	//
	// 真实的 kind 从内容目录的路径（store/<kind>/<name>/<sum>）或 marker
	// 里读回来，那才是物化当时记下的事实。
	for _, r := range p.scanRoots() {
		cnt, err := l.rebuildRoot(p, s, r)
		if err != nil {
			return n, err
		}
		n += cnt
	}
	return n, nil
}

// scanRoot 是一个待扫描的目标根目录。
type scanRoot struct {
	dir string
	// probe 只用来决定「怎么扫、怎么判归属」——形态、扩展名与 store
	// 入口文件名。同一根目录下所有 kind 的这三项必然一致。
	probe core.Kind
}

// scanRoots 返回去重后的根目录列表。
func (p Paths) scanRoots() []scanRoot {
	seen := map[string]bool{}
	var out []scanRoot
	for _, k := range core.AllKinds {
		root := k.Root()
		if root == "" || seen[root] {
			// 合并形态没有独立入口，无法从文件系统反推，只能依赖 lock 本身。
			// lock 丢了就当作从未安装：下次 sync 会重新判定用户 JSON 里
			// 那几个键的归属，判不出是我方的就报冲突，不会误改用户配置。
			continue
		}
		seen[root] = true
		out = append(out, scanRoot{dir: filepath.Join(p.ClaudeHome, root), probe: k})
	}
	return out
}

func (l *Lock) rebuildRoot(p Paths, s Resolver, r scanRoot) (int, error) {
	entries, err := os.ReadDir(r.dir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	shape := r.probe.Shape()
	ext := r.probe.Ext()
	n := 0
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if shape == core.ShapeFile {
			if e.IsDir() || !strings.HasSuffix(name, ext) {
				continue
			}
			name = strings.TrimSuffix(name, ext)
		}
		target := p.Target(r.probe, name)
		info, err := s.For(shape).Inspect(target, p.StoreCtx(r.probe, name))
		if err != nil || info.Ownership != core.OwnMine {
			continue
		}

		kind := r.probe
		entry := core.LockEntry{Name: name, Target: target, Shape: shape}
		switch {
		case info.Marker != nil:
			// copy 策略（目录形态）：marker 是一次物化的记录，信息最全（含版本号与 kind）
			if k := core.Kind(info.Marker.Kind); k.Valid() {
				kind = k
			}
			entry.BundleID = info.Marker.BundleID
			entry.Version = info.Marker.Version
			entry.Checksum = info.Marker.Checksum
			entry.Store = filepath.Join(p.StoreCtx(kind, name).BundleDir, core.ShortSum(info.Marker.Checksum))
		case info.StoreDir != "":
			// link 策略，或文件形态的内容比对命中：
			// 由路径反推 kind 与内容摘要。版本号拿不到，
			// 留 0 由 sync 按 checksum 对照 manifest 补齐。
			if k := kindFromStorePath(p.Store, info.StoreDir); k.Valid() {
				kind = k
			}
			entry.Store = info.StoreDir
			entry.ShortSum = filepath.Base(info.StoreDir)
		}
		entry.Kind = kind
		l.Bundles[core.MakeRef(kind, name)] = entry
		n++
	}
	return n, nil
}

// kindFromStorePath 由 store/<kind>/<name>/<sum> 反推 kind。
//
// 这是 skill 与 command 唯一可靠的区分依据：两者在 claude_home 里
// 长得一模一样，差别只存在于我们自己的 store 布局中。
func kindFromStorePath(storeRoot, dir string) core.Kind {
	rel, err := filepath.Rel(storeRoot, dir)
	if err != nil {
		return ""
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 1 {
		return ""
	}
	return core.Kind(parts[0])
}

// Resolver 按形态给出策略，是 Rebuild 与 Ownerships 需要的最小能力。
// Strategies 实现了它；测试可以塞替身。
type Resolver interface {
	For(shape core.Shape) link.Strategy
}

// Ownerships 逐个探测目标归属，产出 core.Plan 需要的输入。
//
// refs 是 kind/name，kind 直接给出形态、目标路径与 store 入口文件名，
// 因此不再需要外部传入 shape 表。
func Ownerships(p Paths, s Resolver, m *Merger, refs []core.Ref, lock *Lock) (map[core.Ref]core.Ownership, error) {
	out := make(map[core.Ref]core.Ownership, len(refs))
	for _, ref := range refs {
		kind, name := ref.Split()
		if kind.Shape() == core.ShapeMerge {
			// 合并形态没有入口可 lstat，归属只能靠「用户 JSON 里那几个键
			// 的当前值是否仍等于我方上次写入的值」来判（见 merge.go）。
			own, err := m.Inspect(kind, lock.Bundles[ref])
			if err != nil {
				return nil, fmt.Errorf("探测 %s: %w", ref, err)
			}
			out[ref] = own
			continue
		}
		info, err := s.For(kind.Shape()).Inspect(p.Target(kind, name), p.StoreCtx(kind, name))
		if err != nil {
			return nil, fmt.Errorf("探测 %s: %w", ref, err)
		}
		out[ref] = info.Ownership
	}
	return out, nil
}
