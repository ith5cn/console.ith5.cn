package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// hookCommand 是注入到 settings.json 里的命令签名。
// 用一个稳定可识别的前缀，才能做到幂等安装与精确卸载。
const hookCommand = "ith5-hook"

// hookEvents 是我们注册的事件及其 matcher。
//
// PostToolUse 只匹配会改动文件或执行命令的工具 —— 匹配面越大，
// 触发次数越多，而它是同步执行的。
//
// 派 subagent 的工具在不同 Claude Code 版本里叫 Task 或 Agent
// （2.1.263 实测是 Agent），两个都要匹配 —— 少一个的症状是看板永远空白，
// 而且完全静默，没有任何报错指向这里。
const agentSkillMatcher = "Task|Agent|Skill"

// Task/Agent/Skill 额外挂 PreToolUse：PostToolUse 在工具**结束后**才触发，
// 一个跑几分钟的 subagent 只有它的话，看板会全程空白、到结束才刷出一整条，
// 那就不是实时了。PreToolUse 供 start、PostToolUse 供 end，配成一段区间。
// 这两个事件都是低频的（一次会话几十次量级），不构成性能负担。
var hookEvents = []struct {
	Event   string
	Matcher string
	Arg     string
}{
	{"SessionStart", "", "session-start"},
	{"PreToolUse", agentSkillMatcher, "pre-tool-use"},
	{"PostToolUse", "Edit|Write|MultiEdit|Bash|" + agentSkillMatcher, "post-tool-use"},
	// subagent 以非常规方式结束时 PostToolUse 可能不来，
	// 少了它看板上会留下一条永远在转圈的 agent。
	{"SubagentStop", "", "subagent-stop"},
}

// InstallHooks 把 ith5-hook 合并进 Claude Code 的 settings.json。
//
// 三条硬规矩：
//   - **合并而非覆盖**：用户可能已有自己的 hook，覆盖等于毁掉他的配置
//   - 写入前备份为 settings.json.ith5.bak
//   - 幂等：重复安装不产生重复 handler
func InstallHooks(claudeHome, binPath string) (changed bool, err error) {
	path := filepath.Join(claudeHome, "settings.json")
	root := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &root); err != nil {
			return false, fmt.Errorf("settings.json 无法解析，请先修复或备份后删除: %w", err)
		}
		// 只在文件存在时备份，且每次安装都刷新备份
		if err := os.WriteFile(path+".ith5.bak", b, 0o644); err != nil {
			return false, fmt.Errorf("备份 settings.json: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return false, err
	}

	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}

	for _, he := range hookEvents {
		groups, _ := hooks[he.Event].([]any)
		cmd := fmt.Sprintf("%s %s", binPath, he.Arg)
		if hookGroupsContain(groups, hookCommand) {
			// 已有我方 handler：更新命令路径（二进制可能换了位置）
			// 与 matcher（升级时匹配面会变，例如新增 Task|Skill —— 只更命令
			// 会让老机器上的新事件永远不触发，且症状是静默的）。
			if updateOurGroup(groups, cmd, he.Matcher) {
				changed = true
			}
			hooks[he.Event] = groups
			continue
		}
		group := map[string]any{
			"hooks": []any{map[string]any{"type": "command", "command": cmd}},
		}
		if he.Matcher != "" {
			group["matcher"] = he.Matcher
		}
		// 追加而不是替换整个事件数组，保留用户已有的 hook
		hooks[he.Event] = append(groups, group)
		changed = true
	}
	root["hooks"] = hooks

	if !changed {
		return false, nil
	}
	return true, writeJSONFile(path, root, 0o644)
}

// UninstallHooks 只移除我方的 handler，不动用户自己的。
func UninstallHooks(claudeHome string) error {
	path := filepath.Join(claudeHome, "settings.json")
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		return err
	}
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		return nil
	}
	for event, v := range hooks {
		groups, _ := v.([]any)
		kept := make([]any, 0, len(groups))
		for _, g := range groups {
			if !groupIsOurs(g) {
				kept = append(kept, g)
			}
		}
		if len(kept) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = kept
		}
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
	} else {
		root["hooks"] = hooks
	}
	return writeJSONFile(path, root, 0o644)
}

// HooksInstalled 报告我方 handler 是否已就位，供 doctor 检查。
func HooksInstalled(claudeHome string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, he := range hookEvents {
		out[he.Event] = false
	}
	b, err := os.ReadFile(filepath.Join(claudeHome, "settings.json"))
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		return out, err
	}
	hooks, _ := root["hooks"].(map[string]any)
	for _, he := range hookEvents {
		groups, _ := hooks[he.Event].([]any)
		out[he.Event] = hookGroupsContain(groups, hookCommand)
	}
	return out, nil
}

func hookGroupsContain(groups []any, marker string) bool {
	for _, g := range groups {
		if groupIsOurs(g) {
			return true
		}
	}
	return false
}

func groupIsOurs(g any) bool {
	m, _ := g.(map[string]any)
	inner, _ := m["hooks"].([]any)
	for _, h := range inner {
		hm, _ := h.(map[string]any)
		if c, _ := hm["command"].(string); strings.Contains(c, hookCommand) {
			return true
		}
	}
	return false
}

// updateOurGroup 把已安装的我方 handler 对齐到当前期望的命令与 matcher。
// 只动我方的组，用户自己的 handler 一律不碰。
func updateOurGroup(groups []any, cmd, matcher string) bool {
	changed := false
	for _, g := range groups {
		m, _ := g.(map[string]any)
		if !groupIsOurs(g) {
			continue
		}
		inner, _ := m["hooks"].([]any)
		for _, h := range inner {
			hm, _ := h.(map[string]any)
			if c, _ := hm["command"].(string); strings.Contains(c, hookCommand) && c != cmd {
				hm["command"] = cmd
				changed = true
			}
		}
		// matcher 为空表示该事件不需要 matcher（如 SessionStart）；
		// 此时若历史上写过一个，也一并清掉。
		if cur, _ := m["matcher"].(string); cur != matcher {
			if matcher == "" {
				delete(m, "matcher")
			} else {
				m["matcher"] = matcher
			}
			changed = true
		}
	}
	return changed
}
