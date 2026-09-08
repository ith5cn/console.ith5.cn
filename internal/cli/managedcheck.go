package cli

import (
	"encoding/json"
	"os"
	"runtime"
)

// managedSettingsPaths 是各平台上系统级 managed-settings.json 的位置。
var managedSettingsPaths = map[string][]string{
	"darwin":  {"/Library/Application Support/ClaudeCode/managed-settings.json"},
	"linux":   {"/etc/claude-code/managed-settings.json"},
	"windows": {`C:\Program Files\ClaudeCode\managed-settings.json`},
}

// ManagedConflict 描述一条会让 ITH5 静默失效的系统级策略。
type ManagedConflict struct {
	Path   string
	Key    string
	Impact string
}

// CheckManagedSettings 检测系统级 managed settings 是否会让 ITH5 静默失效。
//
// 这是 ADR-002 的现实推论：我们自己不加锁，**但不代表别人没加**。
// 若 IT 部署了 allowManagedHooksOnly，我们注入到用户 settings.json 的
// hook 会被**静默禁用** —— 审计就此无声中断，而界面上一切正常。
// doctor 必须显式报出来，不能让人以为没问题。
func CheckManagedSettings() []ManagedConflict {
	var out []ManagedConflict
	for _, p := range managedSettingsPaths[runtime.GOOS] {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var cfg struct {
			AllowManagedHooksOnly *bool `json:"allowManagedHooksOnly"`
			// 值可能是 true，也可能是指明锁哪几项的数组
			StrictPluginOnlyCustomization any `json:"strictPluginOnlyCustomization"`
		}
		if err := json.Unmarshal(b, &cfg); err != nil {
			out = append(out, ManagedConflict{
				Path: p, Key: "(无法解析)",
				Impact: "系统级策略文件存在但无法解析，其影响未知",
			})
			continue
		}
		if cfg.AllowManagedHooksOnly != nil && *cfg.AllowManagedHooksOnly {
			out = append(out, ManagedConflict{
				Path: p, Key: "allowManagedHooksOnly",
				Impact: "ITH5 的执行审计 hook 会被静默禁用，上报将无声中断",
			})
		}
		if locksSkills(cfg.StrictPluginOnlyCustomization) {
			out = append(out, ManagedConflict{
				Path: p, Key: "strictPluginOnlyCustomization",
				Impact: "用户级 skills 被策略禁用，ITH5 分发的内容不会被加载",
			})
		}
	}
	return out
}

// locksSkills 判断该策略是否锁住了 skills。
// true 表示全锁；数组则要看是否含 "skills"。
func locksSkills(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case []any:
		for _, x := range t {
			if s, ok := x.(string); ok && s == "skills" {
				return true
			}
		}
	}
	return false
}
