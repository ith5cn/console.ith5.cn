package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ith5/ith5/internal/hook"
)

var base = time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)

func ts(offset time.Duration) string {
	return base.Add(offset).UTC().Format(time.RFC3339Nano)
}

func newTestState() *State {
	st := NewState()
	// 固定 now，才能对「运行中区间的已耗时」做确定性断言。
	st.now = func() time.Time { return base.Add(30 * time.Second) }
	return st
}

func TestState_PairsStartAndEndByToolUseID(t *testing.T) {
	st := newTestState()
	st.Apply(hook.Trace{
		SessionID: "s1", ToolUseID: "tu1", Kind: "agent", Stage: "start",
		Name: "ith5-backend-engineer", Detail: "登录接口", OccurredAt: ts(0),
	})
	st.Apply(hook.Trace{
		SessionID: "s1", ToolUseID: "tu1", Kind: "agent", Stage: "end", OccurredAt: ts(5 * time.Second),
	})

	snap := st.Snapshot()
	if len(snap.Sessions) != 1 {
		t.Fatalf("会话数 = %d", len(snap.Sessions))
	}
	sess := snap.Sessions[0]
	if len(sess.Spans) != 1 {
		t.Fatalf("start/end 应合成一条区间，得到 %d 条", len(sess.Spans))
	}
	sp := sess.Spans[0]
	if sp.Running {
		t.Fatal("已结束的区间不应标记为运行中")
	}
	if sp.DurationMS != 5000 {
		t.Fatalf("耗时 = %dms，期望 5000", sp.DurationMS)
	}
	if sp.Name != "ith5-backend-engineer" {
		t.Fatalf("end 事件不该覆盖掉 start 带来的名字，得到 %q", sp.Name)
	}
	if len(sess.Running) != 0 {
		t.Fatal("不该再有运行中的区间")
	}
}

// 这是看板存在的意义：agent 还没结束时就要能看到它在跑，并且秒数在走。
func TestState_RunningSpanCountsElapsed(t *testing.T) {
	st := newTestState()
	st.Apply(hook.Trace{
		SessionID: "s1", ToolUseID: "tu1", Kind: "agent", Stage: "start",
		Name: "ith5-qa-engineer", OccurredAt: ts(0),
	})

	sess := st.Snapshot().Sessions[0]
	if len(sess.Running) != 1 {
		t.Fatalf("运行中区间数 = %d，期望 1", len(sess.Running))
	}
	// now 固定在 +30s
	if got := sess.Running[0].DurationMS; got != 30000 {
		t.Fatalf("运行中已耗时 = %dms，期望 30000", got)
	}
}

// SubagentStop 不带 tool_use_id。没有这个兜底，
// 非常规结束的 agent 会在看板上永远转圈。
func TestState_SubagentStopClosesOldestRunning(t *testing.T) {
	st := newTestState()
	st.Apply(hook.Trace{
		SessionID: "s1", ToolUseID: "tu1", Kind: "agent", Stage: "start",
		Name: "a1", OccurredAt: ts(0),
	})
	st.Apply(hook.Trace{
		SessionID: "s1", Kind: "agent", Stage: "end", OccurredAt: ts(2 * time.Second),
	})

	sess := st.Snapshot().Sessions[0]
	if len(sess.Running) != 0 {
		t.Fatal("SubagentStop 应关掉在跑的 agent")
	}
	if sess.Spans[0].DurationMS != 2000 {
		t.Fatalf("耗时 = %dms", sess.Spans[0].DurationMS)
	}
}

// end 先到（start 丢了）时不能静默吞掉——宁可显示一条零长区间。
func TestState_OrphanEndStillRecorded(t *testing.T) {
	st := newTestState()
	st.Apply(hook.Trace{
		SessionID: "s1", ToolUseID: "tu-lost", Kind: "skill", Stage: "end",
		Name: "ith5-doc-syncer", OccurredAt: ts(time.Second),
	})
	sess := st.Snapshot().Sessions[0]
	if len(sess.Spans) != 1 || sess.Spans[0].Name != "ith5-doc-syncer" {
		t.Fatalf("孤儿 end 被吞掉了: %+v", sess.Spans)
	}
	if sess.Spans[0].Running {
		t.Fatal("孤儿 end 不该留成运行中")
	}
}

func TestState_SortsSessionsByRecentActivity(t *testing.T) {
	st := newTestState()
	st.Apply(hook.Trace{SessionID: "old", Kind: "agent", Stage: "start", Name: "a", OccurredAt: ts(0)})
	st.Apply(hook.Trace{SessionID: "new", Kind: "agent", Stage: "start", Name: "b", OccurredAt: ts(time.Minute)})

	snap := st.Snapshot()
	if snap.Sessions[0].ID != "new" {
		t.Fatalf("最近活动的会话应排在前，得到 %q", snap.Sessions[0].ID)
	}
}

