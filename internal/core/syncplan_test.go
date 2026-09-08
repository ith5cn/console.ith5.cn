package core

import "testing"

func m(name, id string, ver int, sum string) BundleMeta {
	return BundleMeta{ID: id, Name: name, Kind: KindSkill, Version: ver, Checksum: sum}
}
func l(id string, ver int, sum string) LockEntry {
	return LockEntry{BundleID: id, Kind: KindSkill, Version: ver, Checksum: sum}
}

func actionOf(items []PlanItem, name string) Action {
	for _, it := range items {
		if it.Name == name {
			return it.Action
		}
	}
	return Action("<missing>")
}

func TestPlan_Install(t *testing.T) {
	items := Plan(
		[]BundleMeta{m("a", "b1", 1, "c1")},
		map[string]LockEntry{},
		map[string]Ownership{},
	)
	if got := actionOf(items, "a"); got != ActionInstall {
		t.Fatalf("got %v", got)
	}
}

func TestPlan_Update(t *testing.T) {
	items := Plan(
		[]BundleMeta{m("a", "b1", 2, "c2")},
		map[string]LockEntry{"a": l("b1", 1, "c1")},
		map[string]Ownership{"a": OwnMine},
	)
	if got := actionOf(items, "a"); got != ActionUpdate {
		t.Fatalf("got %v", got)
	}
}

func TestPlan_Unchanged(t *testing.T) {
	items := Plan(
		[]BundleMeta{m("a", "b1", 1, "c1")},
		map[string]LockEntry{"a": l("b1", 1, "c1")},
		map[string]Ownership{"a": OwnMine},
	)
	if got := actionOf(items, "a"); got != ActionUnchanged {
		t.Fatalf("got %v", got)
	}
}

func TestPlan_Remove(t *testing.T) {
	items := Plan(
		nil,
		map[string]LockEntry{"a": l("b1", 1, "c1")},
		map[string]Ownership{"a": OwnMine},
	)
	if got := actionOf(items, "a"); got != ActionRemove {
		t.Fatalf("got %v", got)
	}
}

// 用户自有文件优先于一切，即便 lock 声称我们拥有它。
func TestPlan_ForeignAlwaysWins(t *testing.T) {
	t.Run("install 时冲突", func(t *testing.T) {
		items := Plan(
			[]BundleMeta{m("a", "b1", 1, "c1")},
			map[string]LockEntry{},
			map[string]Ownership{"a": OwnForeign},
		)
		if got := actionOf(items, "a"); got != ActionConflict {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("update 时冲突", func(t *testing.T) {
		items := Plan(
			[]BundleMeta{m("a", "b1", 2, "c2")},
			map[string]LockEntry{"a": l("b1", 1, "c1")},
			map[string]Ownership{"a": OwnForeign},
		)
		if got := actionOf(items, "a"); got != ActionConflict {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("remove 时不碰用户目录", func(t *testing.T) {
		items := Plan(
			nil,
			map[string]LockEntry{"a": l("b1", 1, "c1")},
			map[string]Ownership{"a": OwnForeign},
		)
		if got := actionOf(items, "a"); got != ActionConflict {
			t.Fatalf("撤权时若目标已被用户占用，只摘 lock 不删目录，got %v", got)
		}
	})
}

// lock 丢失但目标是我方的 → 按 update 对齐，而非 install（避免 install 误判冲突）。
func TestPlan_LockLostButOwned(t *testing.T) {
	items := Plan(
		[]BundleMeta{m("a", "b1", 1, "c1")},
		map[string]LockEntry{},
		map[string]Ownership{"a": OwnMine},
	)
	if got := actionOf(items, "a"); got != ActionUpdate {
		t.Fatalf("got %v", got)
	}
}

// lock 说装过但目标不见了（用户删了目录，或 store 被清）→ 重新安装。
func TestPlan_LockPresentButTargetGone(t *testing.T) {
	items := Plan(
		[]BundleMeta{m("a", "b1", 1, "c1")},
		map[string]LockEntry{"a": l("b1", 1, "c1")},
		map[string]Ownership{"a": OwnAbsent},
	)
	if got := actionOf(items, "a"); got != ActionInstall {
		t.Fatalf("got %v", got)
	}
}

func TestPlan_SortedByName(t *testing.T) {
	items := Plan(
		[]BundleMeta{m("c", "b3", 1, "x"), m("a", "b1", 1, "x"), m("b", "b2", 1, "x")},
		map[string]LockEntry{},
		map[string]Ownership{},
	)
	want := []string{"a", "b", "c"}
	for i, w := range want {
		if items[i].Name != w {
			t.Fatalf("排序不稳定: %v", items)
		}
	}
}

func TestSummarize(t *testing.T) {
	items := Plan(
		[]BundleMeta{m("a", "b1", 1, "c1"), m("b", "b2", 2, "c2"), m("c", "b3", 1, "c3")},
		map[string]LockEntry{
			"b": l("b2", 1, "old"),
			"c": l("b3", 1, "c3"),
			"d": l("b4", 1, "c4"),
		},
		map[string]Ownership{"b": OwnMine, "c": OwnMine, "d": OwnMine},
	)
	s := Summarize(items)
	if s.Install != 1 || s.Update != 1 || s.Unchanged != 1 || s.Remove != 1 || s.Conflict != 0 {
		t.Fatalf("got %+v", s)
	}
}

// 回滚 = 用旧内容发布新版本，因此 checksum 相同而 version 不同。
// 这曾被判为 unchanged，导致本地 lock 的 version 永久停在旧值，
// 后台「谁拿了哪个版本」从此对不上。
func TestPlan_Rollback_ChecksumSameVersionDiffers(t *testing.T) {
	items := Plan(
		[]BundleMeta{m("a", "b1", 15, "c1")}, // v15 是对 v13 的回滚，内容相同
		map[string]LockEntry{"a": l("b1", 13, "c1")},
		map[string]Ownership{"a": OwnMine},
	)
	if got := actionOf(items, "a"); got != ActionRelabel {
		t.Fatalf("回滚必须判为 relabel（更新元数据+回执，零文件操作），got %v", got)
	}
	for _, it := range items {
		if it.Name == "a" && it.Version != 15 {
			t.Fatalf("relabel 必须携带目标版本号 15，got %d", it.Version)
		}
	}
}

func TestPlan_TrulyUnchangedNeedsBothMatch(t *testing.T) {
	items := Plan(
		[]BundleMeta{m("a", "b1", 13, "c1")},
		map[string]LockEntry{"a": l("b1", 13, "c1")},
		map[string]Ownership{"a": OwnMine},
	)
	if got := actionOf(items, "a"); got != ActionUnchanged {
		t.Fatalf("version 与 checksum 都相同才是 unchanged，got %v", got)
	}
}
