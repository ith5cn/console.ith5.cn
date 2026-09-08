package hook

import (
	"os"
	"path/filepath"
)

// Append 把一条事件原子地追加到队列文件。
//
// 用 O_APPEND 打开：同一文件可能被多个 hook 进程并发写。
// 单次写入小于 PIPE_BUF（4096）时，POSIX 保证 append 不交错，
// 因此不需要额外加锁 —— 加锁会把 hook 的耗时变得不可预测。
func Append(queuePath string, line []byte) error {
	if len(line) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(queuePath), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(queuePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(line)
	return err
}
