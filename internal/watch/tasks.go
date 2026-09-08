package watch

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// task 级进度不靠 agent 汇报，直接读 specs/<feature>/tasks.md 的复选框。
//
// 这是 ith5-ai 的断点续跑机制：开发完一个 task 就把 [ ] 改成 [x]，
// 重跑时读到 [x] 即跳过。也就是说这个文件本身就是权威的进度真相，
// 比任何事件流都可靠 —— 事件会漏，磁盘上的 [x] 不会。
var checkboxRE = regexp.MustCompile(`^\s*[-*]\s*\[([ xX])\]\s*(.*)$`)

// Task 是 tasks.md 里的一行。
type Task struct {
	Done bool   `json:"done"`
	Text string `json:"text"`
}

// Feature 是 specs/ 下的一个子目录。
type Feature struct {
	Name  string `json:"name"`
	Tasks []Task `json:"tasks"`
	Done  int    `json:"done"`
	Total int    `json:"total"`
}

// maxTaskText 截断任务描述，避免一行超长文案把看板撑破。
const maxTaskText = 160

// ReadFeatures 扫 <projectDir>/specs/*/tasks.md。
// 读不到时返回空切片而非错误：项目可能压根没有 specs/，看板照常工作。
func ReadFeatures(projectDir string) []Feature {
	if projectDir == "" {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(projectDir, "specs"))
	if err != nil {
		return nil
	}
	var out []Feature
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		f, ok := readFeature(filepath.Join(projectDir, "specs", e.Name()), e.Name())
		if ok {
			out = append(out, f)
		}
	}
	return out
}

func readFeature(dir, name string) (Feature, bool) {
	b, err := os.ReadFile(filepath.Join(dir, "tasks.md"))
	if err != nil {
		return Feature{}, false
	}
	f := Feature{Name: name}
	for _, line := range strings.Split(string(b), "\n") {
		m := checkboxRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		done := m[1] == "x" || m[1] == "X"
		f.Tasks = append(f.Tasks, Task{Done: done, Text: truncate(m[2], maxTaskText)})
		f.Total++
		if done {
			f.Done++
		}
	}
	return f, f.Total > 0
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
