package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ith5/ith5/internal/api"
	"github.com/ith5/ith5/internal/cli/link"
	"github.com/ith5/ith5/internal/core"
)

// StoreKeep 是每个 bundle 在 store 中保留的内容目录数上限（D11）。
// 按内容而非版本号计数——内容相同的多个版本只占一格。
const StoreKeep = 3

// SyncResult 是一次同步的结果摘要。
type SyncResult struct {
	Summary    core.Summary
	Conflicts  []Conflict
	Rebuilt    int  // 由指针/marker 重建的 lock 条目数
	NotModifi  bool // manifest 命中 304
	EventsSent int  // 本次顺带上传的 L0 事件数
}

// Conflict 记录一次「目标被用户自有内容占用」。
type Conflict struct {
	Name   string
	Target string
	// Reason 是受阻成因：name_taken（重名）或 locally_modified（本地改动）。
	// 两者的处置一样（都不碰），但排查方向完全不同，必须让管理员看到。
	Reason string
}

// Syncer 编排一次同步。
type Syncer struct {
	Paths      Paths
	Store      *Store
	Strategies Strategies
	Client     *Client
	Server     string
}

// Run 执行同步（技术方案 §8.4）。
//
// 顺序刻意如此：
//  1. 先清 staging/trash 的残留 —— store 是真相，残留无条件可删
//  2. 读 lock；缺失或损坏则由指针/marker 重建，而不是进入只读保护模式
//  3. 拉 manifest（带 ETag）
//  4. 探测每个目标的归属 —— 这是不碰用户文件的唯一关口
//  5. core.Plan 算差集（纯函数）
//  6. 逐项执行，成功后才回执
func (s *Syncer) Run(ctx context.Context) (SyncResult, error) {
	var res SyncResult

	if err := s.Paths.EnsureDirs(); err != nil {
		return res, err
	}
	if err := s.Paths.CleanScratch(); err != nil {
		return res, err
	}

	lock, needRebuild, err := LoadLock(s.Paths.LockFile())
	if err != nil {
		return res, err
	}
	if needRebuild {
		n, err := lock.Rebuild(s.Paths, s.Strategies)
		if err != nil {
			return res, fmt.Errorf("重建 lock: %w", err)
		}
		res.Rebuilt = n
		lock.Server = s.Server
	}

	manifest, etag, changed, err := s.Client.Manifest(ctx, "")
	if err != nil {
		return res, err
	}
	_ = etag
	res.NotModifi = !changed

	// 重建出来的条目只有 checksum，没有版本号——用 manifest 对照补齐
	reconcileVersions(lock, manifest)

	metas := make([]core.BundleMeta, 0, len(manifest.Bundles))
	names := make([]string, 0, len(manifest.Bundles))
	shapes := make(map[string]core.Shape, len(manifest.Bundles))
	for _, b := range manifest.Bundles {
		kind := core.Kind(b.Kind)
		metas = append(metas, core.BundleMeta{
			ID: b.ID, Name: b.Name, Kind: kind,
			Version: b.Version, Checksum: b.Checksum, Description: b.Description,
		})
		names = append(names, b.Name)
		shapes[b.Name] = kind.Shape()
	}
	for n, e := range lock.Bundles {
		names = append(names, n)
		// manifest 里没有的（撤权待删）只能按 lock 记的形态探测。
		// 这正是 LockEntry.Shape 必须显式记录的原因：这里没有 manifest 可查。
		if _, ok := shapes[n]; !ok {
			shape := e.Shape
			if shape == "" {
				shape = e.Kind.Shape()
			}
			shapes[n] = shape
		}
	}

	own, err := Ownerships(s.Paths, s.Strategies, names, shapes)
	if err != nil {
		return res, err
	}

	plan := core.Plan(metas, lock.Bundles, own)
	res.Summary = core.Summarize(plan)

	byID := map[string]api.ManifestBundle{}
	for _, b := range manifest.Bundles {
		byID[b.ID] = b
	}

	var receipts []api.DistributionEventJSON
	for _, item := range plan {
		switch item.Action {
		case core.ActionUnchanged:
			continue

		case core.ActionConflict:
			target := s.Paths.Target(item.Shape, item.Name)
			reason := link.ReasonNameTaken
			if info, err := s.Strategies.For(item.Shape).Inspect(target, s.Paths.Store); err == nil && info.Reason != "" {
				reason = info.Reason
			}
			res.Conflicts = append(res.Conflicts, Conflict{Name: item.Name, Target: target, Reason: reason})
			receipts = append(receipts, receipt(item, "conflict_skipped", map[string]any{
				"target":          s.Paths.RelTarget(item.Shape, item.Name),
				"conflict_reason": reason,
			}))
			// 冲突时把它从 lock 里摘掉：我们并不管理这个入口
			delete(lock.Bundles, item.Name)

		case core.ActionRemove:
			if err := s.Strategies.For(item.Shape).Release(s.Paths.Target(item.Shape, item.Name)); err != nil {
				return res, fmt.Errorf("释放 %s: %w", item.Name, err)
			}
			delete(lock.Bundles, item.Name)
			receipts = append(receipts, receipt(item, "remove", nil))

		case core.ActionRelabel:
			// 内容未变，只有版本号变了（回滚）。零文件操作。
			e := lock.Bundles[item.Name]
			e.Version = item.Version
			e.BundleID = item.BundleID
			lock.Bundles[item.Name] = e
			receipts = append(receipts, receipt(item, "update", map[string]any{"relabel": true}))

		case core.ActionInstall, core.ActionUpdate:
			if err := s.materialize(ctx, item, byID[item.BundleID], lock); err != nil {
				return res, err
			}
			receipts = append(receipts, receipt(item, string(item.Action), nil))
		}
	}

	lock.Strategy = s.Strategies.Dir.ID()
	lock.Server = s.Server
	lock.SyncedAt = time.Now()
	lock.TTLSeconds = manifest.TTLSeconds
	if err := lock.Save(s.Paths.LockFile()); err != nil {
		return res, err
	}

	// 只有本地动作成功且新 lock 已落盘后才回执（技术方案 §9）
	if _, err := s.Client.ReportEvents(ctx, receipts); err != nil {
		// 回执失败不影响本地状态，下次 sync 会重报（event_id 幂等）
		return res, nil
	}
	// 顺带把 L0 队列送出去。失败同样不影响同步结果——
	// 事件留在本地，下次补传（技术方案 §2 原则 6）。
	if n, err := FlushEvents(ctx, s.Paths, s.Client); err == nil {
		res.EventsSent = n
	}
	return res, nil
}