func TestState_IgnoresEventsWithoutSession(t *testing.T) {
	st := newTestState()
	if st.Apply(hook.Trace{Kind: "agent", Stage: "start", OccurredAt: ts(0)}) {
		t.Fatal("没有 session_id 的事件应被忽略")
	}
}

func TestTailer_ReadsIncrementally(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s1.ndjson")
	st := newTestState()
	tl := NewTailer(dir, st)

	write(t, path, `{"session_id":"s1","tool_use_id":"tu1","kind":"agent","stage":"start","name":"a1","occurred_at":"`+ts(0)+`"}`+"\n")
	if !tl.Poll() {
		t.Fatal("第一轮应读到新事件")
	}
	if tl.Poll() {
		t.Fatal("没有新内容时不应报告变化")
	}

	appendTo(t, path, `{"session_id":"s1","tool_use_id":"tu1","kind":"agent","stage":"end","occurred_at":"`+ts(3*time.Second)+`"}`+"\n")
	if !tl.Poll() {
		t.Fatal("追加后应读到新事件")
	}
	sp := st.Snapshot().Sessions[0].Spans[0]
	if sp.Running || sp.DurationMS != 3000 {
		t.Fatalf("区间未正确闭合: running=%v dur=%d", sp.Running, sp.DurationMS)
	}
}

// hook 是 O_APPEND 写的，轮询完全可能撞见只写了一半的行。
// 半行必须留到下一轮拼完整，不能当坏行丢掉。
func TestTailer_HandlesPartialLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s1.ndjson")
	st := newTestState()
	tl := NewTailer(dir, st)

	full := `{"session_id":"s1","tool_use_id":"tu1","kind":"agent","stage":"start","name":"a1","occurred_at":"` + ts(0) + `"}` + "\n"
	write(t, path, full[:20])
	tl.Poll()
	if len(st.Snapshot().Sessions) != 0 {
		t.Fatal("半行不该被解析")
	}

	appendTo(t, path, full[20:])
	if !tl.Poll() {
		t.Fatal("补全后应读到事件")
	}
	if len(st.Snapshot().Sessions) != 1 {
		t.Fatal("补全的行没有被解析")
	}
}

func TestTailer_ResetsOnTruncate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s1.ndjson")
	st := newTestState()
	tl := NewTailer(dir, st)

	write(t, path, `{"session_id":"s1","kind":"agent","stage":"start","name":"a1","occurred_at":"`+ts(0)+`"}`+"\n")
	tl.Poll()

	// 文件被换成更短的内容（会话重开）：必须从头读，而不是卡在旧 offset。
	write(t, path, `{"session_id":"s2","kind":"agent","stage":"start","name":"b","occurred_at":"`+ts(0)+`"}`+"\n")
	if !tl.Poll() {
		t.Fatal("截断后应重新从头读")
	}
	var found bool
	for _, s := range st.Snapshot().Sessions {
		if s.ID == "s2" {
			found = true
		}
	}
	if !found {
		t.Fatal("截断后的新内容没被读到")
	}
}

func TestTailer_SkipsCorruptLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s1.ndjson")
	st := newTestState()
	tl := NewTailer(dir, st)

	write(t, path, "这不是 JSON\n"+
		`{"session_id":"s1","kind":"agent","stage":"start","name":"a1","occurred_at":"`+ts(0)+`"}`+"\n")
	tl.Poll()
	if len(st.Snapshot().Sessions) != 1 {
		t.Fatal("一行脏数据不该让后续事件停摆")
	}
}

func TestTailer_MissingDirIsNotAnError(t *testing.T) {
	tl := NewTailer(filepath.Join(t.TempDir(), "nope"), newTestState())
	if tl.Poll() {
		t.Fatal("目录不存在时应安静返回")
	}
}

func TestReadFeatures(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "specs", "2.auth")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "# 任务\n\n- [x] 1. 建表\n- [X] 2. 写 migration\n- [ ] 3. 登录接口\n* [ ] 4. 前端登录页\n\n普通段落不算\n"
	if err := os.WriteFile(filepath.Join(dir, "tasks.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}

	feats := ReadFeatures(root)
	if len(feats) != 1 {
		t.Fatalf("feature 数 = %d", len(feats))
	}
	f := feats[0]
	if f.Name != "2.auth" {
		t.Fatalf("name = %q", f.Name)
	}
	if f.Total != 4 || f.Done != 2 {
		t.Fatalf("进度 = %d/%d，期望 2/4", f.Done, f.Total)
	}
	if f.Tasks[0].Text != "1. 建表" {
		t.Fatalf("任务文本 = %q", f.Tasks[0].Text)
	}
}

func TestReadFeatures_NoSpecsDir(t *testing.T) {
	if got := ReadFeatures(t.TempDir()); got != nil {
		t.Fatalf("没有 specs/ 时应返回空，得到 %+v", got)
	}
	if got := ReadFeatures(""); got != nil {
		t.Fatal("空目录参数应返回空")
	}
}

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendTo(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}
