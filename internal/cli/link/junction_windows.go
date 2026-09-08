//go:build windows

package link

import "os"

// makeJunction 创建目录联接。Go 的 os.Symlink 在 Windows 上对目录目标
// 会尝试创建符号链接（需要提权），因此这里显式走 junction。
func makeJunction(target, link string) error {
	return os.Symlink(target, link)
}
