package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/ith5/ith5/internal/hook"
	"github.com/ith5/ith5/internal/watch"
)

// cmdWatch 起本机看板：跟着 trace 流实时显示当前在跑哪个 agent / skill，
// 以及 specs/*/tasks.md 的 task 进度。
//
// 它不联网、不需要登录 —— 看的是开发者自己这台机器上的东西。
func cmdWatch(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	port := fs.Int("port", 0, "监听端口，0 表示由系统分配")
	noOpen := fs.Bool("no-open", false, "不自动打开浏览器")
	project := fs.String("project", "", "读取 specs/ 的项目根目录，默认为当前目录")
	if err := fs.Parse(args); err != nil {
		return err
	}

	s, err := openSession(false)
	if err != nil {
		return err
	}

	// 打开 trace 开关。hook 默认不写 trace（它会把 task 描述落盘），
	// 由这条命令代表用户显式开启 —— 用户跑了 watch 就是想看这些。
	if !s.cfg.Trace.Enabled {
		s.cfg.Trace.Enabled = true
		if err := os.MkdirAll(s.paths.Home, 0o755); err != nil {
			return err
		}
		if err := s.cfg.Save(s.paths.ConfigFile()); err != nil {
			return fmt.Errorf("开启 trace: %w", err)
		}
		fmt.Println("已开启本机 trace（只写本机，不上传）")
	}

	// 看板没有 hook 就是一块永远空白的板子，而且不会报任何错。
	// 所以这里代表用户把缺的 hook 补上 —— InstallHooks 是合并式的、
	// 会备份、且幂等，不会动用户自己的 handler。
	if err := ensureHooks(s); err != nil {
		fmt.Fprintf(os.Stderr, "提示：未能自动配置 hook（%v）\n看板仍会启动，但在 hook 就位前不会有数据。\n", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := hook.TraceDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	projectDir := *project
	if projectDir == "" {
		if projectDir, err = os.Getwd(); err != nil {
			return err
		}
	}
	if projectDir, err = filepath.Abs(projectDir); err != nil {
		return err
	}

	st := watch.NewState()
	srv := watch.NewServer(st, projectDir)
	tailer := watch.NewTailer(dir, st)

	// 先把已有的 trace 读进来，页面一打开就有内容，而不是从空白开始。
	tailer.Poll()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		cancel()
	}()

	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(done)
	}()
	go tailer.Run(done, srv.Broadcast)

	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	return watch.Serve(ctx, addr, srv, func(actual string) {
		url := "http://" + actual
		fmt.Printf("看板已启动 %s\n项目 %s\n按 Ctrl-C 退出\n", url, projectDir)
		if !*noOpen {
			_ = openBrowser(url)
		}
	})
}

// ensureHooks 把 hook 装上或升级到当前期望的形态。
//
// 无条件跑一次即可：InstallHooks 是合并式、幂等的，已装齐时返回
// changed=false 什么也不打印；而已装的老 matcher 可能还没有 Agent|Skill，
// 正需要它顺手升级过来。与 login 那次共用同一个实现，
// 区别只是这里不需要登录 —— 看板是纯本机功能。
func ensureHooks(s *session) error {
	return installHooks(s)
}
