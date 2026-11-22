// Package webembed 提供给 serve 内嵌的前端构建产物。
//
// 产物由 `python build.py` 生成 webembed/dist.zip，构建时自动加
// `-tags webdist`；普通 `go build` 不含前端，serve 会回退到占位页。
package webembed
