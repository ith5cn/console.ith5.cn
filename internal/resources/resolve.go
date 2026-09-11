package resources

import (
	"sort"
	"strings"
)

// Candidate 是解析输入的一条：某资源在某落点的当前版本。
type Candidate struct {
	Resource
	VersionID string
	Files     []FileRef
	// Namespace 是渲染到 teamai 仓库视图时用的目录名：项目 slug、权限组 key，org 级为 common。
	Namespace string
	// ViaGroup 非空表示这条是经权限组授予的，优先级最低。
	ViaGroup string
	// Content 只对 policy 与 culture 有值：入口文件正文，由 sync 层从 blob 读出后填入。
	// Resolve 本身不读 blob。
	Content string
}

// EntryStatus 是快照条目的状态。
type EntryStatus string

const (
	StatusOK       EntryStatus = "ok"
	StatusConflict EntryStatus = "conflict"
)

// Entry 是快照里的一条资源（docs/设计-同步协议.md §4）。
type Entry struct {
	Kind      Kind   `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Level     Level  `json:"level"`
	OwnerID   string `json:"-"`
	// ResourceID 随快照下发：客户端上报投票（vote_delta 的 bundle_id）时要用它指向具体资源。
	ResourceID string      `json:"resource_id,omitempty"`
	Version    int         `json:"version,omitempty"`
	VersionID  string      `json:"version_id,omitempty"`
	Checksum   string      `json:"sha256,omitempty"`
	Size       int         `json:"size,omitempty"`
	Tags       []string    `json:"tags,omitempty"`
	Files      []FileRef   `json:"files,omitempty"`
	Status     EntryStatus `json:"status"`
	Conflict   *Conflict   `json:"conflict,omitempty"`
}

// Conflict 描述同层级两个项目给出同名不同版本资源的情形。
type Conflict struct {
	Owners   []string `json:"owners"`
	Versions []int    `json:"versions"`
}

// ResolveInput 是一次快照解析的全部输入。
type ResolveInput struct {
	// OrgID 用于 org 级作用域。
	OrgID string
	// TeamIDs 是绑定项目所属的团队。
	TeamIDs []string
	// ProjectIDs 是绑定的项目。
	ProjectIDs []string
	// Candidates 是组织内全部有已发布版本的资源，含 tombstone；解析时按落点过滤。
	Candidates []Candidate
}

// ResolveOutput 是解析结果。
type ResolveOutput struct {
	Entries []Entry
	Policy  Policy
	// Culture 是最具体层级的 culture 条目，可能为 nil。
	Culture *Entry
}

// Resolve 按层级规则计算一份绑定应得的资源集合（docs/设计-资源类型与层级.md §4）。
//
// 规则：
//  1. 只取绑定范围内的候选：org 级全部；team 级限所属团队；project 级限绑定项目；
//     权限组授予的另算，优先级最低。
//  2. 同一 (kind, name) 取最具体层级；下层 tombstone 屏蔽上层同名资源。
//  3. 同层级两个项目给出同名不同版本 → conflict，客户端保留旧快照。
//  4. policy 字段级合并取更严者；culture 取最具体的一份。
//
// 纯函数：不访问数据库，便于穷举测试。
func Resolve(in ResolveInput) ResolveOutput {
	teams := toSet(in.TeamIDs)
	projects := toSet(in.ProjectIDs)

	byKey := map[Ref][]Candidate{}
	var policies []Candidate
	var cultures []Candidate
	for _, c := range in.Candidates {
		if c.OrgID != in.OrgID || c.Archived || c.Version <= 0 {
			continue
		}
		if !inScope(c, in.OrgID, teams, projects) {
			continue
		}
		switch c.Kind {
		case KindPolicy:
			policies = append(policies, c)
			continue
		case KindCulture:
			cultures = append(cultures, c)
			continue
		}
		k := MakeRef(c.Kind, c.Name)
		byKey[k] = append(byKey[k], c)
	}

	out := ResolveOutput{Policy: DefaultPolicy()}
	for _, cs := range byKey {
		if e, ok := pickOne(cs); ok {
			out.Entries = append(out.Entries, e)
		}
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		if out.Entries[i].Kind != out.Entries[j].Kind {
			return out.Entries[i].Kind < out.Entries[j].Kind
		}
		return out.Entries[i].Name < out.Entries[j].Name
	})

	// policy：按层级从宽到严依次合并
	sort.SliceStable(policies, func(i, j int) bool { return rank(policies[i]) < rank(policies[j]) })
	for _, p := range policies {
		if p.Deleted {
			continue
		}
		if parsed, ok := ParsePolicy(p.Content); ok {
			out.Policy = MergePolicy(out.Policy, parsed)
		}
	}

	// culture：最具体的一份
	if best, ok := pickOne(cultures); ok {
		out.Culture = &best
	}
	return out
}

// inScope 判断候选是否落在绑定范围内。
func inScope(c Candidate, orgID string, teams, projects map[string]bool) bool {
	if c.ViaGroup != "" {
		// 权限组授予的资源本身仍有层级落点，但成员身份已由授权解析保证，只需 org 一致
		return true
	}
	switch c.Level {
	case LevelOrg:
		return c.OwnerID == orgID
	case LevelTeam:
		return teams[c.OwnerID]
	case LevelProject:
		return projects[c.OwnerID]
	}
	return false
}

// rank 越大越具体：权限组 0 < org 1 < team 2 < project 3。
func rank(c Candidate) int {
	if c.ViaGroup != "" {
		return 0
	}
	return c.Level.Specificity()
}

// pickOne 在同键候选里选出生效的一条。
func pickOne(cs []Candidate) (Entry, bool) {
	if len(cs) == 0 {
		return Entry{}, false
	}
	best := -1
	for _, c := range cs {
		if r := rank(c); r > best {
			best = r
		}
	}
	var top []Candidate
	for _, c := range cs {
		if rank(c) == best {
			top = append(top, c)
		}
	}
	// 同层级冲突：多个落点、版本或内容不同
	if len(top) > 1 {
		distinct := map[string]bool{}
		for _, c := range top {
			distinct[c.Checksum] = true
		}
		if len(distinct) > 1 {
			sort.Slice(top, func(i, j int) bool { return top[i].OwnerID < top[j].OwnerID })
			e := Entry{Kind: top[0].Kind, Name: top[0].Name, Level: top[0].Level, Status: StatusConflict, Conflict: &Conflict{}}
			for _, c := range top {
				e.Conflict.Owners = append(e.Conflict.Owners, c.OwnerID)
				e.Conflict.Versions = append(e.Conflict.Versions, c.Version)
			}
			return e, true
		}
	}
	c := top[0]
	if c.Deleted {
		// 最具体层级是 tombstone：屏蔽上层，条目不出现
		return Entry{}, false
	}
	e := Entry{
		Kind: c.Kind, Name: c.Name, Namespace: c.Namespace, Level: c.Level, OwnerID: c.OwnerID,
		ResourceID: c.ID, Version: c.Version, VersionID: c.VersionID, Checksum: c.Checksum,
		Tags: c.Tags, Files: CanonicalRefs(c.Files), Status: StatusOK,
	}
	for _, f := range e.Files {
		e.Size += f.Size
	}
	return e, true
}

func toSet(ids []string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// ---------------------------------------------------------------
// policy
// ---------------------------------------------------------------

// Policy 对应 teamai.yaml 的 sharing 块中受管理的字段。
type Policy struct {
	EnforcedRules       []string `json:"enforced_rules"`
	HooksAutoApply      bool     `json:"hooks_auto_apply"`
	HooksRequireScripts bool     `json:"hooks_require_team_scripts"`
	MCPAutoApply        bool     `json:"mcp_auto_apply"`
	MCPAllowedCommands  []string `json:"mcp_allowed_commands"`
	MCPAllowedHosts     []string `json:"mcp_allowed_hosts"`
	RecallEnabled       bool     `json:"recall_enabled"`
	ContributeHint      bool     `json:"contribute_hint"`
	CoAuthor            bool     `json:"co_author"`
}

// DefaultPolicy 是没有任何 policy 资源时的取值，与 teamai 的默认一致。
func DefaultPolicy() Policy {
	return Policy{HooksAutoApply: true, MCPAutoApply: true, ContributeHint: true, CoAuthor: true}
}

// MergePolicy 把下层 policy 合并进上层：能收紧的字段只能更严，可覆盖的字段以下层为准。
func MergePolicy(upper, lower Policy) Policy {
	out := upper
	out.EnforcedRules = union(upper.EnforcedRules, lower.EnforcedRules)
	// 上层 false 则下层不能改为 true
	out.HooksAutoApply = upper.HooksAutoApply && lower.HooksAutoApply
	out.MCPAutoApply = upper.MCPAutoApply && lower.MCPAutoApply
	// 上层 true 则保持
	out.HooksRequireScripts = upper.HooksRequireScripts || lower.HooksRequireScripts
	// 上层非空时下层只能是子集
	out.MCPAllowedCommands = narrow(upper.MCPAllowedCommands, lower.MCPAllowedCommands)
	out.MCPAllowedHosts = narrow(upper.MCPAllowedHosts, lower.MCPAllowedHosts)
	// 下层可覆盖
	out.RecallEnabled = lower.RecallEnabled
	out.ContributeHint = lower.ContributeHint
	out.CoAuthor = lower.CoAuthor
	return out
}

// ParsePolicy 解析 POLICY.yaml。只认顶层扁平键，未知键忽略，缺省键取默认值。
//
// 格式（与 teamai sharing 块一一对应，但扁平化以便零依赖解析）：
//
//	enforced_rules: [security-baseline, naming]
//	hooks_auto_apply: true
//	mcp_allowed_hosts: ["*.corp.example"]
func ParsePolicy(content string) (Policy, bool) {
	if strings.TrimSpace(content) == "" {
		return Policy{}, false
	}
	p := DefaultPolicy()
	for k, v := range topLevelScalars(content) {
		switch k {
		case "enforced_rules":
			p.EnforcedRules = parseInlineList(v)
		case "hooks_auto_apply":
			p.HooksAutoApply = yamlBool(v)
		case "hooks_require_team_scripts":
			p.HooksRequireScripts = yamlBool(v)
		case "mcp_auto_apply":
			p.MCPAutoApply = yamlBool(v)
		case "mcp_allowed_commands":
			p.MCPAllowedCommands = parseInlineList(v)
		case "mcp_allowed_hosts":
			p.MCPAllowedHosts = parseInlineList(v)
		case "recall_enabled":
			p.RecallEnabled = yamlBool(v)
		case "contribute_hint":
			p.ContributeHint = yamlBool(v)
		case "co_author":
			p.CoAuthor = yamlBool(v)
		}
	}
	return p, true
}

func yamlBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "yes", "on", "1":
		return true
	}
	return false
}

func union(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range append(append([]string{}, a...), b...) {
		if x != "" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

// narrow：上层为空表示不限制，取下层；上层非空则结果是交集（下层为空表示不再收窄）。
func narrow(upper, lower []string) []string {
	if len(upper) == 0 {
		return append([]string{}, lower...)
	}
	if len(lower) == 0 {
		return append([]string{}, upper...)
	}
	allowed := toSet(upper)
	var out []string
	for _, x := range lower {
		if allowed[x] {
			out = append(out, x)
		}
	}
	return out
}
