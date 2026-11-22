//go:build webdist

package webembed

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"io/fs"
)

// distZip 是默认构建（build.py）打好的前端产物（dist/ 下的文件按相对路径存放）。
//
// 用 zip 而不是 tar.gz：*zip.Reader 直接实现 fs.FS，serve 可以原样
// 交给 http.FileServer，不需要先解压到磁盘。
//
//go:embed dist.zip
var distZip []byte

// Dist 返回内嵌的前端静态资源。
func Dist() (fs.FS, bool) {
	reader, err := zip.NewReader(bytes.NewReader(distZip), int64(len(distZip)))
	if err != nil {
		return nil, false
	}
	return reader, true
}
