package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/ith5/ith5/internal/core"
)

// Merger 处理合并形态（setting / mcp）的落地。
//
// 这是 ITH5 唯一会写进**用户自有文件**的路径，因此它比其余形态多一层
// 克制：所有判断都以「拿不准就不碰」收尾。
//
// 与指针形态的根本差别在归属判定。skills/<name> 是一个我方独占的入口，
// lstat 一下就知道是不是我们的；而 settings.json 是用户自己的文件，
// 我方写入的只是其中几个顶层键，旁边还并排放着用户自己的配置。
// 这里没有指针可读、没有地方放 marker，唯一可用的证据是：
//
//	这几个键的当前值，是否仍逐字节等于我方上次写进去的值？
//
// 相等即视为我方所有（OwnMine）；不等即认为用户改过（OwnForeign），
// 从此不再覆盖也不再删除，只在同步结果里报冲突。键不存在则是 OwnAbsent。
//
// 这个判据会有一种误判：用户把值改回了和我方完全一致的内容。那时我们
// 会认为它是我方的并继续管理它——后果只是「他手写的配置被我方接管更新」，
// 与他原本想要的结果一致，不造成损失。反方向的误判（把用户的配置当成
// 我方的删掉）则被彻底排除，那才是不可接受的那一侧。
type Merger struct {
	Paths Paths
	Store *Store
}

// 合并的粒度是**顶层键**。
//
// 不做深层合并是刻意的：settings.json 里 hooks、env、permissions 都是
// 结构化的嵌套值，逐层合并会产生「一半是用户的、一半是我方的」的复合
// 对象——那种东西撤权时无法干净摘除，只能靠猜。以顶层键为单位，
// 每个键要么整个属于我方，要么整个属于用户，没有中间态。
//
// 唯一的例外是 mcp：它的顶层键固定是 mcpServers，真正的管理单元是它
// **下面一层**的每个 server 名。否则两个 MCP bundle 会互相覆盖整个
// mcpServers 对象。
func (m *Merger) keysOf(kind core.Kind, fragment map[string]any) []string {
	if kind == core.KindMCP {
		return sortedKeys(mcpServers(fragment))
	}
	return sortedKeys(fragment)
}

// get 读出用户 JSON 中某个管理单元的当前值。
func (m *Merger) get(kind core.Kind, root map[string]any, key string) (any, bool) {
	if kind == core.KindMCP {
		v, ok := mcpServers(root)[key]
		return v, ok
	}
	v, ok := root[key]
	return v, ok
}

// set 写入一个管理单元。
func (m *Merger) set(kind core.Kind, root map[string]any, key string, val any) {
	if kind == core.KindMCP {
		servers, _ := root["mcpServers"].(map[string]any)
		if servers == nil {
			servers = map[string]any{}
			root["mcpServers"] = servers
		}
		servers[key] = val
		return
	}
	root[key] = val
}

// del 删除一个管理单元。
func (m *Merger) del(kind core.Kind, root map[string]any, key string) {
	if kind == core.KindMCP {
		servers, _ := root["mcpServers"].(map[string]any)
		delete(servers, key)
		// 我方删空了就把空壳一并去掉，不留 "mcpServers": {} 这种残渣
		if len(servers) == 0 {
			delete(root, "mcpServers")
		}
		return
	}
	delete(root, key)
}

// Inspect 判定合并形态 bundle 的归属。
//
// locked 是 lock 里记的上一版；它的 Store 指向我方上次写入值的来源目录。
// 没有 lock（首装，或 lock 丢失）时，只要目标键已存在就判 OwnForeign
// ——我们无从证明那是自己写的，就不能动它。
func (m *Merger) Inspect(kind core.Kind, locked core.LockEntry) (core.Ownership, error) {
	root, err := readUserJSON(m.Paths.MergeFile(kind))
	if err != nil {
		return core.OwnAbsent, err
	}

	if locked.Store == "" || len(locked.MergeKeys) == 0 {
		// 没有「我方写过什么」的记录。目标键还不存在就是干净的，
		// 已存在则是用户的东西（或上一次装的残留，同样不该盲目覆盖）。
		for _, k := range keysFromLockOrFile(kind, locked, root) {
			if _, ok := m.get(kind, root, k); ok {
				return core.OwnForeign, nil
			}
		}
		return core.OwnAbsent, nil
	}

	want, err := m.fragmentAt(kind, locked.Store)
	if err != nil {
		// 上一版的内容目录被清掉了（例如手工删了 store）。
		// 拿不出比对基准就不能声称所有权。
		return core.OwnForeign, nil
	}

	present := 0
	for _, k := range locked.MergeKeys {
		cur, ok := m.get(kind, root, k)
		if !ok {
			continue
		}
		present++
		exp, hasExp := m.get(kind, want, k)
		if !hasExp || !reflect.DeepEqual(cur, exp) {
			return core.OwnForeign, nil
		}
	}
	if present == 0 {
		// 我方写过，但键已经不在了——用户手工删掉了。
		// 判 Absent 让它按 install 重新装回来：这与他删掉一个
		// skills/<name> 目录后 sync 会重新装回的行为一致。
		return core.OwnAbsent, nil
	}
	return core.OwnMine, nil
}

