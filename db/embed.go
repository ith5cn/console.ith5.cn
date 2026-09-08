// Package db 只负责把迁移脚本编译进二进制。
//
// 这样 ith5-server 是一个自包含的可执行文件：部署时不需要额外拷贝 SQL 文件，
// 也不需要在目标机器上装 goose CLI。
package db

import "embed"

//go:embed migrations/*.sql
var Migrations embed.FS
