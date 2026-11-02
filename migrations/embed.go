package migrations

import "embed"

// FS 包含所有 SQL 迁移文件，编译时嵌入二进制。
// 文件按前缀编号排序执行，例如 0001_init.sql。
//
//go:embed *.sql
var FS embed.FS
