// Package teamaifmt 把快照渲染成 teamai-cli 能直接读的团队仓库目录（docs/设计-资源类型与层级.md §6）。
//
// 输出是一个「相对路径 → 内容」的映射，由调用方落盘。这里不碰文件系统，便于测试，
// 也便于服务端将来把同一份视图打成 zip。
package teamaifmt

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ith5/ith5/internal/resources"
	"github.com/ith5/ith5/internal/sync"
)

// BlobReader 按哈希取内容。渲染前调用方应已把所需 blob 全部取到本地。
type BlobReader func(sha256 string) ([]byte, error)

// Render 生成 teamai 仓库视图。
//
// 布局采用 teamai 的「无角色清单」扁平模式：skills/<name>/、rules/<name>.md、docs/<path>、
// agents/<name>.yaml、env/env.yaml、hooks/hooks.yaml、mcp/mcp.yaml、claudemd/<ns>/<name>.md、
// culture.md、learnings/<name>.md、teamai.yaml。冲突条目不渲染。
func Render(snap sync.Snapshot, read BlobReader) (map[string][]byte, error) {
	return RenderWith(snap, read, Options{})
}

// Options 控制渲染布局。
type Options struct {
	// Namespaced 生成 teamai 的分区布局：skills/<ns>/<name>/、rules/<ns>/<name>.md、learnings/<ns>/<name>.md，
	// 并写出 manifest/roles.yaml（权限组 = role）与 manifest/projects.yaml（绑定的项目）。
	// teamai 端需要 init 时用 --project <slug> 选中项目；扁平布局不需要。
	Namespaced bool
}

// RenderWith 按选项渲染。默认扁平布局：服务端已经按绑定过滤过，客户端不必再按命名空间筛一遍。
func RenderWith(snap sync.Snapshot, read BlobReader, opts Options) (map[string][]byte, error) {
	out := map[string][]byte{}
	var envVars, hooks, mcps []string
	// 命名空间来自服务端：项目 slug、团队 slug、权限组 key；org 级为 common
	ns := func(e resources.Entry) string {
		if !opts.Namespaced {
			return ""
		}
		if e.Namespace == "" {
			return "common"
		}
		return e.Namespace
	}
	// rule / learning：org 级留在根（teamai 对根级条目不做命名空间过滤），其余进各自的命名空间目录
	scoped := func(e resources.Entry) string {
		if !opts.Namespaced || e.Namespace == "" || e.Namespace == "common" {
			return ""
		}
		return e.Namespace + "/"
	}

	for _, e := range snap.Resources {
		if e.Status != resources.StatusOK {
			continue
		}
		switch e.Kind {
		case resources.KindSkill:
			for _, f := range e.Files {
				b, err := read(f.SHA256)
				if err != nil {
					return nil, fmt.Errorf("%s/%s: %w", e.Kind, e.Name, err)
				}
				if n := ns(e); n != "" {
					out["skills/"+n+"/"+e.Name+"/"+f.Path] = b
				} else {
					out["skills/"+e.Name+"/"+f.Path] = b
				}
			}
		case resources.KindRule:
			b, err := entry(e, read)
			if err != nil {
				return nil, err
			}
			out["rules/"+scoped(e)+e.Name+".md"] = b
		case resources.KindDoc:
			b, err := entry(e, read)
			if err != nil {
				return nil, err
			}
			out["docs/"+e.Name] = b
		case resources.KindAgent:
			b, err := entry(e, read)
			if err != nil {
				return nil, err
			}
			out["agents/"+e.Name+".yaml"] = b
		case resources.KindClaudeMD:
			b, err := entry(e, read)
			if err != nil {
				return nil, err
			}
			ns := e.Namespace
			if ns == "" {
				ns = "common"
			}
			out["claudemd/"+ns+"/"+e.Name+".md"] = b
		case resources.KindLearning:
			b, err := entry(e, read)
			if err != nil {
				return nil, err
			}
			out["learnings/"+scoped(e)+e.Name+".md"] = b
		case resources.KindEnv:
			b, err := entry(e, read)
			if err != nil {
				return nil, err
			}
			envVars = append(envVars, envBlock(e.Name, string(b)))
		case resources.KindHook:
			b, err := entry(e, read)
			if err != nil {
				return nil, err
			}
			hooks = append(hooks, indentItem(string(b)))
			for _, f := range e.Files {
				if strings.HasPrefix(f.Path, "scripts/") {
					sb, err := read(f.SHA256)
					if err != nil {
						return nil, err
					}
					out["hooks/"+f.Path] = sb
				}
			}
		case resources.KindMCP:
			b, err := entry(e, read)
			if err != nil {
				return nil, err
			}
			mcps = append(mcps, indentItem(string(b)))
		}
	}

	if len(envVars) > 0 {
		out["env/env.yaml"] = []byte("variables:\n" + strings.Join(envVars, ""))
	}
	if len(hooks) > 0 {
		out["hooks/hooks.yaml"] = []byte("hooks:\n" + strings.Join(hooks, ""))
	}
	if len(mcps) > 0 {
		out["mcp/mcp.yaml"] = []byte("servers:\n" + strings.Join(mcps, ""))
	}
	if snap.Culture != nil {
		b, err := entry(*snap.Culture, read)
		if err != nil {
			return nil, err
		}
		out["culture.md"] = b
	}
	out["teamai.yaml"] = []byte(teamaiYAML(snap))
	if tags := tagsYAML(snap); tags != "" {
		out["tags.yaml"] = []byte(tags)
	}
	if opts.Namespaced {
		if m := rolesYAML(snap); m != "" {
			out["manifest/roles.yaml"] = []byte(m)
		}
		if m := projectsYAML(snap); m != "" {
			out["manifest/projects.yaml"] = []byte(m)
		}
	}
	return out, nil
}

