package cli

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"syscall"
)

// Friendly 把底层错误翻译成开发者能照着做的提示。
//
// 原则：每条提示都要回答「我现在该干什么」。
// "connection refused" 对使用者毫无意义；"服务端连不上，检查 VPN" 才有。
func Friendly(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, ErrRevoked):
		return "此账号已被撤销访问权限。若这不符合预期，请联系管理员。"

	case errors.Is(err, ErrUnauthorized):
		return "登录已失效，请重新运行：ith5 login"

	case isOffline(err):
		return "连不上服务端。\n" +
			"  · 检查网络或 VPN 是否已连接\n" +
			"  · 已安装的内容仍然可用，恢复网络后再跑 ith5 sync 即可\n" +
			"  · 用 ith5 doctor 查看当前配置指向哪个服务端"

	case isDNS(err):
		return "服务端地址无法解析。检查 ith5 doctor 里显示的服务端地址是否正确。"

	case errors.Is(err, os.ErrPermission):
		return "文件权限不足。检查 ~/.ith5 与 ~/.claude 是否可写：\n" +
			"  ls -ld ~/.ith5 ~/.claude"

	case isDiskFull(err):
		return "磁盘空间不足，同步已中止。清理后重试；已安装的内容未受影响。"

	default:
		return err.Error()
	}
}

// FailOpen 判断这个错误是否应当「保留现状并继续」而非中止。
//
// 分发失败一律 fail-open（技术方案 §8.9）：拿不到更新远好于
// 把开发者已经装好的内容删掉。
func FailOpen(err error) bool {
	return isOffline(err) || isDNS(err)
}

func isOffline(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	for _, target := range []error{syscall.ECONNREFUSED, syscall.EHOSTUNREACH, syscall.ENETUNREACH, syscall.ETIMEDOUT} {
		if errors.Is(err, target) {
			return true
		}
	}
	s := err.Error()
	return strings.Contains(s, "connection refused") ||
		strings.Contains(s, "no route to host") ||
		strings.Contains(s, "network is unreachable")
}

func isDNS(err error) bool {
	var de *net.DNSError
	if errors.As(err, &de) {
		return true
	}
	var ue *url.Error
	return errors.As(err, &ue) && strings.Contains(err.Error(), "no such host")
}

func isDiskFull(err error) bool {
	return errors.Is(err, syscall.ENOSPC) || strings.Contains(err.Error(), "no space left")
}

// Hint 给错误补一句可执行的下一步。
func Hint(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s", Friendly(err))
}
