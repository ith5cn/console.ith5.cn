// Package link 实现「让 Claude Code 的入口指向 store 中某个内容版本」
// 的各种策略（技术方案 §8.3）。
//
// 策略按 (落盘策略, 形态) 两个维度选取：
//
//	           目录形态                    文件形态
//	symlink    指向版本目录                 指向版本目录里的 AGENT.md
//	junction   指向版本目录                 —— 降级 copy，junction 只能指目录
//	copy       目录复制 + marker            FileCopy：单次 rename，靠内容比对判归属
//
// 所有策略共享同一个不变量：ITH5 只操作**一个入口**——skill 是
// skills/<name>，agent 是 agents/<name>.md——从不进入用户目录内部逐文件增删。
package link

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ith5/ith5/internal/core"
)

// Ownership 是目标入口的归属判定结果。
type Ownership = core.Ownership

const (
	OwnAbsent  = core.OwnAbsent
	OwnMine    = core.OwnMine
	OwnForeign = core.OwnForeign
)

// Marker 是 copy 策略下标记目录归属的文件名。
// link 策略下用 readlink 即可判定，但 marker 仍随内容一起存在于 store，
// 因此两种策略的内容目录完全一致，切换策略无需重新下载。
const Marker = core.MarkerFile

// MarkerData 是 marker 文件的内容。
type MarkerData struct {
	BundleID   string `json:"bundle_id"`
	BundleName string `json:"bundle_name"`
	// Kind 必须记：skill 与 command 落盘完全相同（都是 skills/<name>/），
	// 重建时无法从文件系统区分，只能靠这里记下的原始类型。
	Kind           string `json:"kind,omitempty"`
	Version        int    `json:"version"`
	Checksum       string `json:"checksum"`
	MaterializedAt string `json:"materialized_at"`
	Strategy       string `json:"strategy"`
}

// 受阻成因，随 conflict_skipped 回执上报（技术方案 §8.5.1）。
//
// 不区分这两者的话，管理员在「下发受阻」列表里只看到一个路径，
// 他会去查重名——而重名根本不存在，真正的原因是那个人改了文件。
// 这条排查会走进死胡同。
const (
	ReasonNameTaken       = "name_taken"
	ReasonLocallyModified = "locally_modified"
)

// Info 是一次归属判定的结果。
type Info struct {
	Ownership Ownership
	// StoreDir 在 OwnMine 且为 link 策略时指向 store 中的内容目录；
	// 文件形态的内容比对命中时同样填这里（命中的目录名即 checksum）。
	StoreDir string
	// Marker 仅在 OwnMine 且为 copy 策略（目录形态）时有值。
	Marker *MarkerData
	// Reason 仅在 OwnForeign 时有值，说明受阻成因。
	Reason string
}

// Strategy 是指针策略。三种实现共享同一套集成用例，最终状态必须一致。
type Strategy interface {
	ID() string
	// Materialize 让 target 指向 storeDir。target 已存在且属于我方时替换。
	Materialize(storeDir, target string, md MarkerData, sc StoreCtx) error
	// Inspect 判定 target 的归属。sc 给出 store 的位置与本 bundle 的入口
	// 文件名——文件形态靠逐版本内容比对判归属，没有入口文件名就比不了。
	Inspect(target string, sc StoreCtx) (Info, error)
	// Release 释放入口。只在 Inspect 判定为 OwnMine 时可调用。
	Release(target string, sc StoreCtx) error
}

// StoreCtx 是判定归属所需的 store 侧信息。
//
// 它取代了原来的 storeRoot 单参数：store 布局改成
// store/<kind>/<name>/<checksum> 之后，「这个 bundle 的内容在哪」
// 无法再从 target 路径反推——同名不同 kind 的两个 bundle，target
// 长得完全不一样，但从前的代码只看得到一个名字。
type StoreCtx struct {
	// Root 是 store 根目录（~/.ith5/store），用于判断链接是否指向我方。
	Root string
	// BundleDir 是本 bundle 的目录（<root>/<kind>/<name>），
	// 文件形态在它下面逐版本比对内容。
	BundleDir string
	// EntryFile 是 store 版本目录里的入口文件名（AGENT.md / HOOK.js / ...）。
	EntryFile string
}

// ErrForeign 表示目标被用户自有内容占用，必须跳过。
var ErrForeign = fmt.Errorf("目标已被用户自有内容占用")

// readMarker 读取 copy 策略的标记文件。
func readMarker(dir string) (*MarkerData, bool) {
	b, err := os.ReadFile(filepath.Join(dir, Marker))
	if err != nil {
		return nil, false
	}
	var md MarkerData
	if err := json.Unmarshal(b, &md); err != nil {
		return nil, false
	}
	return &md, true
}

func writeMarker(dir string, md MarkerData) error {
	if md.MaterializedAt == "" {
		md.MaterializedAt = time.Now().UTC().Format(time.RFC3339)
	}
	b, err := json.MarshalIndent(md, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, Marker), append(b, '\n'), 0o644)
}

// underRoot 判断 p 是否位于 root 之内（已规范化比较）。
func underRoot(p, root string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// inspectPointer 是 symlink 与 junction 共用的判定逻辑。
//
// Node 与 Go 在两个平台上都把 junction 报告为符号链接，因此这段代码
// 三平台一致。
func inspectPointer(target string, sc StoreCtx) (Info, error) {
	fi, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return Info{Ownership: OwnAbsent}, nil
	}
	if err != nil {
		return Info{}, err
	}

	if fi.Mode()&os.ModeSymlink != 0 {
		dest, err := os.Readlink(target)
		if err != nil {
			return Info{Ownership: OwnForeign}, nil
		}
		if !filepath.IsAbs(dest) {
			dest = filepath.Join(filepath.Dir(target), dest)
		}
		if underRoot(dest, sc.Root) {
			return Info{Ownership: OwnMine, StoreDir: dest}, nil
		}
		// 指向 store 之外的链接是用户自己建的，不碰
		return Info{Ownership: OwnForeign, Reason: ReasonNameTaken}, nil
	}

	// 真实目录：只有带我方 marker 的才算我们的（copy 策略留下的）
	if fi.IsDir() {
		if md, ok := readMarker(target); ok {
			return Info{Ownership: OwnMine, Marker: md}, nil
		}
	}
	return Info{Ownership: OwnForeign, Reason: ReasonNameTaken}, nil
}
