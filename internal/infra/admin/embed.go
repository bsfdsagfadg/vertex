// Package admin 嵌入管理后台的静态前端资源（admin.html + 背景图）。
//
// 资源由 go:embed 编进二进制，无需随程序额外分发文件；api 层据此服务 /admin 页面。
// 前端面板（assets/admin.html）的接口契约由 internal/api/admin.go 的各 handler 兑现。
package admin

import "embed"

// Assets 持有 internal/infra/admin/assets/ 下的全部静态资源（admin.html 及图片等）。
//
//go:embed assets/*
var Assets embed.FS