// rolesYAML 把权限组投影成 teamai 的 role：一个组一个 role，命名空间就是组的 key。
// teamai 要求 roles 非空，没有授权时不生成文件。
func rolesYAML(snap sync.Snapshot) string {
	if len(snap.Grants) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("# 由 ith5 生成：每个权限组对应一个 role。\nversion: 1\nroles:\n")
	for _, g := range snap.Grants {
		sb.WriteString("  - id: " + yamlStr(g.GroupKey) + "\n")
		sb.WriteString("    description: " + yamlStr(g.Name) + "\n")
		sb.WriteString("    resources:\n      knowledge: [" + yamlStr(g.GroupKey) + "]\n      skills: [" + yamlStr(g.GroupKey) + "]\n")
	}
	return sb.String()
}

// projectsYAML 把绑定的项目投影成 teamai 的 project。
//
// 服务端已经算好了这个绑定应得的全部内容，所以每个项目的 skills / knowledge 命名空间都带上
// common 与全部权限组：teamai 选中任一项目就能拿到 org 级与授权的资源，不需要再选 role。
func projectsYAML(snap sync.Snapshot) string {
	if len(snap.Projects) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("# 由 ith5 生成：绑定的项目。\nversion: 1\nprojects:\n")
	for _, p := range snap.Projects {
		shared := []string{p.Slug, "common"}
		for _, g := range snap.Grants {
			shared = append(shared, g.GroupKey)
		}
		sb.WriteString("  - id: " + yamlStr(p.Slug) + "\n")
		sb.WriteString("    name: " + yamlStr(p.Name) + "\n")
		sb.WriteString("    resources:\n      knowledge: " + yamlList(shared) + "\n      skills: " + yamlList(shared) + "\n      learnings: [" + yamlStr(p.Slug) + "]\n")
	}
	return sb.String()
}

func entry(e resources.Entry, read BlobReader) ([]byte, error) {
	name := e.Kind.EntryFile()
	for _, f := range e.Files {
		if f.Path == name {
			b, err := read(f.SHA256)
			if err != nil {
				return nil, fmt.Errorf("%s/%s: %w", e.Kind, e.Name, err)
			}
			return b, nil
		}
	}
	return nil, fmt.Errorf("%s/%s: 缺少入口文件 %s", e.Kind, e.Name, name)
}

