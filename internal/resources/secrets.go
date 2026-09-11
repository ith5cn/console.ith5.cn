package resources

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// SecretHit 是一次密钥命中：哪个文件的第几行、像哪种密钥。不回传命中的内容本身。
type SecretHit struct {
	Path string `json:"path,omitempty"`
	Kind string `json:"kind"`
	Line int    `json:"line"`
}

// SecretError 表示内容里疑似有密钥。按设计拒绝而不是自动打码：作者知道该改哪一行。
type SecretError struct{ Hits []SecretHit }

func (e *SecretError) Error() string {
	if len(e.Hits) == 0 {
		return "resources: 内容疑似包含密钥"
	}
	h := e.Hits[0]
	where := fmt.Sprintf("第 %d 行", h.Line)
	if h.Path != "" {
		where = h.Path + " " + where
	}
	return fmt.Sprintf("resources: 内容疑似包含密钥（%s，%s）", where, h.Kind)
}

var secretPatterns = []struct {
	kind string
	re   *regexp.Regexp
}{
	{"private_key", regexp.MustCompile(`-----BEGIN (RSA |EC |OPENSSH |DSA |PGP )?PRIVATE KEY-----`)},
	{"aws_access_key", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{"github_token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}\b`)},
	{"gitlab_token", regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}\b`)},
	{"slack_token", regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`)},
	{"openai_key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`)},
	{"anthropic_key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}\b`)},
	{"bearer", regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._-]{24,}`)},
	{"connection_string", regexp.MustCompile(`(?i)\b(postgres|postgresql|mysql|mongodb(\+srv)?|redis|amqp)://[^\s:/]+:[^\s@/]{3,}@`)},
	{"kv_secret", regexp.MustCompile(`(?i)\b(password|passwd|secret|token|api[_-]?key|access[_-]?token|auth[_-]?token|client[_-]?secret)\s*[=:]\s*["']?[A-Za-z0-9+/._=-]{12,}`)},
}

// ScanSecrets 逐行扫描疑似密钥。宁可误报让作者改，也不把真密钥发给全团队。
func ScanSecrets(content string) []SecretHit {
	var hits []SecretHit
	for i, line := range strings.Split(content, "\n") {
		for _, p := range secretPatterns {
			if p.re.MatchString(line) {
				hits = append(hits, SecretHit{Kind: p.kind, Line: i + 1})
				break
			}
		}
	}
	return hits
}

// ScanFiles 扫描一个资源的全部文本文件；二进制与超大文件跳过。
func ScanFiles(files []File) error {
	var hits []SecretHit
	for _, f := range files {
		if !IsTextPath(f.Path) || len(f.Content) > MaxScanBytes {
			continue
		}
		for _, h := range ScanSecrets(f.Content) {
			h.Path = f.Path
			hits = append(hits, h)
		}
	}
	if len(hits) > 0 {
		return &SecretError{Hits: hits}
	}
	return nil
}

// MaxScanBytes 是单个文件参与扫描的上限；更大的文件几乎不可能是手写内容。
const MaxScanBytes = 1 << 20

var textExt = map[string]bool{
	".md": true, ".txt": true, ".yaml": true, ".yml": true, ".json": true, ".toml": true, ".ini": true, ".env": true,
	".sh": true, ".bash": true, ".zsh": true, ".ps1": true, ".js": true, ".ts": true, ".mjs": true, ".cjs": true,
	".py": true, ".rb": true, ".go": true, ".xml": true, ".html": true, ".css": true, ".sql": true, ".csv": true,
}

// IsTextPath 按扩展名判断是否值得逐行扫描；没有扩展名的（如 Dockerfile、Makefile）也算文本。
func IsTextPath(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	if ext == "" {
		return true
	}
	return textExt[ext]
}

// secretLikeEnvName 判断环境变量名是否像凭据：这类变量必须标记 secret: true，值只在本机保存。
var secretLikeEnvName = regexp.MustCompile(`(?i)(SECRET|TOKEN|PASSWORD|PASSWD|PRIVATE_KEY|API_KEY|ACCESS_KEY|CREDENTIAL)`)
