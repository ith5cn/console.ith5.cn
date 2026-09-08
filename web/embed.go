// Package web 把管理后台的构建产物编译进服务端二进制。
//
// 这是选 Vite 而非 Next.js 的直接收益：前端是纯静态产物，由 ith5-server
// 直接托管，部署拓扑里不需要第二个 Node 运行时。
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist 返回构建产物的文件系统。若尚未执行 npm run build，返回 nil。
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}