// Apply 把一个版本的片段合并进用户 JSON，返回本次实际写入的键。
//
// 只在 Inspect 判定为 OwnAbsent 或 OwnMine 时调用。写入前备份，
// 写入用「临时文件 + rename」，与 lock 的落盘方式一致。
func (m *Merger) Apply(kind core.Kind, storeDir string, prev core.LockEntry) ([]string, error) {
	fragment, err := m.fragmentAt(kind, storeDir)
	if err != nil {
		return nil, err
	}
	path := m.Paths.MergeFile(kind)
	root, err := readUserJSON(path)
	if err != nil {
		return nil, err
	}

	// 上一版写过、这一版不再包含的键要撤掉，否则内容删了键还留着。
	// 同样只在「值仍是我方上次写的」时才撤。
	newKeys := m.keysOf(kind, fragment)
	if len(prev.MergeKeys) > 0 && prev.Store != "" {
		if old, err := m.fragmentAt(kind, prev.Store); err == nil {
			for _, k := range prev.MergeKeys {
				if contains(newKeys, k) {
					continue
				}
				cur, ok := m.get(kind, root, k)
				exp, hasExp := m.get(kind, old, k)
				if ok && hasExp && reflect.DeepEqual(cur, exp) {
					m.del(kind, root, k)
				}
			}
		}
	}

	for _, k := range newKeys {
		v, _ := m.get(kind, fragment, k)
		m.set(kind, root, k, v)
	}
	if err := writeUserJSON(path, root); err != nil {
		return nil, err
	}
	return newKeys, nil
}

// Release 撤销一次合并。
//
// 只删「当前值仍等于我方写入值」的键。用户改过的一律留着——
// 这与 PRD「切断续期，不保证擦除」的立场一致：撤权停止的是后续更新，
// 不是强行改回用户的配置文件。
func (m *Merger) Release(kind core.Kind, locked core.LockEntry) error {
	if len(locked.MergeKeys) == 0 || locked.Store == "" {
		return nil
	}
	want, err := m.fragmentAt(kind, locked.Store)
	if err != nil {
		return nil
	}
	path := m.Paths.MergeFile(kind)
	root, err := readUserJSON(path)
	if err != nil {
		return err
	}
	changed := false
	for _, k := range locked.MergeKeys {
		cur, ok := m.get(kind, root, k)
		exp, hasExp := m.get(kind, want, k)
		if ok && hasExp && reflect.DeepEqual(cur, exp) {
			m.del(kind, root, k)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return writeUserJSON(path, root)
}

// fragmentAt 读出 store 某个内容目录里的 JSON 片段。
func (m *Merger) fragmentAt(kind core.Kind, storeDir string) (map[string]any, error) {
	b, err := os.ReadFile(filepath.Join(storeDir, kind.EntryFile()))
	if err != nil {
		return nil, err
	}
	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil {
		return nil, fmt.Errorf("%s 不是合法的 JSON 对象: %w", storeDir, err)
	}
	return obj, nil
}

// keysFromLockOrFile 在没有比对基准时，尽力给出「我方会碰哪些键」。
// lock 里记过就用 lock 的；否则无从得知，返回空——空集合意味着
// Inspect 判 OwnAbsent，随后 Apply 会真正读到片段并逐键处理。
func keysFromLockOrFile(kind core.Kind, locked core.LockEntry, _ map[string]any) []string {
	return locked.MergeKeys
}

func mcpServers(root map[string]any) map[string]any {
	s, _ := root["mcpServers"].(map[string]any)
	if s == nil {
		return map[string]any{}
	}
	return s
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}

// readUserJSON 读一个用户自有的 JSON 配置文件；不存在时返回空对象。
//
// 解析失败必须报错而不是当作空对象：把一个语法坏掉的 settings.json
// 当成空的，然后写回一份只含我方内容的文件，等于抹掉用户的全部配置。
func readUserJSON(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return map[string]any{}, nil
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		return nil, fmt.Errorf("%s 无法解析，请先修复或备份后删除: %w", path, err)
	}
	if root == nil {
		root = map[string]any{}
	}
	return root, nil
}

// writeUserJSON 备份原文件后原子写回。
//
// 与 config.go 的 writeJSONFile 分开，是因为多了一步备份：那个函数写的是
// ITH5 自己的文件（凭据、配置），写坏了重新登录即可；这个函数写的是用户
// 的 settings.json，写坏了他损失的是自己的配置。
func writeUserJSON(path string, root map[string]any) error {
	if b, err := os.ReadFile(path); err == nil {
		// 每次写入都刷新备份，与 InstallHooks 的做法一致
		if err := os.WriteFile(path+".ith5.bak", b, 0o644); err != nil {
			return fmt.Errorf("备份 %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".ith5.tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReleaseEntry 释放一条 lock 记录占用的目标，按形态分派。
//
// 撤权清理与 --purge 都要走它：少一个分支，合并形态写进用户
// settings.json 的键就会在「已清理」的提示下被静默留下。
func ReleaseEntry(p Paths, st Strategies, ref core.Ref, e core.LockEntry) error {
	kind, name := ref.Split()
	if e.Kind != "" {
		kind = e.Kind
	}
	if e.Name != "" {
		name = e.Name
	}
	if kind.Shape() == core.ShapeMerge {
		return (&Merger{Paths: p}).Release(kind, e)
	}
	return st.For(kind.Shape()).Release(p.Target(kind, name), p.StoreCtx(kind, name))
}
