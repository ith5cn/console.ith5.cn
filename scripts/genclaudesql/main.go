// 一次性工具：把 ~/.claude 下的内容打包成 ITH5 bundle，生成可执行的 SQL。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ith5/ith5/internal/core"
)

type bundle struct {
	Name  string
	Kind  core.Kind
	Desc  string
	Files []core.File
}

var home = os.Getenv("HOME")

func read(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// descOf 取入口文件 frontmatter 的 description，截断到 240 字符以内。
func descOf(content, fallback string) string {
	fm, err := core.ParseFrontmatter(content)
	if err != nil || fm.Description == "" {
		return fallback
	}
	return clip(strings.TrimSpace(fm.Description))
}

func clip(d string) string {
	if r := []rune(d); len(r) > 240 {
		return strings.TrimSpace(string(r[:237])) + "..."
	}
	return d
}

// collectDir 收集一个 skill 目录下的全部文件，跳过 .DS_Store 与运行日志。
func collectDir(root string) []core.File {
	var out []core.File
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		base := d.Name()
		if base == ".DS_Store" || strings.HasSuffix(base, ".log") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		out = append(out, core.File{Path: filepath.ToSlash(rel), Content: read(p)})
		return nil
	})
	if err != nil {
		panic(err)
	}
	return out
}

// single 造一个单文件 bundle（文件形态与合并形态都用它）。
func single(kind core.Kind, name, desc, content string) bundle {
	return bundle{
		Name: name, Kind: kind, Desc: desc,
		Files: []core.File{{Path: kind.EntryFile(), Content: content}},
	}
}

// slug 把文件名转成合法的 bundle 名（小写，非字母数字变连字符）。
func slug(s string) string {
	s = strings.ToLower(strings.TrimSuffix(s, filepath.Ext(s)))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// firstHeading 取 Markdown 第一个标题作为描述，退化为文件名。
func firstHeading(content, fallback string) string {
	for _, line := range strings.Split(content, "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "#") {
			if h := strings.TrimSpace(strings.TrimLeft(t, "# ")); h != "" {
				return clip(h)
			}
		}
	}
	return fallback
}

func main() {
	var bundles []bundle

	// 1) skills/<name>/ -> kind=skill，原样落盘
	skillRoot := filepath.Join(home, ".claude", "skills")
	entries, err := os.ReadDir(skillRoot)
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		files := collectDir(filepath.Join(skillRoot, e.Name()))
		var entry string
		for _, f := range files {
			if f.Path == core.SkillFile {
				entry = f.Content
			}
		}
		bundles = append(bundles, bundle{
			Name: e.Name(), Kind: core.KindSkill,
			Desc: descOf(entry, e.Name()), Files: files,
		})
	}

	// 2) agents/<name>.md -> kind=agent
	forEachFile(filepath.Join(home, ".claude", "agents"), ".md", func(name, content string) {
		bundles = append(bundles, single(core.KindAgent, name, descOf(content, ""), content))
	})

	// 3) standards/<name>.md -> kind=standard，一份规范一个 bundle
	forEachFile(filepath.Join(home, ".claude", "standards"), ".md", func(name, content string) {
		s := slug(name)
		bundles = append(bundles, single(core.KindStandard, s,
			firstHeading(content, "团队规范："+s), content))
	})

	// 4) hooks/<name>.js -> kind=hook（.log 是运行日志，不入库）
	forEachFile(filepath.Join(home, ".claude", "hooks"), ".js", func(name, content string) {
		bundles = append(bundles, single(core.KindHook, slug(name),
			"Stop 钩子脚本。注意：脚本落盘不等于生效，还需在 settings.json 的 hooks 里登记。", content))
	})

	// 5) workflows/<name>.js -> kind=workflow
	forEachFile(filepath.Join(home, ".claude", "workflows"), ".js", func(name, content string) {
		bundles = append(bundles, single(core.KindWorkflow, slug(name),
			"Workflow 编排脚本，落到 ~/.claude/workflows/。", content))
	})

	// 6) settings.json -> kind=setting，按顶层键合并
	bundles = append(bundles, single(core.KindSetting, "claude-baseline",
		"团队 Claude Code 基线配置：默认模型、Workflow 开关、启用的插件与 marketplace。按顶层键合并进 ~/.claude/settings.json，成员改过的键不会被覆盖。",
		read(filepath.Join(home, ".claude", "settings.json"))))

	// 7) MCP server 定义 -> kind=mcp，按 server 名合并
	bundles = append(bundles, single(core.KindMCP, "telegram",
		"telegram MCP server 定义，合并进 ~/.claude.json 的 mcpServers。",
		read(filepath.Join(home, ".claude", "plugins", "cache",
			"claude-plugins-official", "telegram", "0.0.7", ".mcp.json"))))

	// 校验 + 计算 checksum
	sort.Slice(bundles, func(i, j int) bool {
		if bundles[i].Kind != bundles[j].Kind {
			return bundles[i].Kind < bundles[j].Kind
		}
		return bundles[i].Name < bundles[j].Name
	})
	type row struct {
		b         bundle
		filesJSON string
		checksum  string
	}
	var rows []row
	seen := map[core.Ref]bool{}
	for _, b := range bundles {
		if err := core.ValidateName(b.Name); err != nil {
			panic(fmt.Sprintf("%s: %v", b.Name, err))
		}
		// 同一个 ref 出现两次会在数据库唯一键上炸，早发现早改名
		ref := core.MakeRef(b.Kind, b.Name)
		if seen[ref] {
			panic("重复的 bundle: " + string(ref))
		}
		seen[ref] = true
		if err := core.ValidateForPublish(b.Kind, b.Name, b.Files); err != nil {
			panic(fmt.Sprintf("%s (%s): %v", b.Name, b.Kind, err))
		}
		sum, err := core.Checksum(b.Files)
		if err != nil {
			panic(err)
		}
		j, err := core.CanonicalJSON(b.Files)
		if err != nil {
			panic(err)
		}
		rows = append(rows, row{b, string(j), sum})
	}

	var sb strings.Builder
	sb.WriteString(header)
	for _, r := range rows {
		sb.WriteString(fmt.Sprintf("  (%s, %s, %s, %s::jsonb, %s),\n",
			q(r.b.Name), q(string(r.b.Kind)), q(r.b.Desc), q(r.filesJSON), q(r.checksum)))
	}
	s := strings.TrimSuffix(sb.String(), ",\n") + ";\n" + footer
	if err := os.WriteFile(os.Args[1], []byte(s), 0o644); err != nil {
		panic(err)
	}

	byKind := map[core.Kind]int{}
	for _, r := range rows {
		byKind[r.b.Kind]++
		fmt.Fprintf(os.Stderr, "%-9s %-28s %2d files  %s\n",
			r.b.Kind, r.b.Name, len(r.b.Files), r.checksum[:23])
	}
	fmt.Fprintf(os.Stderr, "\n共 %d 个 bundle:", len(rows))
	for _, k := range core.AllKinds {
		if byKind[k] > 0 {
			fmt.Fprintf(os.Stderr, " %s=%d", k, byKind[k])
		}
	}
	fmt.Fprintln(os.Stderr)
}

// forEachFile 遍历目录下指定扩展名的文件，name 不含扩展名。
func forEachFile(dir, ext string, fn func(name, content string)) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ext) || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		fn(strings.TrimSuffix(e.Name(), ext), read(filepath.Join(dir, e.Name())))
	}
}

func q(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
