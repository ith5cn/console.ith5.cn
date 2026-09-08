package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/ith5/ith5/internal/cli"
	"github.com/ith5/ith5/internal/cli/link"
	"github.com/ith5/ith5/internal/core"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", cli.Friendly(err))
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`ith5 —— 公司 skill / command 分发客户端

用法:
  ith5 login     登录并绑定本机
  ith5 sync      同步公司分发的内容
  ith5 status    查看当前状态
  ith5 doctor    体检
  ith5 logout    登出（--purge 同时移除已安装内容）

环境变量:
  ITH5_SERVER    服务端地址（首次 login 时需要）
`)
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}
	ctx := context.Background()
	switch args[0] {
	case "login":
		return cmdLogin(ctx)
	case "sync":
		return cmdSync(ctx)
	case "status":
		return cmdStatus()
	case "doctor":
		return cmdDoctor()
	case "logout":
		return cmdLogout(args[1:])
	case "-h", "--help", "help":
		usage()
		return nil
	default:
		return fmt.Errorf("未知命令 %q，运行 ith5 --help 查看用法", args[0])
	}
}

// session 汇总一次命令所需的本地状态。
type session struct {
	paths cli.Paths
	cfg   *cli.Config
	creds *cli.Credentials
}

func openSession(needAuth bool) (*session, error) {
	cfg0 := &cli.Config{}
	p, err := cli.DefaultPaths("")
	if err != nil {
		return nil, err
	}
	cfg, err := cli.LoadConfig(p.ConfigFile())
	if err != nil {
		return nil, err
	}
	if cfg.ClaudeHome != "" {
		if p, err = cli.DefaultPaths(cfg.ClaudeHome); err != nil {
			return nil, err
		}
	}
	_ = cfg0
	s := &session{paths: p, cfg: cfg}
	if needAuth {
		cr, err := cli.LoadCredentials(p.CredentialsFile())
		if err != nil {
			return nil, err
		}
		s.creds = cr
	}
	return s, nil
}

func cmdLogin(ctx context.Context) error {
	s, err := openSession(false)
	if err != nil {
		return err
	}
	server := os.Getenv("ITH5_SERVER")
	if server == "" {
		server = s.cfg.Server
	}
	if server == "" {
		return errors.New("请设置 ITH5_SERVER 环境变量指向服务端地址")
	}

	// 预建目录 —— 尤其是 skills：Claude Code 会监视技能目录，但若会话启动
	// 时该目录尚不存在，新建后需重启才被监视。必须赶在用户第一次启动
	// Claude Code 之前建好，否则首次 sync 会表现为「装了但看不到」。
	if err := s.paths.EnsureDirs(); err != nil {
		return err
	}

	if s.cfg.Fingerprint == "" {
		fp, err := cli.NewFingerprint()
		if err != nil {
			return err
		}
		s.cfg.Fingerprint = fp
	}
	if s.cfg.LinkStrategy == "" {
		st, err := cli.DetectStrategy(s.paths)
		if err != nil {
			return err
		}
		s.cfg.LinkStrategy = st
		fmt.Printf("检测到本机可用的落盘方式: %s\n", st)
	}
	s.cfg.Server = server
	s.cfg.Telemetry.Enabled = true
	if err := s.cfg.Save(s.paths.ConfigFile()); err != nil {
		return err
	}

	c := cli.NewClient(server, "")
	host, _ := os.Hostname()
	start, err := c.DeviceStart(ctx, s.cfg.Fingerprint, host, runtime.GOOS)
	if err != nil {
		return err
	}

	fmt.Printf("\n  请在浏览器中打开:  %s\n  并输入验证码:      %s\n\n",
		start.VerificationURL, start.UserCode)
	_ = openBrowser(start.VerificationURL)
	fmt.Print("等待批准")

	deadline := time.Now().Add(time.Duration(start.ExpiresIn) * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(time.Duration(start.Interval) * time.Second)
		fmt.Print(".")
		tok, ok, err := c.DevicePoll(ctx, start.DeviceCode)
		if err != nil {
			fmt.Println()
			return err
		}
		if !ok {
			continue
		}
		fmt.Println()
		cr := &cli.Credentials{
			AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken,
			MachineID: tok.MachineID, UserEmail: tok.User.Email, OrgID: tok.User.OrgID,
		}
		if err := cr.Save(s.paths.CredentialsFile()); err != nil {
			return err
		}
		if err := installHooks(s); err != nil {
			fmt.Printf("⚠ hook 安装失败（审计上报将不可用）: %v\n", err)
		}
		fmt.Printf("✓ 已登录: %s\n\n下一步: ith5 sync\n", tok.User.Email)
		return nil
	}
	fmt.Println()
	return errors.New("验证码已过期，请重新运行 ith5 login")
}

