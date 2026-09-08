//go:build !windows

package link

import (
	"fmt"
	"runtime"
)

// makeJunction 在非 Windows 平台不可用。Junction 策略只应在 Windows 上被选中。
func makeJunction(target, link string) error {
	return fmt.Errorf("目录联接仅 Windows 可用，当前平台 %s", runtime.GOOS)
}
