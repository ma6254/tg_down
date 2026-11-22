//go:build !webdist

package webembed

import "io/fs"

// Dist 返回内嵌的前端静态资源；未使用 webdist 构建标签时没有产物。
//
// 由默认构建（build.py）生成 webembed/dist.zip 并给 go build 加 -tags webdist。
func Dist() (fs.FS, bool) {
	return nil, false
}
