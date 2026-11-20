// Package avatar 把账号与 peer 的头像字节缓存到 <数据目录>/avatar/。
//
// 文件名按身份命名：账号头像用 Telegram user.id（换手机号/用户名不变），
// peer 头像用 kind-id-photoID（头像更新后自然落到新文件，无需失效逻辑）。
// 本包只做磁盘缓存，不认识 HTTP，也不负责联网下载。
package avatar

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ma6254/tg_down/internal/appdir"
	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
	"github.com/ma6254/tg_down/internal/utils"
)

// TTL 是头像文件的复用时长；超过后重新联网取一次。
// 取不到新头像时调用方仍会回退到旧文件，不影响展示。
const TTL = 24 * time.Hour

// Path 返回账号头像的缓存路径，id 是 Telegram user.id。
func Path(telegramID int64) (string, error) {
	dir, err := avatarDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("%d.jpg", telegramID)), nil
}

// PeerPath 返回群聊、频道与其他用户头像的缓存路径。
//
// 文件名带 photoID：头像更新后自然落到新文件，不需要额外的失效逻辑。
func PeerPath(peer mtproto.Peer, photoID int64) (string, error) {
	dir, err := avatarDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("peer-%s-%d-%d.jpg", peer.Kind, peer.ID, photoID)), nil
}

// avatarDir 返回头像目录，不保证已存在。
func avatarDir() (string, error) {
	dir, err := appdir.DataDir()
	if err != nil {
		return "", fmt.Errorf("解析数据目录: %w", err)
	}
	return filepath.Join(dir, "avatar"), nil
}

// Fresh 判断缓存是否还在 TTL 内。
func Fresh(path string) bool {
	info, err := os.Stat(path)
	return err == nil && time.Since(info.ModTime()) < TTL
}

// Load 读取按 user_id 命名的缓存。
//
// 文件不存在时把旧版本按本地账号 ID 命名的文件迁移过来，
// 保证升级后不重新联网；迁移失败也不影响本次返回。
func Load(localID, telegramID int64, path string) ([]byte, bool) {
	if data, ok := utils.ReadFileBytes(path); ok {
		return data, true
	}
	legacy, err := Path(localID)
	if err != nil || legacy == path {
		return nil, false
	}
	data, ok := utils.ReadFileBytes(legacy)
	if !ok {
		return nil, false
	}
	if err := os.Rename(legacy, path); err != nil {
		// 跨盘/权限等场景退回复制，失败也不影响本次返回。
		if err := Save(path, data); err != nil {
			return data, true
		}
		_ = os.Remove(legacy)
	}
	return data, true
}

// Save 原子写头像文件，避免并发读取时读到半截图片。
func Save(path string, data []byte) error {
	return utils.WriteFileAtomic(path, data)
}

// Remove 删除账号的头像文件（user_id 命名与旧版本地 ID 命名两种）。
func Remove(acct database.Account) {
	if id := acct.TelegramIDValue(); id > 0 {
		if path, err := Path(id); err == nil {
			_ = os.Remove(path)
		}
	}
	if path, err := Path(acct.ID); err == nil {
		_ = os.Remove(path)
	}
}
