package watch

import _ "embed"

// 看板页面编进二进制：`ith5 watch` 必须在任何机器上单文件可用，
// 不能依赖运行时去某个目录找静态资源。
//
//go:embed dashboard.html
var dashboardHTML []byte
