// ith5-materialize 把服务端快照物化成 teamai 能直接读的 .teamai/ 目录。
//
// 它是验证工具，也是将来给 teamai-cli 写适配器的参考实现（docs/开发规格.md §5）：
//
//	ith5-materialize login  --server https://…            设备授权流登录，凭据存在 ~/.ith5/materialize.json
//	ith5-materialize bind   --project billing[,website]    把当前目录绑定到项目
//	ith5-materialize sync   [--out .teamai]                取快照、按哈希取内容、渲染落盘、回执
//	ith5-materialize push / contribute / remove            写方向，见 write.go
//	ith5-materialize projects / unbind / logout            列项目、解绑、登出
//
// 落盘规则：先写暂存目录，全部到齐再逐文件替换；目标文件哈希既非旧值也非新值说明用户改过，
// 标 conflict_skipped 不覆盖。journal 记在 <out>/.ith5-journal.json。
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/ith5/ith5/internal/sync"
	"github.com/ith5/ith5/internal/teamaifmt"
)

const journalFile = ".ith5-journal.json"

// version 随 X-Client-Version 头发给服务端；服务端 /v1/capabilities 的 min_client_version 低于它时拒绝。
var version = "0.1.0"

type credentials struct {
	Server       string `json:"server"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	MachineID    string `json:"machine_id"`
	BindingID    string `json:"binding_id,omitempty"`
	Workspace    string `json:"workspace,omitempty"`
}

// journal 记录上次物化写下的每个文件的哈希，用来判断用户是否改过。
type journal struct {
	Revision string            `json:"revision"`
	Layout   string            `json:"layout,omitempty"` // "" 扁平；"namespaced" 分区布局
	Files    map[string]string `json:"files"`            // 相对路径 -> sha256
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: ith5-materialize <login|projects|bind|sync|push|contribute|remove|unbind|logout> [选项]")
	}
	switch args[0] {
	case "login":
		return cmdLogin(args[1:])
	case "projects":
		return cmdProjects(args[1:])
	case "bind":
		return cmdBind(args[1:])
	case "sync":
		return cmdSync(args[1:])
	case "push":
		return cmdPush(args[1:])
	case "contribute":
		return cmdContribute(args[1:])
	case "remove":
		return cmdRemove(args[1:])
	case "unbind":
		return cmdUnbind(args[1:])
	case "logout":
		return cmdLogout(args[1:])
	}
	return fmt.Errorf("未知子命令 %q", args[0])
}

// ---------------------------------------------------------------
// login
// ---------------------------------------------------------------

func cmdLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	server := fs.String("server", "", "服务端地址，如 https://console.example")
	enroll := fs.String("enroll", "", "管理员发的接入码（可选）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *server == "" {
		return errors.New("--server 必填")
	}
	base := strings.TrimRight(*server, "/")
	host, _ := os.Hostname()

	// 先探一次能力：版本不够时在这里就说清楚，而不是在后面的某个请求上收到 426
	var caps struct {
		MinClientVersion string `json:"min_client_version"`
		APIVersion       string `json:"api_version"`
	}
	if err := call(base, "", http.MethodGet, "/v1/capabilities", nil, &caps); err != nil {
		return fmt.Errorf("连不上服务端 %s: %w", base, err)
	}
	if caps.APIVersion != "" && caps.APIVersion != "v1" {
		return fmt.Errorf("服务端 API 版本 %s 与本工具（v1）不兼容", caps.APIVersion)
	}

	var start struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURL string `json:"verification_url"`
		Interval        int    `json:"interval"`
	}
	if err := call(base, "", http.MethodPost, "/v1/auth/device/start", map[string]any{
		"fingerprint": fingerprint(), "hostname": host, "os": runtime.GOOS, "enrollment_code": *enroll,
	}, &start); err != nil {
		return err
	}
	fmt.Printf("请在浏览器打开 %s 并输入验证码: %s\n等待批准…\n", start.VerificationURL, start.UserCode)

	interval := time.Duration(max(start.Interval, 2)) * time.Second
	for {
		time.Sleep(interval)
		var tok struct {
			AccessToken          string   `json:"access_token"`
			RefreshToken         string   `json:"refresh_token"`
			MachineID            string   `json:"machine_id"`
			EnrollmentProjectIDs []string `json:"enrollment_project_ids"`
		}
		err := call(base, "", http.MethodPost, "/v1/auth/device/poll", map[string]any{"device_code": start.DeviceCode}, &tok)
		var he *httpError
		if errors.As(err, &he) && he.Code == "AUTHORIZATION_PENDING" {
			continue
		}
		if err != nil {
			return err
		}
		c := credentials{Server: base, AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken, MachineID: tok.MachineID}
		if err := saveCreds(c); err != nil {
			return err
		}
		fmt.Println("登录成功。")
		if len(tok.EnrollmentProjectIDs) > 0 {
			fmt.Printf("接入码限定的项目: %s\n下一步在项目目录执行: ith5-materialize bind --project-id %s\n",
				strings.Join(tok.EnrollmentProjectIDs, ","), strings.Join(tok.EnrollmentProjectIDs, ","))
		}
		return nil
	}
}

// ---------------------------------------------------------------
// bind
// ---------------------------------------------------------------

func cmdBind(args []string) error {
	fs := flag.NewFlagSet("bind", flag.ContinueOnError)
	slugs := fs.String("project", "", "项目 slug，逗号分隔")
	ids := fs.String("project-id", "", "项目 id，逗号分隔（与 --project 二选一）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := loadCreds()
	if err != nil {
		return err
	}
	var projectIDs []string
	if *ids != "" {
		projectIDs = strings.Split(*ids, ",")
	} else if *slugs != "" {
		var list struct {
			Items []struct {
				ID   string `json:"id"`
				Slug string `json:"slug"`
			} `json:"items"`
		}
		if err := callAuthed(&c, http.MethodGet, "/v1/projects", nil, &list); err != nil {
			return err
		}
		want := map[string]bool{}
		for _, s := range strings.Split(*slugs, ",") {
			want[strings.TrimSpace(s)] = true
		}
		for _, p := range list.Items {
			if want[p.Slug] {
				projectIDs = append(projectIDs, p.ID)
				delete(want, p.Slug)
			}
		}
		for s := range want {
			return fmt.Errorf("找不到项目 %q 或你不是它的成员", s)
		}
	} else {
		return errors.New("--project 或 --project-id 必填")
	}

	cwd, _ := os.Getwd()
	var b struct {
		ID string `json:"id"`
	}
	if err := callAuthed(&c, http.MethodPost, "/v1/bindings", map[string]any{
		"workspace_id": workspaceID(cwd), "display_name": filepath.Base(cwd), "project_ids": projectIDs,
	}, &b); err != nil {
		return err
	}
	c.BindingID, c.Workspace = b.ID, cwd
	if err := saveCreds(c); err != nil {
		return err
	}
	fmt.Printf("已绑定 %s → binding %s\n", cwd, b.ID)
	return nil
}

// ---------------------------------------------------------------
// sync
// ---------------------------------------------------------------

func cmdSync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	out := fs.String("out", ".teamai", "输出目录（teamai self 模式读取的目录）")
	force := fs.Bool("force", false, "忽略本地 revision，强制重新拉取并落盘")
	namespaced := fs.Bool("namespaced", false, "teamai 分区布局：skills/<ns>/、manifest/roles.yaml 与 projects.yaml；teamai init 需带 --project <slug>")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := loadCreds()
	if err != nil {
		return err
	}
	if c.BindingID == "" {
		return errors.New("尚未绑定，先执行 ith5-materialize bind")
	}
	jr := loadJournal(*out)
	// 布局切换会让 journal 里的路径全部失效：强制全量重来，避免把旧布局的文件当成用户改动
	wantLayout := ""
	if *namespaced {
		wantLayout = "namespaced"
	}
	if jr.Layout != wantLayout && len(jr.Files) > 0 {
		fmt.Println("布局改变，清理旧布局文件后重新物化。")
		purgeOwned(*out)
		jr = journal{Files: map[string]string{}}
	}

	// 1. 快照，带 If-None-Match
	req, _ := http.NewRequest(http.MethodGet, c.Server+"/v1/bindings/"+c.BindingID+"/snapshot", nil)
	req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	req.Header.Set("X-Client-Version", version)
	if jr.Revision != "" && !*force {
		req.Header.Set("If-None-Match", `"`+jr.Revision+`"`)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		fmt.Println("已是最新，无需变更。")
		return nil
	}
	if resp.StatusCode == http.StatusUnauthorized {
		if err := refresh(&c); err != nil {
			return fmt.Errorf("设备已撤销或凭据失效，请重新登录: %w", err)
		}
		return cmdSync(args)
	}
	if resp.StatusCode != http.StatusOK {
		return readError(resp)
	}
	var snap sync.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		return err
	}

	// 2. 按哈希取内容：本地缓存里有的不再下载
	cache := filepath.Join(home(), ".ith5", "blobs")
	_ = os.MkdirAll(cache, 0o755)
	read := func(sha string) ([]byte, error) {
		p := filepath.Join(cache, strings.TrimPrefix(sha, "sha256:"))
		if b, err := os.ReadFile(p); err == nil && "sha256:"+hexSum(b) == sha {
			return b, nil
		}
		var buf bytes.Buffer
		if err := callAuthedRaw(&c, "/v1/blobs/"+sha, &buf); err != nil {
			return nil, err
		}
		if "sha256:"+hexSum(buf.Bytes()) != sha {
			return nil, fmt.Errorf("blob %s 校验失败", sha)
		}
		_ = os.WriteFile(p, buf.Bytes(), 0o644)
		return buf.Bytes(), nil
	}

	// 3. 渲染成 teamai 仓库视图
	files, err := teamaifmt.RenderWith(snap, read, teamaifmt.Options{Namespaced: *namespaced})
	if err != nil {
		return err
	}

	// 4. 逐文件落盘，按 journal 判断归属
	results, newJournal := apply(*out, files, jr)
	newJournal.Revision, newJournal.Layout = snap.Revision, wantLayout
	if err := saveJournal(*out, newJournal); err != nil {
		return err
	}
	if err := saveSnapshot(*out, snap); err != nil {
		return err
	}

	// 5. 回执
	var rep []map[string]any
	for _, r := range results {
		rep = append(rep, map[string]any{"kind": r.kind, "name": r.name, "action": r.action})
	}
	if err := callAuthed(&c, http.MethodPost, "/v1/bindings/"+c.BindingID+"/sync-results",
		map[string]any{"applied_revision": snap.Revision, "results": rep}, nil); err != nil {
		fmt.Fprintln(os.Stderr, "回执失败（本地已更新，下次重试）:", err)
	}
	counts := map[string]int{}
	for _, r := range results {
		counts[r.action]++
	}
	fmt.Printf("同步完成 revision=%s  installed=%d updated=%d removed=%d conflict=%d\n",
		short(snap.Revision), counts["installed"], counts["updated"], counts["removed"], counts["conflict_skipped"])
	for _, e := range snap.Resources {
		if e.Status == "conflict" {
			fmt.Printf("  冲突未下发: %s/%s（%d 个项目给出不同版本）\n", e.Kind, e.Name, len(e.Conflict.Owners))
		}
	}
	return nil
}

type result struct{ kind, name, action string }

// apply 把渲染结果写到磁盘。
//
// 每个文件三种情况：目标不存在 → 写入 installed；目标哈希等于 journal 记的旧值 → 覆盖 updated；
// 目标哈希既不是旧值也不是新值 → 用户改过，conflict_skipped 不碰。journal 里有、这次没有 → removed。
func apply(out string, files map[string][]byte, old journal) ([]result, journal) {
	nj := journal{Files: map[string]string{}}
	var results []result
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		content := files[rel]
		want := hexSum(content)
		abs := filepath.Join(out, filepath.FromSlash(rel))
		kind, name := classify(rel)
		cur, err := os.ReadFile(abs)
		switch {
		case err != nil:
			if werr := writeFile(abs, content); werr != nil {
				results = append(results, result{kind, name, "failed"})
				continue
			}
			results = append(results, result{kind, name, "installed"})
		case hexSum(cur) == want:
			// 已是目标内容，无需动作
		case old.Files[rel] == hexSum(cur):
			if werr := writeFile(abs, content); werr != nil {
				results = append(results, result{kind, name, "failed"})
				continue
			}
			results = append(results, result{kind, name, "updated"})
		default:
			// 用户改过的文件归用户所有：不记进 journal，之后的删除步骤就不会把它当成我们的文件删掉
			results = append(results, result{kind, name, "conflict_skipped"})
			continue
		}
		nj.Files[rel] = want
	}
	// 上次有、这次没有：只删仍是我们写的那份
	for rel, sum := range old.Files {
		if _, keep := files[rel]; keep {
			continue
		}
		abs := filepath.Join(out, filepath.FromSlash(rel))
		kind, name := classify(rel)
		cur, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		if hexSum(cur) != sum {
			results = append(results, result{kind, name, "conflict_skipped"})
			continue
		}
		_ = os.Remove(abs)
		removeEmptyParents(out, abs)
		results = append(results, result{kind, name, "removed"})
	}
	return results, nj
}

// removeEmptyParents 删掉文件之后把空出来的目录一并清掉：teamai 把 skills/<ns>/ 下的每个目录都当作一个 skill，
// 留一个空目录会被当成重复或损坏的 skill。
func removeEmptyParents(root, file string) {
	dir := filepath.Dir(file)
	for dir != root && strings.HasPrefix(dir, root) {
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// classify 从渲染路径反推 kind / name，只用于回执展示。
func classify(rel string) (string, string) {
	parts := strings.SplitN(rel, "/", 3)
	switch {
	case len(parts) >= 2 && parts[0] == "skills":
		// 分区布局下是 skills/<ns>/<name>/...；扁平布局是 skills/<name>/...
		if len(parts) == 3 && strings.Contains(parts[2], "/") {
			return "skill", strings.SplitN(parts[2], "/", 2)[0]
		}
		return "skill", parts[1]
	case len(parts) == 2 && parts[0] == "rules":
		return "rule", strings.TrimSuffix(parts[1], ".md")
	case len(parts) >= 2 && parts[0] == "docs":
		return "doc", strings.TrimPrefix(rel, "docs/")
	case len(parts) == 2 && parts[0] == "agents":
		return "agent", strings.TrimSuffix(parts[1], ".yaml")
	case len(parts) == 3 && parts[0] == "claudemd":
		return "claudemd", strings.TrimSuffix(parts[2], ".md")
	case len(parts) == 2 && parts[0] == "learnings":
		return "learning", strings.TrimSuffix(parts[1], ".md")
	case rel == "env/env.yaml":
		return "env", "env.yaml"
	case strings.HasPrefix(rel, "hooks/"):
		return "hook", strings.TrimPrefix(rel, "hooks/")
	case rel == "mcp/mcp.yaml":
		return "mcp", "mcp.yaml"
	case rel == "culture.md":
		return "culture", "culture"
	case rel == "teamai.yaml" || rel == "tags.yaml":
		return "policy", rel
	}
	return "doc", rel
}

// ---------------------------------------------------------------
// HTTP
// ---------------------------------------------------------------

type httpError struct {
	Status  int
	Code    string
	Message string
}

func (e *httpError) Error() string { return fmt.Sprintf("%d %s: %s", e.Status, e.Code, e.Message) }

func call(base, token, method, path string, body, out any) error {
	return callWith(base, token, method, path, nil, body, out)
}

func callWith(base, token, method, path string, headers map[string]string, body, out any) error {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, err := http.NewRequest(method, base+path, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Client-Version", version)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return readError(resp)
	}
	if out != nil && resp.StatusCode != http.StatusNoContent {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func callAuthed(c *credentials, method, path string, body, out any) error {
	return callAuthedWith(c, method, path, nil, body, out)
}

// callAuthedWith 带额外请求头（如 If-Match）；401 时刷新一次令牌再试。
func callAuthedWith(c *credentials, method, path string, headers map[string]string, body, out any) error {
	err := callWith(c.Server, c.AccessToken, method, path, headers, body, out)
	var he *httpError
	if errors.As(err, &he) && he.Status == http.StatusUnauthorized {
		if rerr := refresh(c); rerr != nil {
			return err
		}
		return callWith(c.Server, c.AccessToken, method, path, headers, body, out)
	}
	return err
}

func callAuthedRaw(c *credentials, path string, w io.Writer) error {
	req, _ := http.NewRequest(http.MethodGet, c.Server+path, nil)
	req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	req.Header.Set("X-Client-Version", version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return readError(resp)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

func refresh(c *credentials) error {
	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := call(c.Server, "", http.MethodPost, "/v1/auth/token", map[string]any{"refresh_token": c.RefreshToken}, &tok); err != nil {
		return err
	}
	c.AccessToken, c.RefreshToken = tok.AccessToken, tok.RefreshToken
	return saveCreds(*c)
}

func readError(resp *http.Response) error {
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return &httpError{Status: resp.StatusCode, Code: body.Error.Code, Message: body.Error.Message}
}

// ---------------------------------------------------------------
// 本地状态
// ---------------------------------------------------------------

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

func credsPath() string { return filepath.Join(home(), ".ith5", "materialize.json") }

func loadCreds() (credentials, error) {
	var c credentials
	b, err := os.ReadFile(credsPath())
	if err != nil {
		return c, errors.New("尚未登录，先执行 ith5-materialize login --server …")
	}
	return c, json.Unmarshal(b, &c)
}

func saveCreds(c credentials) error {
	if err := os.MkdirAll(filepath.Dir(credsPath()), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(credsPath(), b, 0o600)
}

func loadJournal(out string) journal {
	var j journal
	b, err := os.ReadFile(filepath.Join(out, journalFile))
	if err == nil {
		_ = json.Unmarshal(b, &j)
	}
	if j.Files == nil {
		j.Files = map[string]string{}
	}
	return j
}

func saveJournal(out string, j journal) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(j, "", "  ")
	return os.WriteFile(filepath.Join(out, journalFile), b, 0o644)
}

func writeFile(abs string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	tmp := abs + ".ith5-tmp"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, abs)
}

func fingerprint() string {
	host, _ := os.Hostname()
	return hexSum([]byte(host + "|" + home()))[:32]
}

func workspaceID(dir string) string { return hexSum([]byte(dir))[:32] }

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

func jsonMarshalIndent(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }

func hexSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func short(rev string) string {
	if len(rev) > 19 {
		return rev[:19]
	}
	return rev
}
