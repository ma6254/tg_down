package utils

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ErrEmptyContent 表示要写入的内容为空。
//
// 头像、缩略图一类的缓存写空文件没有意义，调用方通常也把它当成写入失败。
var ErrEmptyContent = errors.New("内容为空")

// FileExists 判断路径是否指向一个存在的普通文件。
//
// 目录不算存在；路径两端的空白会被忽略（数据库里的路径偶尔带空格）。
func FileExists(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// ReadFileBytes 读取文件内容。
//
// 文件不存在、读取失败或内容为空都按未命中处理，返回 (nil, false)：
// 缓存类调用方据此走联网重新获取，而不是区分具体错误。
func ReadFileBytes(path string) ([]byte, bool) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}

// WriteFileAtomic 原子写文件：先写同目录下的临时文件再改名，
// 避免并发读取时读到半截内容。
//
// 目标目录不存在时自动创建；内容为空返回 ErrEmptyContent。
func WriteFileAtomic(path string, data []byte) error {
	if len(data) == 0 {
		return ErrEmptyContent
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	// 任何一步失败都清掉临时文件，避免在缓存目录里堆垃圾。
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if err := os.Chmod(tempPath, 0o644); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}