// installHooks 把 ith5-hook 注入 Claude Code 的 settings.json。
//
// hook 二进制与 ith5 放在同一目录一起分发；找不到就跳过并告警，
// 而不是让整个登录失败——审计不可用是问题，登录不了是灾难。
func installHooks(s *session) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	binPath := filepath.Join(filepath.Dir(exe), "ith5-hook")
	if _, err := os.Stat(binPath); err != nil {
		if p, lookErr := exec.LookPath("ith5-hook"); lookErr == nil {
			binPath = p
		} else {
			return fmt.Errorf("未找到 ith5-hook（应与 ith5 放在同一目录）")
		}
	}
	changed, err := cli.InstallHooks(s.paths.ClaudeHome, binPath)
	if err != nil {
		return err
	}
	if changed {
		fmt.Println("✓ 已配置执行审计上报（原有 hook 已保留，备份见 settings.json.ith5.bak）")
	}
	return nil
}

func cmdSync(ctx context.Context) error {
	s, err := openSession(true)
	if err != nil {
		return err
	}
	syncer := &cli.Syncer{
		Paths:      s.paths,
		Store:      cli.NewStore(s.paths.Store),
		Strategies: cli.NewStrategies(s.cfg.LinkStrategy, s.paths),
		Client:     cli.NewClient(s.cfg.Server, s.creds.AccessToken),
		Server:     s.cfg.Server,
	}

	res, err := syncer.Run(ctx)
	if errors.Is(err, cli.ErrRevoked) {
		return handleRevoked(s)
	}
	if errors.Is(err, cli.ErrUnauthorized) {
		// 访问令牌过期，用刷新令牌续一次
		c := cli.NewClient(s.cfg.Server, "")
		tok, rerr := c.Refresh(ctx, s.creds.RefreshToken)
		if errors.Is(rerr, cli.ErrRevoked) {
			return handleRevoked(s)
		}
		if rerr != nil {
			return fmt.Errorf("登录已失效，请重新运行 ith5 login")
		}
		s.creds.AccessToken = tok.AccessToken
		if err := s.creds.Save(s.paths.CredentialsFile()); err != nil {
			return err
		}
		syncer.Client = cli.NewClient(s.cfg.Server, tok.AccessToken)
		res, err = syncer.Run(ctx)
	}
	if err != nil {
		// 分发失败一律 fail-open：拿不到更新，远好于把开发者
		// 已经装好的内容删掉（技术方案 §8.9）
		if cli.FailOpen(err) {
			fmt.Println(cli.Friendly(err))
			if lock, _, lerr := cli.LoadLock(s.paths.LockFile()); lerr == nil && len(lock.Bundles) > 0 {
				fmt.Printf("\n本机已安装的 %d 项内容保持不变。\n", len(lock.Bundles))
			}
			return nil
		}
		return err
	}

	sum := res.Summary
	if res.Rebuilt > 0 {
		fmt.Printf("（本地账本已由现有内容自动重建，%d 项）\n", res.Rebuilt)
	}
	parts := []string{}
	add := func(n int, label string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", label, n))
		}
	}
	add(sum.Install, "+新增")
	add(sum.Update, "~更新")
	add(sum.Relabel, "·版本对齐")
	add(sum.Remove, "-移除")
	add(sum.Conflict, "!受阻")
	if len(parts) == 0 {
		fmt.Printf("已是最新（%d 项）\n", sum.Unchanged)
	} else {
		fmt.Printf("%s  （未变 %d）\n", strings.Join(parts, "  "), sum.Unchanged)
	}
	if res.EventsSent > 0 {
		fmt.Printf("已上报 %d 条执行记录\n", res.EventsSent)
	}
	for _, c := range res.Conflicts {
		// 两种成因的处置一样（都不碰），但员工该做的事完全不同：
		// 重名要改名或删掉自己的，本地改动是他自己动过、需要决定要不要保留。
		// 只说「已存在同名内容」会让改过文件的人一头雾水。
		switch c.Reason {
		case link.ReasonLocallyModified:
			fmt.Printf("  ! %s 跳过：本机这份被改动过，公司的新版本未覆盖你的改动\n", c.Name)
			fmt.Printf("    %s（想拿新版就删掉它再 sync）\n", c.Target)
		default:
			fmt.Printf("  ! %s 跳过：该路径已存在同名内容，未做任何改动\n", c.Name)
			fmt.Printf("    %s\n", c.Target)
		}
	}
	return nil
}

