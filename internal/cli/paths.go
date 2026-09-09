// Package cli 实现 ith5 命令行的本地状态与同步逻辑。
package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ith5/ith5/internal/cli/link"
	"github.com/ith5/ith5/internal/core"
)

// Paths 是本地所有状态的位置（技术方案 §8.1）。
type Paths struct {
	Home       string // ~/.ith5
	Store      string // ~/.ith5/store —— 真相
	ClaudeHome string // ~/.claude
	Skills     string // <claude_home>/skills —— 目标（目录形态）
	Agents     string // <claude_home>/agents —— 目标（文件形态）
	Staging    string // <claude_home>/.ith5-staging
	Trash      string // <claude_home>/.ith5-trash
	// UserConfig 是 ~/.claude.json —— MCP server 定义所在的用户配置。
	// 它不在 claude_home 之下，而是与之平级挂在用户主目录，
	// 因此不能由 ClaudeHome 拼出来。
	UserConfig string
}

// Roots 返回全部有独立入口的 Kind 及其目标根目录。
//
// 合并形态（setting/mcp）不在其中——它们没有独立入口，
// 由 mergeTarget 决定写进哪个用户 JSON 文件。
func (p Paths) Roots() map[core.Kind]string {
	out := map[core.Kind]string{}
	for _, k := range core.AllKinds {
		if r := k.Root(); r != "" {
			out[k] = filepath.Join(p.ClaudeHome, r)
		}
	}
	return out
}

// DefaultPaths 按默认位置构造。claudeHome 为空时取 ~/.claude。
//
// staging 与 trash 刻意放在 <claude_home> 之下、skills 之外：
//   - 必须与目标同卷，否则 rename 会报 EXDEV（用户可改 claude_home 指向别的卷）
//   - 必须在 skills 与 agents 之外，否则 Claude Code 会把暂存物当成一个技能
//     或一个 subagent，瞬时多出 /coding-standard.new 一类命令、或一个半成品
//     subagent，污染用户的 / 菜单与派发列表
func DefaultPaths(claudeHome string) (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("定位用户主目录: %w", err)
	}
	if claudeHome == "" {
		claudeHome = filepath.Join(home, ".claude")
	}
	ith5 := filepath.Join(home, ".ith5")
	return Paths{
		UserConfig: filepath.Join(home, ".claude.json"),
		Home:       ith5,
		Store:      filepath.Join(ith5, "store"),
		ClaudeHome: claudeHome,
		Skills:     filepath.Join(claudeHome, "skills"),
		Agents:     filepath.Join(claudeHome, "agents"),
		Staging:    filepath.Join(claudeHome, ".ith5-staging"),
		Trash:      filepath.Join(claudeHome, ".ith5-trash"),
	}, nil
}

// EnsureDirs 预建所有目录。
//
// 其中预建 skills 与 agents 目录尤为关键：Claude Code 会监视这两个目录，
// 会话内的变更实时生效；但**若会话启动时目录尚不存在，新建后需重启才被监视**。
// 全新机器上它们通常都不存在，所以必须在用户首次启动 Claude Code 之前建好，
// 否则第一次 sync 会表现为「装了但看不到」（PRD C19）。
//
// agents 目录按与 skills 相同处理，这是推定而非已核实（S4 待实测）：
// 两者用的是同一套 watch 机制，没有理由不同。按「需要重启」实现，
// 是两种实测结果下都正确的那一侧——预建多余也无害，漏建则是静默失效。
func (p Paths) EnsureDirs() error {
	dirs := []string{p.Home, p.Store, p.ClaudeHome, p.Staging, p.Trash}
	for _, d := range p.Roots() {
		dirs = append(dirs, d)
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("创建目录 %s: %w", d, err)
		}
	}
	return nil
}

// Target 返回某个 bundle 的目标入口路径（技术方案 §8.2.0）。
//
// 目录形态是 skills/<name>/，文件形态是 <root>/<name><ext>。
// 合并形态没有独立入口，返回它要写进的那个用户 JSON 文件。
func (p Paths) Target(kind core.Kind, bundleName string) string {
	if kind.Shape() == core.ShapeMerge {
		return p.MergeFile(kind)
	}
	return filepath.Join(p.ClaudeHome, kind.Root(), bundleName+kind.Ext())
}

// MergeFile 返回合并形态要写入的用户 JSON 文件。
func (p Paths) MergeFile(kind core.Kind) string {
	if kind == core.KindMCP {
		return p.UserConfig
	}
	return filepath.Join(p.ClaudeHome, "settings.json")
}

// RelTarget 返回用于回执 detail 的相对化路径。
// 绝对路径含 home，与 L0 白名单同一约束，不得上报（技术方案 §5.2）。
func (p Paths) RelTarget(kind core.Kind, bundleName string) string {
	switch {
	case kind == core.KindMCP:
		return "~/.claude.json"
	case kind.Shape() == core.ShapeMerge:
		return "settings.json"
	default:
		return kind.Root() + "/" + bundleName + kind.Ext()
	}
}

// StoreCtx 组装归属判定所需的 store 侧信息。
func (p Paths) StoreCtx(kind core.Kind, bundleName string) link.StoreCtx {
	return link.StoreCtx{
		Root:      p.Store,
		BundleDir: filepath.Join(p.Store, string(kind), bundleName),
		EntryFile: kind.EntryFile(),
	}
}
func (p Paths) LockFile() string        { return filepath.Join(p.Home, "lock.json") }
func (p Paths) ConfigFile() string      { return filepath.Join(p.Home, "config.json") }
func (p Paths) CredentialsFile() string { return filepath.Join(p.Home, "credentials.json") }

// CleanScratch 清空 staging 与 trash。
//
// 两者中的任何残留都可无条件删除——store 是真相，中断后重做即可
// （技术方案 §8.5.4）。doctor 与每次 sync 开始时各执行一次。
func (p Paths) CleanScratch() error {
	for _, d := range []string{p.Staging, p.Trash} {
		entries, err := os.ReadDir(d)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := os.RemoveAll(filepath.Join(d, e.Name())); err != nil {
				return fmt.Errorf("清理 %s: %w", filepath.Join(d, e.Name()), err)
			}
		}
	}
	return nil
}
