package watch

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ith5/ith5/internal/hook"
)

// Tailer 轮询 trace 目录，把新追加的行喂给 State。
//
// 用轮询而不是 fsnotify：省一个跨平台的原生依赖，而看板的实时性
// 要求是「人眼觉得是即时的」，300ms 足够；hook 写入是 O_APPEND 的
// 原子小写，轮询读到半行的唯一情况是行尚未写完，留到下一轮即可。
type Tailer struct {
	dir      string
	state    *State
	interval time.Duration

	mu sync.Mutex
	// offsets 只推进到「最后一个完整行的末尾」。读到半行时不推进，
	// 下一轮从它的起点重读 —— 比另存一份半行缓冲简单，也不会两边不同步。
	offsets map[string]int64
}

func NewTailer(dir string, st *State) *Tailer {
	return &Tailer{
		dir: dir, state: st, interval: 300 * time.Millisecond,
		offsets: map[string]int64{},
	}
}

// Run 持续轮询直到 done 关闭。每轮若有变化就调一次 onChange。
func (t *Tailer) Run(done <-chan struct{}, onChange func()) {
	tick := time.NewTicker(t.interval)
	defer tick.Stop()
	for {
		if t.Poll() && onChange != nil {
			onChange()
		}
		select {
		case <-done:
			return
		case <-tick.C:
		}
	}
}

// Poll 扫一轮，返回是否有新事件。
func (t *Tailer) Poll() bool {
	entries, err := os.ReadDir(t.dir)
	if err != nil {
		return false // 目录还不存在：hook 尚未写过 trace，不是错误
	}
	changed := false
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".ndjson") {
			continue
		}
		if t.readFile(filepath.Join(t.dir, e.Name())) {
			changed = true
		}
	}
	return changed
}

func (t *Tailer) readFile(path string) bool {
	t.mu.Lock()
	off := t.offsets[path]
	t.mu.Unlock()

	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	// 文件被截断或换了（会话重开）：从头再来，否则会一直读到 EOF 之外。
	if fi.Size() < off {
		off = 0
	}
	if fi.Size() == off {
		return false
	}

	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return false
	}

	r := bufio.NewReader(f)
	changed := false
	for {
		line, err := r.ReadString('\n')
		// EOF 时 line 是没有换行结尾的残行 —— 它还没写完，
		// 不推进 offset、不解析，下一轮连着后半截一起重读。
		if err != nil {
			break
		}
		off += int64(len(line))
		if t.apply(line) {
			changed = true
		}
	}

	t.mu.Lock()
	t.offsets[path] = off
	t.mu.Unlock()
	return changed
}

func (t *Tailer) apply(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	var tr hook.Trace
	if err := json.Unmarshal([]byte(line), &tr); err != nil {
		return false // 坏行直接跳过，绝不让看板因为一行脏数据停摆
	}
	return t.state.Apply(tr)
}
