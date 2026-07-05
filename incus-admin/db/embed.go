// Package db 通过 go:embed 把 SQL 迁移文件打进二进制，供 migrate 子命令
// （goose runner）在部署产物内自包含执行，避免在目标机上还要额外拷贝
// db/migrations 目录。测试脚手架（internal/testhelper）仍直接读磁盘目录，
// 二者互不影响。
package db

import "embed"

// MigrationsFS 内嵌 db/migrations 下的全部 .sql 迁移。goose 通过
// goose.SetBaseFS(db.MigrationsFS) + dir="migrations" 读取。
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS
