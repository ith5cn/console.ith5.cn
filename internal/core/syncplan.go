package core

import "sort"

// Action 是同步计划中对单个 Bundle 的动作（技术方案 §8.7）。
type Action string

const (
	ActionInstall Action = "install"
	ActionUpdate  Action = "update"
	ActionRemove  Action = "remove"
	// ActionRelabel：内容未变但版本号变了——这是**回滚**的形态
	// （回滚 = 用旧内容发布新版本，checksum 必然相同）。
	// 只需更新 lock 元数据并回执，零文件操作、零下载。
	//
	// 若把这种情况判为 unchanged，本地 lock 的 version 会永久停在旧值，
	// 后台「谁拿了哪个版本」从此对不上——而回滚恰恰最需要审计准确。
	ActionRelabel   Action = "relabel"
	ActionUnchanged Action = "unchanged"
	// ActionConflict：目标已存在但不属于我方，跳过并回执 conflict_skipped。
	ActionConflict Action = "conflict_skipped"
)

// Ownership 是目标入口的归属判定结果（技术方案 §8.5.1）。
// 由调用方通过一次 lstat（link 策略）或读 marker（copy 策略）得出，
// core 不触碰文件系统。
type Ownership int

const (
	// OwnAbsent 目标不存在。
	OwnAbsent Ownership = iota
	// OwnMine 目标是指向我方 store 的指针，或含我方 marker 的目录。
	OwnMine
	// OwnForeign 其他任何情况——用户自有，绝不覆盖。
	OwnForeign
)

// LockEntry 是 lock.json 中的一条（技术方案 §8.8）。
// lock 是缓存而非真相，可由指针或 marker 重建。
type LockEntry struct {
	BundleID string `json:"id"`
	Kind     Kind   `json:"kind"`
	// Shape 必须显式记录，不能由 Kind 反推（技术方案 §8.8）：
	// 二者今天一一对应，但 Kind 是服务端的展示概念、Shape 是客户端的落盘契约。
	// 少记这个字段，Release 就得靠 Kind 猜该 unlink 还是 rmdir——
	// 那正是 §8.6 那个「递归删入 store」陷阱的复发路径。
	Shape    Shape  `json:"shape"`
	Version  int    `json:"version"`
	Checksum string `json:"checksum"`
	Target   string `json:"target"`
	Store    string `json:"store"`
	// ShortSum 仅在 lock 由指针重建、尚未与 manifest 对照时有值：
	// 内容目录不携带版本号（同一目录被多个版本共享），因此重建只能
	// 拿到摘要，版本号需按 checksum 对照 manifest 补齐（技术方案 §8.8）。
	ShortSum string `json:"-"`
}

// PlanItem 是同步计划中的一项。
type PlanItem struct {
	Name     string
	Action   Action
	BundleID string
	Kind     Kind
	Shape    Shape
	// Version/Checksum 在 install/update 时是目标版本，
	// 在 remove 时是本地当前版本。
	Version  int
	Checksum string
}

// Plan 计算 manifest 与本地状态的差集。
//
// 纯函数：manifest 来自服务端，lock 来自本地缓存，own 来自文件系统探测，
// 三者都由调用方准备好传入。
//
// own 的键是 bundle 名。缺失的键按 OwnAbsent 处理。
//
// **调用方职责**：lock 必须在调用 Plan 之前完成重建（技术方案 §8.8——
// 由指针的 readlink 或 copy 策略的 marker 反推）。Plan 收到的 lock
// 应当已经是重建后的结果。因此 “有归属但无 lock 记录” 在这里是异常路径，
// 按 update 兜底对齐到 manifest 版本。
func Plan(manifest []BundleMeta, lock map[string]LockEntry, own map[string]Ownership) []PlanItem {
	items := make([]PlanItem, 0, len(manifest)+len(lock))
	inManifest := make(map[string]bool, len(manifest))

	for _, m := range manifest {
		inManifest[m.Name] = true
		locked, hasLock := lock[m.Name]
		ownership := own[m.Name]

		item := PlanItem{
			Name:     m.Name,
			BundleID: m.ID,
			Kind:     m.Kind,
			Shape:    m.Kind.Shape(),
			Version:  m.Version,
			Checksum: m.Checksum,
		}

		switch {
		case ownership == OwnForeign:
			// 用户自有文件优先于一切，即便 lock 声称我们拥有它。
			// 这种不一致通常是用户手工替换了目录。
			item.Action = ActionConflict
		case !hasLock:
			if ownership == OwnAbsent {
				item.Action = ActionInstall
			} else {
				// 有归属但无 lock 记录：lock 丢失或损坏。
				// 归属判定说明是我方的，按 update 走以对齐到 manifest 版本。
				item.Action = ActionUpdate
			}
		case ownership == OwnAbsent:
			// lock 说装过，但目标不见了（用户删了、或 store 被清）。
			item.Action = ActionInstall
		case locked.Checksum != m.Checksum:
			item.Action = ActionUpdate
		case locked.Version != m.Version:
			// checksum 相同、version 不同 → 回滚（或重发相同内容）。
			item.Action = ActionRelabel
		default:
			item.Action = ActionUnchanged
		}
		items = append(items, item)
	}

	// lock 有、manifest 无 → 撤权或归档，执行 remove。
	for name, l := range lock {
		if inManifest[name] {
			continue
		}
		action := ActionRemove
		if own[name] == OwnForeign {
			// 目标已被用户占用，不碰它，只从 lock 里摘掉。
			action = ActionConflict
		}
		// remove 的形态取自 lock 的显式记录，而不是由 Kind 反推：
		// 要删的东西已经不在 manifest 里了，本地记的那份才是它实际的落盘方式。
		shape := l.Shape
		if shape == "" {
			shape = l.Kind.Shape()
		}
		items = append(items, PlanItem{
			Name:     name,
			Action:   action,
			BundleID: l.BundleID,
			Kind:     l.Kind,
			Shape:    shape,
			Version:  l.Version,
			Checksum: l.Checksum,
		})
	}

	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

// Summary 汇总计划，用于 CLI 输出与回执统计。
type Summary struct {
	Install   int
	Update    int
	Relabel   int
	Remove    int
	Unchanged int
	Conflict  int
}

func Summarize(items []PlanItem) Summary {
	var s Summary
	for _, it := range items {
		switch it.Action {
		case ActionInstall:
			s.Install++
		case ActionUpdate:
			s.Update++
		case ActionRelabel:
			s.Relabel++
		case ActionRemove:
			s.Remove++
		case ActionUnchanged:
			s.Unchanged++
		case ActionConflict:
			s.Conflict++
		}
	}
	return s
}
