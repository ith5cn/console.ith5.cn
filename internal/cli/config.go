package cli

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
)

// Config 是本机配置。
type Config struct {
	Server      string `json:"server"`
	ClaudeHome  string `json:"claude_home,omitempty"`
	Fingerprint string `json:"fingerprint"`
	// LinkStrategy 在 login/doctor 时实测探测并固化，不自动来回切换（D10）：
	// 自动切换会导致同一台机器上出现两种归属判定方式，排障困难。
	LinkStrategy string `json:"link_strategy"`
	Telemetry    struct {
		Enabled bool `json:"enabled"`
	} `json:"telemetry"`
	// Trace 控制本机 trace 流（~/.ith5/trace/），只服务 `ith5 watch` 看板，
	// 永不上传。必须声明在这里：Save 会整体覆写 config.json，
	// 漏掉字段等于每次 login/sync 都把用户打开的开关悄悄关掉。
	Trace struct {
		Enabled bool `json:"enabled"`
	} `json:"trace"`
}

// Credentials 存令牌。权限 0600。
//
// V1 刻意不用系统钥匙串：可用的原生模块都要为四个平台出 prebuild，
// 而我们已经要分发 hook 与 gitleaks 两个二进制，再加一个原生依赖
// 会把 npm 打包和完整性校验的面再扩一圈。钥匙串列为 V2 加固项。
type Credentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	MachineID    string `json:"machine_id"`
	UserEmail    string `json:"user_email"`
	OrgID        string `json:"org_id"`
}

func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("配置文件损坏: %w", err)
	}
	return &c, nil
}

func (c *Config) Save(path string) error { return writeJSONFile(path, c, 0o644) }

func LoadCredentials(path string) (*Credentials, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("尚未登录，请先运行 ith5 login")
	}
	if err != nil {
		return nil, err
	}
	var cr Credentials
	if err := json.Unmarshal(b, &cr); err != nil {
		return nil, fmt.Errorf("凭据文件损坏，请重新运行 ith5 login")
	}
	return &cr, nil
}

func (c *Credentials) Save(path string) error { return writeJSONFile(path, c, 0o600) }

func writeJSONFile(path string, v any, mode os.FileMode) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// NewFingerprint 生成设备指纹，首次登录时固化。
func NewFingerprint() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	host, _ := os.Hostname()
	return fmt.Sprintf("%s-%s-%s", runtime.GOOS, shortHost(host), hex.EncodeToString(b)), nil
}

func shortHost(h string) string {
	if len(h) > 24 {
		return h[:24]
	}
	if h == "" {
		return "unknown"
	}
	return h
}
