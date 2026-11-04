// Package web 把构建好的前端嵌进二进制
//
// dist 目录由 frontend/ 下的 `npm run build` 生成，是可选的。未构建时 Dist 返回 nil，
// 服务端改为返回说明如何构建的页面，没有 Node.js 的全新克隆里 `go build` 仍然可用，
// 命令行和 CI 都依赖这一点
package web

import (
	"embed"
	"io/fs"
)

// 仪表盘的构建挂在 `go generate` 后面
//
// `go:embed` 在编译时给文件拍快照，前端必须先于 Go 构建完成，顺序搞错会静默嵌进
// 一个空目录。下面两条指令把顺序交给工具链强制执行，而不是写在脚本或 README 里
//
//	go generate ./...   # 构建仪表盘
//	go build ./backend/cmd/fizzra
//
// 用 `go generate` 而不是 Makefile，它本身就是 Go 工具链的一部分，不用额外安装，
// Windows、macOS、Linux 上是同一条命令。npm 会自己判断前端是否已是最新，
// `go generate` 则总是执行，发布构建里这是更安全的选择
//
//go:generate npm --prefix ../../../frontend ci
//go:generate npm --prefix ../../../frontend run build

// all: 前缀不能省，Vite 会在以 "_" 或 "." 开头的目录下产出资源，
// 没有这个前缀这些文件会被漏掉
//
//go:embed all:dist
var embedded embed.FS

// Dist 返回前端文件系统，仪表盘未构建时为 nil
func Dist() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return nil
	}
	// 没有 index.html 的构建产物不是可用的仪表盘
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}

// Available 报告二进制里是否嵌入了构建好的仪表盘
func Available() bool {
	return Dist() != nil
}