// materialize 下载（若需要）并切换指针。
func (s *Syncer) materialize(ctx context.Context, item core.PlanItem, mb api.ManifestBundle, lock *Lock) error {
	// 内容已在 store 里就不下载——回滚与重发相同内容时命中这里
	if !s.Store.Has(item.Shape, item.Name, item.Checksum) {
		body, err := s.Client.BundleVersion(ctx, item.BundleID, item.Version)
		if err != nil {
			return fmt.Errorf("下载 %s: %w", item.Name, err)
		}
		files := make([]core.File, len(body.Files))
		for i, f := range body.Files {
			files[i] = core.File{Path: f.Path, Content: f.Content}
		}
		if err := s.Store.Write(item.Kind, item.Name, item.Checksum, files); err != nil {
			return fmt.Errorf("写入 %s: %w", item.Name, err)
		}
	}

	strategy := s.Strategies.For(item.Shape)
	dir := s.Store.Dir(item.Name, item.Checksum)
	target := s.Paths.Target(item.Shape, item.Name)
	md := link.MarkerData{
		BundleID: item.BundleID, BundleName: item.Name,
		Version: item.Version, Checksum: item.Checksum, Strategy: strategy.ID(),
	}
	if err := strategy.Materialize(dir, target, md); err != nil {
		return fmt.Errorf("切换 %s 的入口: %w", item.Name, err)
	}

	lock.Bundles[item.Name] = core.LockEntry{
		BundleID: item.BundleID, Kind: item.Kind, Shape: item.Shape, Version: item.Version,
		Checksum: item.Checksum, Target: target, Store: dir,
	}
	_ = s.Store.Prune(item.Name, []string{item.Checksum}, StoreKeep)
	return nil
}

// reconcileVersions 用 manifest 补齐重建 lock 时拿不到的版本号。
//
// 内容目录按摘要寻址，不携带版本号（同一目录被多个版本共享），
// 因此重建只能拿到摘要，版本号必须由 manifest 对照（技术方案 §8.8）。
func reconcileVersions(lock *Lock, m api.ManifestResp) {
	for _, b := range m.Bundles {
		e, ok := lock.Bundles[b.Name]
		if !ok || e.Version != 0 || e.ShortSum == "" {
			continue
		}
		if core.ShortSum(b.Checksum) == e.ShortSum {
			e.BundleID = b.ID
			e.Kind = core.Kind(b.Kind)
			e.Shape = core.Kind(b.Kind).Shape()
			e.Version = b.Version
			e.Checksum = b.Checksum
			e.ShortSum = ""
			lock.Bundles[b.Name] = e
		}
	}
}

func receipt(item core.PlanItem, action string, detail map[string]any) api.DistributionEventJSON {
	return api.DistributionEventJSON{
		EventID:    uuid.NewString(),
		BundleID:   item.BundleID,
		Version:    item.Version,
		Action:     action,
		Detail:     detail,
		OccurredAt: time.Now().UTC(),
	}
}
