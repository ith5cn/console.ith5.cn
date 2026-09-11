package main

// 写方向：把本地 .teamai/ 的改动推回服务端。
//
//	ith5-materialize push       扫描 sync 之后改过或新增的资源，上传成变更集并提交
//	ith5-materialize contribute 分享一条经验（直接发布，服务端做密钥扫描）
//	ith5-materialize remove     删除一个资源（只含 delete 操作的变更集）
//	ith5-materialize projects   列出我有权限的项目
//	ith5-materialize unbind     解除当前目录的绑定，可选清理我们写下的文件
//	ith5-materialize logout     撤销本机设备并删除本地凭据
//
// 归属判断沿用 sync 的 journal：只有哈希与 journal 不同的文件才算改动。
// 资源到层级的映射来自上次 sync 保存的快照；新资源默认落在绑定的唯一项目，多项目时用 --project 指定。

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ith5/ith5/internal/resources"
	"github.com/ith5/ith5/internal/sync"
)

const snapshotFile = ".ith5-snapshot.json"

// op 是一条待提交的变更集操作，与服务端 OpJSON 对齐。
type op struct {
	Op        string              `json:"op"`
	Level     string              `json:"level"`
	TeamID    string              `json:"team_id,omitempty"`
	ProjectID string              `json:"project_id,omitempty"`
	Kind      string              `json:"kind"`
	Name      string              `json:"name"`
	Files     []resources.FileRef `json:"files,omitempty"`
	PrevVer   *int                `json:"expected_prev_version,omitempty"`
	blobs     map[string][]byte   // sha256 -> 内容，提交前上传
}

// localResource 是从磁盘反推出来的一个资源：kind、name 与它的文件。
type localResource struct {
	kind  resources.Kind
	name  string
	ns    string            // claudemd 的命名空间目录，其余为空
	files map[string][]byte // 资源内相对路径 -> 内容
	dirty bool
}

// ---------------------------------------------------------------
// push
// ---------------------------------------------------------------

func cmdPush(args []string) error {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	out := fs.String("out", ".teamai", "teamai 目录")
	title := fs.String("title", "", "变更集标题（默认按改动生成）")
	desc := fs.String("description", "", "变更集说明")
	project := fs.String("project", "", "新资源落到哪个项目（slug）；绑定只有一个项目时可省略")
	level := fs.String("level", "", "新资源的层级：org | team | project（默认 project）")
	fastTrack := fs.Bool("fast-track", false, "有发布权限时跳过审核直接发布")
	dryRun := fs.Bool("dry-run", false, "只打印将要提交的操作，不上传")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := loadCreds()
	if err != nil {
		return err
	}
	snap, err := loadSnapshot(*out)
	if err != nil {
		return err
	}
	jr := loadJournal(*out)

	locals, err := scanLocal(*out, jr.Layout == "namespaced")
	if err != nil {
		return err
	}
	var ops []op
	for _, lr := range locals {
		if !isDirty(lr, jr) {
			continue
		}
		o, err := toOp(lr, snap, c, *project, *level)
		if err != nil {
			return err
		}
		ops = append(ops, o)
	}
	if len(ops) == 0 {
		fmt.Println("没有改动。")
		return nil
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Kind+ops[i].Name < ops[j].Kind+ops[j].Name })

	fmt.Printf("将提交 %d 个操作：\n", len(ops))
	for _, o := range ops {
		fmt.Printf("  %-6s %s/%s  (%s %s, %d 个文件)\n", o.Op, o.Kind, o.Name, o.Level, scopeLabel(o, snap), len(o.Files))
	}
	if *dryRun {
		return nil
	}

	if *title == "" {
		*title = defaultTitle(ops)
	}
	cs, err := submitChangeset(&c, *title, *desc, ops, *fastTrack)
	if err != nil {
		return err
	}
	fmt.Printf("变更集 %s 已%s：%s/#/changesets/%s\n", short(cs.ID), stateLabel(cs.State), c.Server, cs.ID)
	return nil
}

