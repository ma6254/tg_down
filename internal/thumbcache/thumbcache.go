// Package thumbcache 把缩略图字节缓存到用户数据目录下的 thumbnail/ 里。
//
// 缓存键由媒体身份（photo/document + 文件 ID + 尺寸类型）决定，
// 与消息链接、token 无关：同一条媒体在预览页和任务卡片之间共享同一份文件，
// 进程重启或 token 过期后仍然可用。
package thumbcache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ma6254/tg_down/internal/appdir"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// Dir 返回缩略图缓存目录（<数据目录>/thumbnail），不保证已存在。
func Dir() (string, error) {
	base, err := appdir.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "thumbnail"), nil
}

// Key 按媒体身份生成缓存键；尺寸类型不同视为不同文件。
func Key(thumb mtproto.Thumb) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%s", thumb.Kind, thumb.FileID, thumb.SizeType)))
	return hex.EncodeToString(sum[:])
}

// ValidKey 判断缓存键是否是 Key 生成的 64 位十六进制串。
//
// 键会直接参与文件路径拼接，必须先校验，避免调用方（例如历史缓存里的
// 前端回传字段）用 "../" 之类的内容逃出缓存目录。
func ValidKey(key string) bool {
	if len(key) != sha256.Size*2 {
		return false
	}
	for _, r := range key {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// Cached 判断某个键的缩略图是否已落盘（只看文件是否非空）。
func Cached(key string) bool {
	if !ValidKey(key) {
		return false
	}
	path, err := Path(key)
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
}

// Path 返回某个键对应的文件路径（两级分片，避免单目录文件过多）。
func Path(key string) (string, error) {
	if !ValidKey(key) {
		return "", fmt.Errorf("缩略图缓存键 %q 不合法", key)
	}
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, key[:2], key+".jpg"), nil
}

// Get 读取缓存；文件不存在、为空或读取失败都按未命中处理。
func Get(key string) ([]byte, bool) {
	path, err := Path(key)
	if err != nil {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}

// Put 原子写入缓存：先写临时文件再改名，避免读到半截图片。
func Put(key string, data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("缩略图内容为空")
	}
	path, err := Path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建缩略图目录: %w", err)
	}

	temp, err := os.CreateTemp(filepath.Dir(path), ".thumb-*.tmp")
	if err != nil {
		return fmt.Errorf("创建缩略图临时文件: %w", err)
	}
	tempPath := temp.Name()
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		_ = os.Remove(tempPath)
		return fmt.Errorf("写入缩略图缓存: %w", err)
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("关闭缩略图临时文件: %w", err)
	}
	if err := os.Chmod(tempPath, 0o644); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("设置缩略图权限: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("提交缩略图缓存: %w", err)
	}
	return nil
}