// handleRevoked 在收到撤权信号时清理本机。
//
// 这是离职回收里唯一挂在员工必经路径上的机制，但它能被绕过
// （不启动、离线、改二进制）。真正有效的是把敏感能力放在服务端 API
// 之后，本地那份只是空壳。
func handleRevoked(s *session) error {
	fmt.Println("此账号已被撤销访问权限，正在清理本机分发的内容…")
	lock, _, err := cli.LoadLock(s.paths.LockFile())
	if err == nil {
		st := cli.NewStrategies(s.cfg.LinkStrategy, s.paths)
		for name, e := range lock.Bundles {
			shape := lockShape(e)
			if err := st.For(shape).Release(s.paths.Target(shape, name)); err == nil {
				fmt.Printf("  已移除 %s\n", name)
			}
		}
	}
	_ = cli.UninstallHooks(s.paths.ClaudeHome)
	os.Remove(s.paths.LockFile())
	os.Remove(s.paths.CredentialsFile())
	os.RemoveAll(s.paths.Store)
	fmt.Println("清理完成。")
	return nil
}

func cmdStatus() error {
	s, err := openSession(false)
	if err != nil {
		return err
	}
	fmt.Printf("服务端    : %s\n", orNone(s.cfg.Server))
	// 分形态显示。同一台机器上两种形态可以走不同策略——Windows 尤其如此：
	// junction 只能指向目录，agent 那个单文件只能复制。
	// 只印一个值的话，「我这台是 junction」这句话是错的，排障会走偏。
	strategies := cli.NewStrategies(s.cfg.LinkStrategy, s.paths)
	if s.cfg.LinkStrategy == "" {
		fmt.Printf("落盘方式  : 未探测（运行 ith5 login 或 doctor）\n")
	} else {
		fmt.Printf("落盘方式  : skill %s ／ agent %s\n",
			strategies.Dir.ID(), strategies.File.ID())
	}
	if cr, err := cli.LoadCredentials(s.paths.CredentialsFile()); err == nil {
		fmt.Printf("已登录    : %s\n", cr.UserEmail)
	} else {
		fmt.Printf("已登录    : 否（运行 ith5 login）\n")
	}

	lock, rebuilt, err := cli.LoadLock(s.paths.LockFile())
	if err != nil {
		return err
	}
	if rebuilt {
		fmt.Println("本地账本  : 缺失或损坏（下次 sync 会自动重建）")
		return nil
	}
	fmt.Printf("待上报    : %d 条执行记录\n", cli.QueueDepth(s.paths))
	fmt.Printf("上次同步  : %s", lock.SyncedAt.Local().Format("2006-01-02 15:04:05"))
	if lock.Expired(time.Now()) {
		fmt.Print("（已过期）")
	}
	fmt.Printf("\n\n已安装 %d 项:\n", len(lock.Bundles))
	names := make([]string, 0, len(lock.Bundles))
	for n := range lock.Bundles {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		e := lock.Bundles[n]
		fmt.Printf("  %-24s v%-4d %-8s %s\n", n, e.Version, string(e.Kind),
			s.paths.RelTarget(lockShape(e), n))
	}
	return nil
}