// scanLocal 只认识我们自己渲染出来的目录布局；env / hooks / mcp 是合并后的 YAML，无法无损反推，跳过。
func scanLocal(out string, namespaced bool) ([]localResource, error) {
	var list []localResource
	byKey := map[string]*localResource{}
	add := func(kind resources.Kind, name, ns, rel string, content []byte) {
		key := string(kind) + "/" + ns + "/" + name
		lr, ok := byKey[key]
		if !ok {
			lr = &localResource{kind: kind, name: name, ns: ns, files: map[string][]byte{}}
			byKey[key] = lr
		}
		lr.files[rel] = content
	}
	err := filepath.WalkDir(out, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != out {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || strings.HasSuffix(d.Name(), ".ith5-tmp") {
			return nil
		}
		rel := filepath.ToSlash(strings.TrimPrefix(path, out+string(filepath.Separator)))
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		parts := strings.Split(rel, "/")
		switch {
		case parts[0] == "manifest":
			// 服务端生成的视图，不回推
		case namespaced && parts[0] == "skills" && len(parts) >= 4:
			add(resources.KindSkill, parts[2], parts[1], strings.Join(parts[3:], "/"), content)
		case !namespaced && parts[0] == "skills" && len(parts) >= 3:
			add(resources.KindSkill, parts[1], "", strings.Join(parts[2:], "/"), content)
		case namespaced && parts[0] == "rules" && len(parts) == 3 && strings.HasSuffix(rel, ".md"):
			add(resources.KindRule, strings.TrimSuffix(parts[2], ".md"), parts[1], "RULE.md", content)
		case parts[0] == "rules" && len(parts) == 2 && strings.HasSuffix(rel, ".md"):
			add(resources.KindRule, strings.TrimSuffix(parts[1], ".md"), "", "RULE.md", content)
		case parts[0] == "docs" && len(parts) >= 2:
			add(resources.KindDoc, strings.TrimPrefix(rel, "docs/"), "", "DOC.md", content)
		case parts[0] == "agents" && len(parts) == 2 && strings.HasSuffix(rel, ".yaml"):
			add(resources.KindAgent, strings.TrimSuffix(parts[1], ".yaml"), "", "AGENT.yaml", content)
		case parts[0] == "claudemd" && len(parts) == 3 && strings.HasSuffix(rel, ".md"):
			add(resources.KindClaudeMD, strings.TrimSuffix(parts[2], ".md"), parts[1], "CLAUDEMD.md", content)
		case rel == "culture.md":
			add(resources.KindCulture, "culture", "", "CULTURE.md", content)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, lr := range byKey {
		list = append(list, *lr)
	}
	sort.Slice(list, func(i, j int) bool {
		return string(list[i].kind)+"/"+list[i].name < string(list[j].kind)+"/"+list[j].name
	})
	return list, nil
}

// isDirty：资源里任一文件的哈希与 journal 不同，或 journal 里没有它。
func isDirty(lr localResource, jr journal) bool {
	for rel, content := range lr.files {
		if jr.Files[renderedPath(lr, rel)] != hexSum(content) {
			return true
		}
	}
	return false
}

// renderedPath 把资源内相对路径还原成 sync 渲染时的路径，用来查 journal。
func renderedPath(lr localResource, rel string) string {
	switch lr.kind {
	case resources.KindSkill:
		if lr.ns != "" {
			return "skills/" + lr.ns + "/" + lr.name + "/" + rel
		}
		return "skills/" + lr.name + "/" + rel
	case resources.KindRule:
		if lr.ns != "" {
			return "rules/" + lr.ns + "/" + lr.name + ".md"
		}
		return "rules/" + lr.name + ".md"
	case resources.KindDoc:
		return "docs/" + lr.name
	case resources.KindAgent:
		return "agents/" + lr.name + ".yaml"
	case resources.KindClaudeMD:
		return "claudemd/" + lr.ns + "/" + lr.name + ".md"
	case resources.KindCulture:
		return "culture.md"
	}
	return rel
}

// toOp 决定一个本地资源要提交到哪一层。已在快照里的沿用原层级；新资源按参数或绑定推断。
func toOp(lr localResource, snap sync.Snapshot, c credentials, projectSlug, level string) (op, error) {
	o := op{Op: "put", Kind: string(lr.kind), Name: lr.name, blobs: map[string][]byte{}}
	for rel, content := range lr.files {
		sha := "sha256:" + hexSum(content)
		o.Files = append(o.Files, resources.FileRef{Path: rel, SHA256: sha, Size: len(content)})
		o.blobs[sha] = content
	}
	sort.Slice(o.Files, func(i, j int) bool { return o.Files[i].Path < o.Files[j].Path })

	if e := findEntry(snap, lr); e != nil {
		o.Level = string(e.Level)
		v := e.Version
		o.PrevVer = &v
		switch e.Level {
		case resources.LevelProject:
			o.ProjectID = projectIDBySlug(snap, e.Namespace)
		case resources.LevelTeam:
			id, err := teamIDBySlug(c, e.Namespace)
			if err != nil {
				return o, err
			}
			o.TeamID = id
		}
		if o.Level != string(resources.LevelOrg) && o.ProjectID == "" && o.TeamID == "" {
			// 经权限组授予的资源命名空间是组 key，落点要按它真实的层级找；找不到就当 org 级
			o.Level = string(resources.LevelOrg)
		}
		return o, nil
	}

	// 新资源：分区布局下目录名就是项目 slug（common 表示组织级）
	if projectSlug == "" && lr.ns != "" && lr.ns != "common" {
		projectSlug = lr.ns
	}
	if level == "" && lr.ns == "common" {
		level = string(resources.LevelOrg)
	}
	if level == "" {
		level = string(resources.LevelProject)
	}
	o.Level = level
	switch resources.Level(level) {
	case resources.LevelOrg:
	case resources.LevelProject:
		if projectSlug == "" {
			if len(snap.Projects) != 1 {
				return o, fmt.Errorf("%s/%s 是新资源，绑定了多个项目，请用 --project 指定", lr.kind, lr.name)
			}
			o.ProjectID = snap.Projects[0].ID
		} else {
			o.ProjectID = projectIDBySlug(snap, projectSlug)
			if o.ProjectID == "" {
				return o, fmt.Errorf("当前绑定里没有项目 %q", projectSlug)
			}
		}
	case resources.LevelTeam:
		if projectSlug == "" {
			return o, errors.New("--level team 需要用 --project 指定团队 slug")
		}
		id, err := teamIDBySlug(c, projectSlug)
		if err != nil {
			return o, err
		}
		o.TeamID = id
	default:
		return o, fmt.Errorf("未知层级 %q", level)
	}
	return o, nil
}

func findEntry(snap sync.Snapshot, lr localResource) *resources.Entry {
	if lr.kind == resources.KindCulture {
		return snap.Culture
	}
	for i := range snap.Resources {
		e := &snap.Resources[i]
		if e.Kind != lr.kind || e.Name != lr.name {
			continue
		}
		if lr.ns == "" || e.Namespace == lr.ns || (lr.ns == "common" && (e.Namespace == "" || e.Level == resources.LevelOrg)) {
			return e
		}
	}
	return nil
}

func projectIDBySlug(snap sync.Snapshot, slug string) string {
	for _, p := range snap.Projects {
		if p.Slug == slug {
			return p.ID
		}
	}
	return ""
}

func teamIDBySlug(c credentials, slug string) (string, error) {
	var list struct {
		Items []struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
		} `json:"items"`
	}
	if err := callAuthed(&c, http.MethodGet, "/v1/teams", nil, &list); err != nil {
		return "", err
	}
	for _, t := range list.Items {
		if t.Slug == slug {
			return t.ID, nil
		}
	}
	return "", fmt.Errorf("找不到团队 %q", slug)
}

// ---------------------------------------------------------------
// 提交
// ---------------------------------------------------------------

type changesetResp struct {
	ID    string `json:"id"`
	State string `json:"state"`
	Title string `json:"title"`
	ETag  string `json:"etag"`
	Ops   []op   `json:"ops"`
}

// submitChangeset 上传 blob、创建（或续用）变更集并提交。
//
// 续用规则对应 teamai push 的「更新已存在的 MR」：我名下已有一个 draft / in_review 变更集碰到同一个资源，
// 就把新操作合并进去重新提交，而不是再开一个。
func submitChangeset(c *credentials, title, desc string, ops []op, fastTrack bool) (changesetResp, error) {
	for _, o := range ops {
		for sha, content := range o.blobs {
			if err := uploadBlob(c, sha, content); err != nil {
				return changesetResp{}, fmt.Errorf("上传 %s/%s: %w", o.Kind, o.Name, err)
			}
		}
	}
	body := map[string]any{"title": title, "description": desc, "ops": ops, "fast_track": fastTrack}

	var cs changesetResp
	if existing, ok := findOpenChangeset(c, ops); ok {
		body["ops"] = mergeOps(existing.Ops, ops)
		// 续用时保留原标题，除非调用者明确给了新的
		if existing.Title != "" && strings.HasPrefix(title, "push: ") || strings.HasPrefix(title, "put ") || strings.HasPrefix(title, "remove ") {
			body["title"] = existing.Title
		}
		if err := callAuthedWith(c, http.MethodPatch, "/v1/change-sets/"+existing.ID, map[string]string{"If-Match": `"` + existing.ETag + `"`}, body, &cs); err != nil {
			return cs, err
		}
		fmt.Printf("合并进已有变更集 %s\n", short(existing.ID))
	} else if err := callAuthed(c, http.MethodPost, "/v1/change-sets", body, &cs); err != nil {
		return cs, err
	}
	if err := callAuthed(c, http.MethodPost, "/v1/change-sets/"+cs.ID+"/submit", nil, &cs); err != nil {
		return cs, err
	}
	if fastTrack {
		if err := callAuthed(c, http.MethodPost, "/v1/change-sets/"+cs.ID+"/publish", nil, &cs); err != nil {
			return cs, fmt.Errorf("已提交但发布失败（变更集停在 %s）: %w", cs.State, err)
		}
	}
	return cs, nil
}

func uploadBlob(c *credentials, sha string, content []byte) error {
	req, _ := http.NewRequest(http.MethodPost, c.Server+"/v1/blobs", bytes.NewReader(content))
	req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Client-Version", version)
	req.Header.Set("X-Sha256", sha)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		if rerr := refresh(c); rerr != nil {
			return readError(resp)
		}
		return uploadBlob(c, sha, content)
	}
	if resp.StatusCode >= 400 {
		return readError(resp)
	}
	return nil
}

func findOpenChangeset(c *credentials, ops []op) (changesetResp, bool) {
	var list struct {
		Items []changesetResp `json:"items"`
	}
	if err := callAuthed(c, http.MethodGet, "/v1/change-sets?view=mine", nil, &list); err != nil {
		return changesetResp{}, false
	}
	for _, cs := range list.Items {
		if cs.State != "draft" && cs.State != "in_review" {
			continue
		}
		for _, have := range cs.Ops {
			for _, want := range ops {
				if have.Kind == want.Kind && have.Name == want.Name && have.Level == want.Level && have.ProjectID == want.ProjectID && have.TeamID == want.TeamID {
					var full changesetResp
					if err := callAuthed(c, http.MethodGet, "/v1/change-sets/"+cs.ID, nil, &full); err != nil {
						return changesetResp{}, false
					}
					return full, true
				}
			}
		}
	}
	return changesetResp{}, false
}

// mergeOps 用新操作替换同键的旧操作，其余保留。
func mergeOps(old, add []op) []op {
	key := func(o op) string { return o.Level + "|" + o.TeamID + "|" + o.ProjectID + "|" + o.Kind + "|" + o.Name }
	replaced := map[string]bool{}
	for _, o := range add {
		replaced[key(o)] = true
	}
	var out []op
	for _, o := range old {
		if !replaced[key(o)] {
			o.blobs = nil
			out = append(out, o)
		}
	}
	return append(out, add...)
}

func defaultTitle(ops []op) string {
	if len(ops) == 1 {
		return fmt.Sprintf("%s %s/%s", ops[0].Op, ops[0].Kind, ops[0].Name)
	}
	return fmt.Sprintf("push: %d 个资源", len(ops))
}

func scopeLabel(o op, snap sync.Snapshot) string {
	for _, p := range snap.Projects {
		if p.ID == o.ProjectID {
			return p.Slug
		}
	}
	if o.TeamID != "" {
		return "team " + short(o.TeamID)
	}
	return "org"
}

func stateLabel(state string) string {
	switch state {
	case "in_review":
		return "提交审核"
	case "published":
		return "发布"
	case "approved":
		return "批准"
	}
	return state
}

// ---------------------------------------------------------------
// contribute / remove
// ---------------------------------------------------------------

func cmdContribute(args []string) error {
	fs := flag.NewFlagSet("contribute", flag.ContinueOnError)
	title := fs.String("title", "", "标题（必填）")
	project := fs.String("project", "", "项目 slug；省略则组织共享")
	tags := fs.String("tags", "", "标签，逗号分隔")
	out := fs.String("out", ".teamai", "teamai 目录，用来解析项目 slug")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *title == "" || fs.NArg() != 1 {
		return errors.New("用法: ith5-materialize contribute --title 标题 [--project slug] [--tags a,b] <文件.md>")
	}
	c, err := loadCreds()
	if err != nil {
		return err
	}
	content, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	body := map[string]any{"title": *title, "content": string(content)}
	if *tags != "" {
		body["tags"] = strings.Split(*tags, ",")
	}
	if *project != "" {
		snap, err := loadSnapshot(*out)
		if err != nil {
			return err
		}
		id := projectIDBySlug(snap, *project)
		if id == "" {
			return fmt.Errorf("当前绑定里没有项目 %q", *project)
		}
		body["project_id"] = id
	}
	var cs changesetResp
	if err := callAuthed(&c, http.MethodPost, "/v1/learnings", body, &cs); err != nil {
		var he *httpError
		if errors.As(err, &he) && he.Code == "SECRET_DETECTED" {
			return fmt.Errorf("内容含疑似密钥，已拒绝：%s", he.Message)
		}
		return err
	}
	fmt.Printf("经验已%s（变更集 %s）。下次 sync 后可在 learnings/ 看到。\n", stateLabel(cs.State), short(cs.ID))
	return nil
}

func cmdRemove(args []string) error {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	kind := fs.String("kind", "", "资源类型，如 rule")
	name := fs.String("name", "", "资源名")
	out := fs.String("out", ".teamai", "teamai 目录")
	fastTrack := fs.Bool("fast-track", false, "有发布权限时直接发布")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *kind == "" || *name == "" {
		return errors.New("用法: ith5-materialize remove --kind rule --name naming")
	}
	c, err := loadCreds()
	if err != nil {
		return err
	}
	snap, err := loadSnapshot(*out)
	if err != nil {
		return err
	}
	lr := localResource{kind: resources.Kind(*kind), name: *name}
	e := findEntry(snap, lr)
	if e == nil {
		return fmt.Errorf("快照里没有 %s/%s，先 sync 或检查名字", *kind, *name)
	}
	o := op{Op: "delete", Level: string(e.Level), Kind: *kind, Name: *name}
	switch e.Level {
	case resources.LevelProject:
		o.ProjectID = projectIDBySlug(snap, e.Namespace)
	case resources.LevelTeam:
		if o.TeamID, err = teamIDBySlug(c, e.Namespace); err != nil {
			return err
		}
	}
	cs, err := submitChangeset(&c, fmt.Sprintf("remove %s/%s", *kind, *name), "", []op{o}, *fastTrack)
	if err != nil {
		return err
	}
	fmt.Printf("删除已%s：%s/#/changesets/%s\n", stateLabel(cs.State), c.Server, cs.ID)
	return nil
}

// ---------------------------------------------------------------
// projects / unbind / logout
// ---------------------------------------------------------------

func cmdProjects(args []string) error {
	c, err := loadCreds()
	if err != nil {
		return err
	}
	var list struct {
		Items []struct {
			ID       string `json:"id"`
			Slug     string `json:"slug"`
			Name     string `json:"name"`
			Archived bool   `json:"archived"`
		} `json:"items"`
	}
	if err := callAuthed(&c, http.MethodGet, "/v1/projects", nil, &list); err != nil {
		return err
	}
	if len(list.Items) == 0 {
		fmt.Println("你还不属于任何项目，找管理员要接入码或把你加进项目。")
		return nil
	}
	for _, p := range list.Items {
		mark := " "
		if p.Archived {
			mark = "x"
		}
		fmt.Printf("[%s] %-20s %s\n", mark, p.Slug, p.Name)
	}
	fmt.Println("\n绑定：ith5-materialize bind --project <slug>[,<slug>]")
	return nil
}

func cmdUnbind(args []string) error {
	fs := flag.NewFlagSet("unbind", flag.ContinueOnError)
	out := fs.String("out", ".teamai", "teamai 目录")
	purge := fs.Bool("purge", false, "同时删除我们写下且未被改动过的文件")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := loadCreds()
	if err != nil {
		return err
	}
	if c.BindingID == "" {
		return errors.New("当前没有绑定")
	}
	if err := callAuthed(&c, http.MethodDelete, "/v1/bindings/"+c.BindingID, nil, nil); err != nil {
		var he *httpError
		if !errors.As(err, &he) || he.Status != http.StatusNotFound {
			return err
		}
	}
	c.BindingID, c.Workspace = "", ""
	if err := saveCreds(c); err != nil {
		return err
	}
	fmt.Println("已解除绑定。")
	if *purge {
		n := purgeOwned(*out)
		fmt.Printf("已清理 %d 个文件（被你改过的保留）。\n", n)
	}
	return nil
}

func cmdLogout(args []string) error {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	out := fs.String("out", ".teamai", "teamai 目录")
	purge := fs.Bool("purge", false, "同时删除我们写下且未被改动过的文件")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := loadCreds()
	if err != nil {
		return err
	}
	if err := callAuthed(&c, http.MethodPost, "/v1/auth/revocations", map[string]string{"machine_id": c.MachineID}, nil); err != nil {
		fmt.Fprintln(os.Stderr, "服务端撤销失败（本地凭据仍会删除）:", err)
	}
	if err := os.Remove(credsPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Println("已登出，本机设备已撤销。")
	if *purge {
		n := purgeOwned(*out)
		fmt.Printf("已清理 %d 个文件（被你改过的保留）。\n", n)
	}
	return nil
}

// purgeOwned 删除 journal 记录的、哈希仍等于我们写下时的文件；用户改过的一律不动。
func purgeOwned(out string) int {
	jr := loadJournal(out)
	n := 0
	for rel, sum := range jr.Files {
		abs := filepath.Join(out, filepath.FromSlash(rel))
		cur, err := os.ReadFile(abs)
		if err != nil || hexSum(cur) != sum {
			continue
		}
		if os.Remove(abs) == nil {
			n++
			removeEmptyParents(out, abs)
		}
	}
	_ = os.Remove(filepath.Join(out, journalFile))
	_ = os.Remove(filepath.Join(out, snapshotFile))
	return n
}

// ---------------------------------------------------------------
// 快照缓存
// ---------------------------------------------------------------

func loadSnapshot(out string) (sync.Snapshot, error) {
	var snap sync.Snapshot
	b, err := os.ReadFile(filepath.Join(out, snapshotFile))
	if err != nil {
		return snap, errors.New("没有上次同步的快照，先执行 ith5-materialize sync")
	}
	return snap, jsonUnmarshal(b, &snap)
}

func saveSnapshot(out string, snap sync.Snapshot) error {
	b, _ := jsonMarshalIndent(snap)
	return os.WriteFile(filepath.Join(out, snapshotFile), b, 0o644)
}