// envBlock 把 ENV.yaml（value / description 两个键）渲染成 env.yaml 的一条。
func envBlock(key, content string) string {
	var sb strings.Builder
	sb.WriteString("  - key: " + key + "\n")
	if resources.IsSecretEnv(content) {
		// 密钥变量：服务端没有值，客户端按本机的 env 覆盖机制填；空值让 teamai 提示而不是静默
		sb.WriteString("    value: \"\"\n    # secret: 值不在服务端，需在本机设置\n")
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") || strings.HasPrefix(line, "secret:") {
			continue
		}
		sb.WriteString("    " + line + "\n")
	}
	return sb.String()
}

// indentItem 把一份顶层 YAML 映射缩进成列表项。
func indentItem(content string) string {
	var sb strings.Builder
	first := true
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if first {
			sb.WriteString("  - " + line + "\n")
			first = false
		} else {
			sb.WriteString("    " + line + "\n")
		}
	}
	return sb.String()
}

// teamaiYAML 生成 teamai.yaml：mode: self 让 teamai 直接读磁盘，不走 git。
func teamaiYAML(snap sync.Snapshot) string {
	p := snap.Policy
	var sb strings.Builder
	sb.WriteString("# 由 ith5 生成，随每次同步覆盖；请勿手改。\n")
	sb.WriteString("team: " + yamlStr(snap.Org.Slug) + "\n")
	sb.WriteString("description: " + yamlStr(snap.Org.Name) + "\n")
	sb.WriteString("repo: ith5://" + snap.Org.Slug + "\n")
	sb.WriteString("provider: git\n")
	sb.WriteString("mode: self\n")
	sb.WriteString("sharing:\n")
	sb.WriteString("  rules:\n    enforced: " + yamlList(p.EnforcedRules) + "\n")
	sb.WriteString("  hooks:\n    autoApply: " + yamlBool(p.HooksAutoApply) + "\n    requireTeamScripts: " + yamlBool(p.HooksRequireScripts) + "\n")
	sb.WriteString("  mcp:\n    autoApply: " + yamlBool(p.MCPAutoApply) + "\n    allowedCommands: " + yamlList(p.MCPAllowedCommands) + "\n    allowedHosts: " + yamlList(p.MCPAllowedHosts) + "\n")
	sb.WriteString("  recall:\n    enabled: " + yamlBool(p.RecallEnabled) + "\n")
	sb.WriteString("  contributeHint:\n    enabled: " + yamlBool(p.ContributeHint) + "\n")
	sb.WriteString("  coAuthor:\n    enabled: " + yamlBool(p.CoAuthor) + "\n")
	return sb.String()
}

// tagsYAML 从条目的 tags 生成 tags.yaml：{skills: {name: [tags]}, rules: {...}}。
func tagsYAML(snap sync.Snapshot) string {
	skills, rules := map[string][]string{}, map[string][]string{}
	for _, e := range snap.Resources {
		if len(e.Tags) == 0 || e.Status != resources.StatusOK {
			continue
		}
		switch e.Kind {
		case resources.KindSkill:
			skills[e.Name] = e.Tags
		case resources.KindRule:
			rules[e.Name] = e.Tags
		}
	}
	if len(skills) == 0 && len(rules) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("version: 1\n")
	writeTagSection(&sb, "skills", skills)
	writeTagSection(&sb, "rules", rules)
	return sb.String()
}

func writeTagSection(sb *strings.Builder, key string, m map[string][]string) {
	if len(m) == 0 {
		return
	}
	sb.WriteString(key + ":\n")
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		sb.WriteString("  " + n + ": " + yamlList(m[n]) + "\n")
	}
}

func yamlStr(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

func yamlBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func yamlList(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	quoted := make([]string, len(items))
	for i, it := range items {
		quoted[i] = yamlStr(it)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