func cmdDoctor() error {
	s, err := openSession(false)
	if err != nil {
		return err
	}
	ok := true
	check := func(name string, pass bool, detail string) {
		mark := "✓"
		if !pass {
			mark, ok = "✗", false
		}
		fmt.Printf("  %s %-28s %s\n", mark, name, detail)
	}
	fmt.Println("体检:")

	check("配置文件", s.cfg.Server != "", orNone(s.cfg.Server))
	_, credErr := cli.LoadCredentials(s.paths.CredentialsFile())
	check("登录凭据", credErr == nil, ifErr(credErr, "已登录"))

	if fi, err := os.Stat(s.paths.CredentialsFile()); err == nil {
		mode := fi.Mode().Perm()
		check("凭据文件权限", mode == 0o600, fmt.Sprintf("%o（应为 600）", mode))
	}

	_, dirErr := os.Stat(s.paths.Skills)
	check("技能目录", dirErr == nil, s.paths.Skills)

	detected, derr := cli.DetectStrategy(s.paths)
	st := cli.NewStrategies(s.cfg.LinkStrategy, s.paths)
	check("落盘方式可用", derr == nil,
		fmt.Sprintf("探测=%s 配置=%s（skill %s ／ agent %s）",
			detected, orNone(s.cfg.LinkStrategy), st.Dir.ID(), st.File.ID()))
	if s.cfg.LinkStrategy != "" && detected != s.cfg.LinkStrategy {
		fmt.Printf("    提示: 探测结果与配置不一致，如需切换请手工修改 %s\n", s.paths.ConfigFile())
	}

	// ADR-002 说我们自己不加锁，但不代表别人没加。
	// 系统级策略会让我们的 hook 或 skills 静默失效，界面上却一切正常。
	if conflicts := cli.CheckManagedSettings(); len(conflicts) > 0 {
		ok = false
		for _, c := range conflicts {
			fmt.Printf("  ✗ %-28s %s\n", "系统策略冲突", c.Key)
			fmt.Printf("    %s\n    来源: %s\n", c.Impact, c.Path)
		}
	} else {
		check("系统策略", true, "无冲突")
	}

	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		sums := filepath.Join(dir, "SHA256SUMS")
		hookBin := filepath.Join(dir, "ith5-hook")
		if _, statErr := os.Stat(sums); statErr == nil {
			vErr := cli.VerifyBinary(hookBin, sums)
			check("hook 二进制完整性", vErr == nil, ifErrMsg(vErr, "校验通过"))
		}
	}

	hooks, hErr := cli.HooksInstalled(s.paths.ClaudeHome)
	allHooks := hErr == nil
	var missing []string
	for ev, ok := range hooks {
		if !ok {
			allHooks = false
			missing = append(missing, ev)
		}
	}
	sort.Strings(missing)
	detail := "已安装"
	if len(missing) > 0 {
		detail = "缺少 " + strings.Join(missing, "、") + "（运行 ith5 login 重装）"
	}
	check("执行审计 hook", allHooks, detail)

	if cli.QueueOverLimit(s.paths) {
		check("上报队列", false, "已达磁盘上限，新事件将被丢弃；请检查网络或运行 ith5 sync")
	} else {
		check("上报队列", true, fmt.Sprintf("%d 条待上报", cli.QueueDepth(s.paths)))
	}

	// 悬空指针：用户误删 store 会让所有入口失效，且 Claude Code 静默读不到
	lock, _, _ := cli.LoadLock(s.paths.LockFile())
	dangling := 0
	for name, e := range lock.Bundles {
		// os.Stat 跟随链接：悬空的软链在这里会报错，正是我们要找的。
		// agent 的悬空比 skill 更隐蔽——skill 悬空时用户敲 /name 立刻发现，
		// agent 悬空只有在派发那一刻才失败，而那发生在长任务中途。
		if _, err := os.Stat(s.paths.Target(lockShape(e), name)); err != nil {
			dangling++
		}
	}
	check("入口完好", dangling == 0, fmt.Sprintf("%d 个悬空（运行 ith5 sync 修复）", dangling))

	if !ok {
		return errors.New("体检发现问题")
	}
	return nil
}

func cmdLogout(args []string) error {
	s, err := openSession(false)
	if err != nil {
		return err
	}
	purge := len(args) > 0 && args[0] == "--purge"
	if purge {
		lock, _, err := cli.LoadLock(s.paths.LockFile())
		if err == nil {
			st := cli.NewStrategies(s.cfg.LinkStrategy, s.paths)
			for name, e := range lock.Bundles {
				shape := lockShape(e)
				if err := st.For(shape).Release(s.paths.Target(shape, name)); err != nil {
					fmt.Printf("  跳过 %s: %v\n", name, err)
					continue
				}
				fmt.Printf("  已移除 %s\n", name)
			}
		}
		os.Remove(s.paths.LockFile())
		os.RemoveAll(s.paths.Store)
		if err := cli.UninstallHooks(s.paths.ClaudeHome); err == nil {
			fmt.Println("  已移除执行审计 hook")
		}
	}
	os.Remove(s.paths.CredentialsFile())
	fmt.Println("已登出。")
	return nil
}

func orNone(s string) string {
	if s == "" {
		return "（未设置）"
	}
	return s
}

func ifErrMsg(err error, ok string) string {
	if err != nil {
		return err.Error()
	}
	return ok
}

func ifErr(err error, ok string) string {
	if err != nil {
		return "未登录"
	}
	return ok
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

// lockShape 取 lock 条目记录的落盘形态。
//
// 旧版 lock 没有这个字段，回退到按 kind 推。正常情况下不会走到——
// lockVersion 变更会让旧 lock 直接重建——但 Release 是删文件的路径，
// 在这里多一个兜底比在这里相信「不会发生」划算。
func lockShape(e core.LockEntry) core.Shape {
	if e.Shape != "" {
		return e.Shape
	}
	return e.Kind.Shape()
}
