package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// VerifyBinary 校验随包分发的原生二进制是否与清单一致（D9）。
//
// ith5-hook 会被写进 Claude Code 的配置并在每次工具调用时执行 ——
// 它被替换掉的后果比普通文件严重得多，因此分发时带 SHA256SUMS，
// doctor 逐个核对。校验失败时**报错而不是静默降级**：
// 悄悄退回一个慢实现或不上报，比直接报错糟得多。
func VerifyBinary(binPath, sumsPath string) error {
	want, err := lookupSum(sumsPath, filepath.Base(binPath))
	if err != nil {
		return err
	}
	got, err := fileSHA256(binPath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("%s 的校验和不匹配\n  期望: %s\n  实际: %s\n"+
			"该文件可能已被替换，请重新安装", filepath.Base(binPath), want, got)
	}
	return nil
}

func lookupSum(sumsPath, name string) (string, error) {
	b, err := os.ReadFile(sumsPath)
	if err != nil {
		return "", fmt.Errorf("读取校验清单: %w", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if filepath.Base(strings.TrimPrefix(fields[1], "*")) == name {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("校验清单中没有 %s 的记录", name)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
