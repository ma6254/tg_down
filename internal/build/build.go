// Package build 保存编译期注入的版本信息。
//
// 发布时由构建脚本通过 -ldflags 注入，例如：
//
//	-X github.com/ma6254/tg_down/internal/build.Version=0.1.0
package build

import "runtime"

var (
	// Version 是语义化版本号，默认 dev。
	Version = "dev"
	// Commit 是构建对应的 git 提交，默认 none。
	Commit = "none"
	// BuildTime 是构建时间（RFC3339），默认 unknown。
	BuildTime = "unknown"
)

// GoVersion 返回编译当前二进制的 Go 版本。
func GoVersion() string { return runtime.Version() }
