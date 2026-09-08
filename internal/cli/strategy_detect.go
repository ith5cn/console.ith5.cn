package cli

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/ith5/ith5/internal/cli/link"
	"github.com/ith5/ith5/internal/core"
)

// DetectStrategy 实测探测本机可用的指针策略（D10）。
//
// 必须是「建一个真实链接 → 读穿验证 → 删除」，不能只判断平台：
// 家目录在网络盘、非 NTFS 卷等情况下 junction 会失败，而这些
// 只有实测才知道。
func DetectStrategy(p Paths) (string, error) {
	if err := p.EnsureDirs(); err != nil {
		return "", err
	}
	probeSrc := filepath.Join(p.Staging, ".probe-src")
	probeLink := filepath.Join(p.Staging, ".probe-link")
	defer func() {
		os.Remove(probeLink)
		os.RemoveAll(probeSrc)
	}()

	if err := os.MkdirAll(probeSrc, 0o755); err != nil {
		return "copy", nil
	}
	if err := os.WriteFile(filepath.Join(probeSrc, "probe"), []byte("ok"), 0o644); err != nil {
		return "copy", nil
	}
	os.Remove(probeLink)
	if err := os.Symlink(probeSrc, probeLink); err != nil {
		return "copy", nil
	}
	// 读穿验证：链接建起来了不等于能用
	if b, err := os.ReadFile(filepath.Join(probeLink, "probe")); err != nil || string(b) != "ok" {
		return "copy", nil
	}
	if runtime.GOOS == "windows" {
		return "junction", nil
	}
	return "symlink", nil
}

// NewStrategy 按名字构造**目录形态**的策略。
func NewStrategy(id string, p Paths) link.Strategy {
	switch id {
	case "symlink":
		return link.Symlink{}
	case "junction":
		return link.Junction{}
	default:
		return link.Copy{Staging: p.Staging, Trash: p.Trash}
	}
}

// NewFileStrategy 构造**文件形态**的策略。
//
// 目前恒为复制（D13）。两个理由：
//
//  1. Windows 上文件形态**没有链接可选**——junction（mklink /J）只能指向
//     目录；指向文件要用文件符号链接，那正是本设计特意规避的、需要管理员
//     或开发者模式的原语。
//  2. Claude Code 是否读穿 agents/<name>.md 的符号链接尚未实测（S4）。
//     而文件形态的复制路径本就是一次原子 rename，比目录形态的复制还简单，
//     所以先做必然正确的那侧。S4 通过后再开 link，只是加速，不改语义。
func NewFileStrategy(p Paths) link.Strategy {
	return link.FileCopy{Staging: p.Staging, StoreRoot: p.Store}
}

// Strategies 按形态分派策略（技术方案 §8.3 的 (策略, 形态) 查表）。
type Strategies struct {
	Dir  link.Strategy
	File link.Strategy
}

func NewStrategies(id string, p Paths) Strategies {
	return Strategies{Dir: NewStrategy(id, p), File: NewFileStrategy(p)}
}

// For 返回该形态生效的策略。
func (s Strategies) For(shape core.Shape) link.Strategy {
	if shape == core.ShapeFile {
		return s.File
	}
	return s.Dir
}
