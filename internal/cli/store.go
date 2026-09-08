package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/ith5/ith5/internal/core"
)

// Store 是本地内容仓库，也是同步的唯一真相（技术方案 §8.0 的 I1）。
//
// 布局：~/.ith5/store/<bundle>/<checksum前16位>/
// 目录一旦写成即不可变（I2），因此不存在需要恢复的中间态——
// 写坏了整目录删掉重来即可，不需要事务日志或回滚备份。
type Store struct {
	root string
}

func NewStore(root string) *Store { return &Store{root: root} }

// Dir 返回某个内容版本的绝对路径。
func (s *Store) Dir(bundleName, checksum string) string {
	return filepath.Join(s.root, bundleName, core.ShortSum(checksum))
}

// Has 判断该内容是否已在本地。
//
// 回滚时命中这里即意味着零下载：回滚是「用旧内容发布新版本」，
// 新旧版本 checksum 相同，指向同一个内容目录。
func (s *Store) Has(shape core.Shape, bundleName, checksum string) bool {
	if core.ShortSum(checksum) == "" {
		return false
	}
	fi, err := os.Stat(filepath.Join(s.Dir(bundleName, checksum), shape.EntryFile()))
	return err == nil && fi.Mode().IsRegular()
}

// Write 把一个版本的文件写入 store。
//
// 已存在则直接返回（内容不可变，无需覆盖）。写入过程用临时目录 +
// 整体 rename，失败时把临时目录整个删掉，不留半成品。
func (s *Store) Write(kind core.Kind, bundleName, checksum string, files []core.File) error {
	if err := core.ValidateName(bundleName); err != nil {
		return err
	}
	// 服务端已经校验过，这里再校验一次：内容从网络来，落到用户机器上，
	// 中间任何一环出问题都不该由用户承担。
	if err := core.ValidateFiles(kind, files); err != nil {
		return fmt.Errorf("%s: %w", bundleName, err)
	}
	// 落盘前再算一次 checksum：服务端说什么不算数，本地校验才算
	ok, err := core.VerifyChecksum(files, checksum)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s: checksum 校验失败，拒绝落盘", bundleName)
	}
	if s.Has(kind.Shape(), bundleName, checksum) {
		return nil
	}

	final := s.Dir(bundleName, checksum)
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(final), ".tmp-*")
	if err != nil {
		return err
	}
	// 任何一步失败都整体删除，不留中间态
	success := false
	defer func() {
		if !success {
			os.RemoveAll(tmp)
		}
	}()

	for _, f := range files {
		dst := filepath.Join(tmp, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, []byte(f.Content), 0o644); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, final); err != nil {
		// 并发写同一内容时对方可能已经建好，这是可接受的竞态
		if s.Has(kind.Shape(), bundleName, checksum) {
			success = true
			return nil
		}
		return fmt.Errorf("提交内容目录: %w", err)
	}
	success = true
	return nil
}

// Prune 只保留指定的内容目录，其余删除。
//
// keep 是 checksum 列表；按内容而非版本号计数，因此内容相同的多个版本
// 只占一格（D11）。
func (s *Store) Prune(bundleName string, keep []string, maxKeep int) error {
	dir := filepath.Join(s.root, bundleName)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	protected := make(map[string]bool, len(keep))
	for _, c := range keep {
		protected[core.ShortSum(c)] = true
	}

	type ent struct {
		name string
		mod  int64
	}
	var candidates []ent
	for _, e := range entries {
		if !e.IsDir() || protected[e.Name()] {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		candidates = append(candidates, ent{e.Name(), info.ModTime().UnixNano()})
	}
	// 新的留下，旧的先删
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].mod > candidates[j].mod })

	room := maxKeep - len(protected)
	for i, c := range candidates {
		if i < room {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, c.name)); err != nil {
			return err
		}
	}
	return nil
}

// Root 返回 store 根目录，供 ownership 判定使用。
func (s *Store) Root() string { return s.root }
