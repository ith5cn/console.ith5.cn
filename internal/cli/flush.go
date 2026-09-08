package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ith5/ith5/internal/api"
)

const (
	// flushBatch 是单次上传的条数上限（技术方案 §13.1）。
	flushBatch = 200
	// FlushThreshold 是触发后台 flush 的队列条数（技术方案 §6.3）。
	FlushThreshold = 500
	// queueMaxBytes 是队列磁盘上限。达到上限时**保留旧事件**（D5）：
	// 审计链不因新事件而丢失早期记录。
	queueMaxBytes = 32 << 20
)

func (p Paths) QueueFile() string   { return filepath.Join(p.Home, "queue", "events.ndjson") }
func (p Paths) InflightDir() string { return filepath.Join(p.Home, "queue", "inflight") }

// FlushEvents 上传本地队列。
//
// 流程刻意如此（技术方案 §10.4）：
//  1. 先把活跃文件**原子 rename** 成 inflight 批次，而不是原地截断
//     —— hook 进程可能正在 append，截断会丢事件
//  2. 逐批上传；成功才删除 inflight 文件
//  3. 上传失败保留文件，下次重试；事件自带 UUID，服务端幂等
func FlushEvents(ctx context.Context, p Paths, c *Client) (sent int, err error) {
	if err := os.MkdirAll(p.InflightDir(), 0o700); err != nil {
		return 0, err
	}
	if err := rotateQueue(p); err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(p.InflightDir())
	if err != nil {
		return 0, err
	}
	for _, e := range entries {
		path := filepath.Join(p.InflightDir(), e.Name())
		n, err := flushFile(ctx, path, c)
		sent += n
		if err != nil {
			// 保留文件下次重试，不中断其余批次
			return sent, err
		}
		os.Remove(path)
	}
	return sent, nil
}

// rotateQueue 把活跃队列原子搬到 inflight。
func rotateQueue(p Paths) error {
	q := p.QueueFile()
	fi, err := os.Stat(q)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Size() == 0 {
		return nil
	}
	dst := filepath.Join(p.InflightDir(), fmt.Sprintf("batch-%d.ndjson", time.Now().UnixNano()))
	return os.Rename(q, dst)
}

func flushFile(ctx context.Context, path string, c *Client) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 8192), 1<<20)
	var batch []api.ExecutionEventJSON
	sent := 0

	send := func() error {
		if len(batch) == 0 {
			return nil
		}
		n, err := c.ReportExecutionEvents(ctx, batch)
		if err != nil {
			return err
		}
		sent += n
		batch = batch[:0]
		return nil
	}

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev api.ExecutionEventJSON
		if err := json.Unmarshal(line, &ev); err != nil {
			// 坏行直接丢弃：它进不了库，留着只会反复失败
			continue
		}
		batch = append(batch, ev)
		if len(batch) >= flushBatch {
			if err := send(); err != nil {
				return sent, err
			}
		}
	}
	if err := sc.Err(); err != nil {
		return sent, err
	}
	return sent, send()
}

// QueueDepth 返回待上传事件数，供 status 展示。
func QueueDepth(p Paths) int {
	n := countLines(p.QueueFile())
	if entries, err := os.ReadDir(p.InflightDir()); err == nil {
		for _, e := range entries {
			n += countLines(filepath.Join(p.InflightDir(), e.Name()))
		}
	}
	return n
}

func countLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 8192), 1<<20)
	n := 0
	for sc.Scan() {
		if len(sc.Bytes()) > 0 {
			n++
		}
	}
	return n
}

// QueueOverLimit 判断队列是否超出磁盘上限。
// 超限时保留旧事件并告警，不静默丢弃（D5）。
func QueueOverLimit(p Paths) bool {
	fi, err := os.Stat(p.QueueFile())
	return err == nil && fi.Size() > queueMaxBytes
}
