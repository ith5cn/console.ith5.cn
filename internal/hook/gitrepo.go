package hook

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// maxWalkUp 限制向上查找 .git 的层数，避免在深路径上浪费时间。
const maxWalkUp = 24

// FindRepo 从 dir 向上查找 git 仓库，返回仓库根与归一化后的 remote。
//
// **直接读 .git/config，不调用 git 子进程** —— 起一个 git 进程要几十毫秒，
// 而整个 hook 的预算是 50ms（技术方案 §10.5）。读一个小文件是微秒级。
func FindRepo(dir string) (root, remote string) {
	if dir == "" {
		return "", ""
	}
	for i := 0; i < maxWalkUp && dir != "" && dir != "/"; i++ {
		gitPath := filepath.Join(dir, ".git")
		if fi, err := os.Stat(gitPath); err == nil {
			cfg := filepath.Join(gitPath, "config")
			if fi.Mode().IsRegular() {
				// worktree 或 submodule：.git 是文件，指向真正的 git 目录
				if real := readGitdirPointer(gitPath, dir); real != "" {
					cfg = filepath.Join(real, "config")
				}
			}
			return dir, NormalizeRemote(readOriginURL(cfg))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", ""
}

func readGitdirPointer(file, base string) string {
	b, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(b))
	p, ok := strings.CutPrefix(line, "gitdir:")
	if !ok {
		return ""
	}
	p = strings.TrimSpace(p)
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	return p
}

// readOriginURL 从 .git/config 里取 [remote "origin"] 的 url。
func readOriginURL(cfgPath string) string {
	f, err := os.Open(cfgPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 8192), 64*1024)
	inOrigin := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			inOrigin = strings.HasPrefix(line, `[remote "origin"]`)
			continue
		}
		if !inOrigin {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == "url" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// NormalizeRemote 把各种 remote 写法统一成 host/path。
//
//	git@github.com:acme/web.git      → github.com/acme/web
//	https://u:p@github.com/acme/web  → github.com/acme/web
//
// 归一化的目的有两个：后台按 repo 聚合时不能因写法不同而分裂；
// 以及**剥掉 URL 里可能内嵌的凭据** —— 那属于 L0 禁运。
func NormalizeRemote(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	// scp 风格：git@host:path
	if !strings.Contains(s, "://") {
		if at := strings.LastIndex(s, "@"); at >= 0 {
			s = s[at+1:]
		}
		s = strings.Replace(s, ":", "/", 1)
	} else {
		if i := strings.Index(s, "://"); i >= 0 {
			s = s[i+3:]
		}
		if at := strings.LastIndex(s, "@"); at >= 0 {
			s = s[at+1:] // 剥掉 user:password@
		}
	}
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	if i := strings.Index(s, "/"); i > 0 {
		return strings.ToLower(s[:i]) + s[i:]
	}
	return strings.ToLower(s)
}
