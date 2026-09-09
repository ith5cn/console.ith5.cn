package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ith5/ith5/internal/core"
)

func main() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	bad, n := 0, 0
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) != 4 {
			fmt.Fprintf(os.Stderr, "格式异常: %.60s\n", line)
			bad++
			continue
		}
		kind, name, want, filesJSON := core.Kind(parts[0]), parts[1], parts[2], parts[3]
		var files []core.File
		if err := json.Unmarshal([]byte(filesJSON), &files); err != nil {
			fmt.Fprintf(os.Stderr, "%s/%s: files 解析失败: %v\n", kind, name, err)
			bad++
			continue
		}
		n++
		// 复算 checksum：客户端落盘前会做同样的事，对不上就拒装
		got, err := core.Checksum(files)
		if err != nil {
			panic(err)
		}
		if got != want {
			fmt.Fprintf(os.Stderr, "%s/%s: checksum 不一致\n  存: %s\n  算: %s\n", kind, name, want, got)
			bad++
		}
		// 客户端 Store.Write 会再跑一次 ValidateFiles，这里提前验
		if err := core.ValidateForPublish(kind, name, files); err != nil {
			fmt.Fprintf(os.Stderr, "%s/%s: 发布校验失败: %v\n", kind, name, err)
			bad++
		}
	}
	if err := sc.Err(); err != nil {
		panic(err)
	}
	fmt.Printf("复算并校验 %d 个 bundle，问题 %d 个\n", n, bad)
	if bad > 0 {
		os.Exit(1)
	}
}
