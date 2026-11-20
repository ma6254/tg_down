package serve

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gorm.io/gorm"

	accountsvc "github.com/ma6254/tg_down/internal/account"
	"github.com/ma6254/tg_down/internal/appdir"
	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/server"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
	"github.com/ma6254/tg_down/internal/utils"
)

// avatarCacheTTL 是头像文件的复用时长；超过后重新联网取一次。
// 取不到新头像时仍会回退到旧文件，不影响展示。
const avatarCacheTTL = 24 * time.Hour

// getAccountAvatar 返回账号头像，缓存到 <数据目录>/avatar/<user_id>.jpg。
//
// 头像按 Telegram user.id 归档（换手机号也不变）；旧版本按本地账号 ID 存的文件
// 会在首次访问时自动迁移。读顺序：新鲜缓存 → 联网取新头像（成功则写盘）→
// 失败时退回旧缓存。
func getAccountAvatar(ctx context.Context, id int64) (server.MediaThumbnail, error) {
	if err := ctx.Err(); err != nil {
		return server.MediaThumbnail{}, err
	}

	db, err := utils.OpenDatabase()
	if err != nil {
		return server.MediaThumbnail{}, fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	acct, err := db.GetAccount(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return server.MediaThumbnail{}, fmt.Errorf("%w: 账号 ID %d 不存在", server.ErrAccountNotFound, id)
		}
		return server.MediaThumbnail{}, fmt.Errorf("查询账号 %d: %w", id, err)
	}

	telegramID := acct.TelegramIDValue()
	var (
		path     string
		cached   []byte
		hasCache bool
	)
	if telegramID > 0 {
		path, err = avatarCachePath(telegramID)
		if err != nil {
			return server.MediaThumbnail{}, err
		}
		cached, hasCache = loadAvatar(acct.ID, telegramID, path)
		if hasCache && avatarFresh(path) {
			return server.MediaThumbnail{Data: cached, ContentType: "image/jpeg"}, nil
		}
	}

	if acct.Kind != database.AccountKindMTProto {
		if hasCache {
			return server.MediaThumbnail{Data: cached, ContentType: "image/jpeg"}, nil
		}
		return server.MediaThumbnail{}, server.ErrThumbnailNotFound
	}
	if !utils.FileExists(acct.SessionPath) {
		if hasCache {
			return server.MediaThumbnail{Data: cached, ContentType: "image/jpeg"}, nil
		}
		return server.MediaThumbnail{}, fmt.Errorf("%w: 账号 #%d 的 session 文件不存在", server.ErrAccountNoSession, acct.ID)
	}

	proxy, err := config.NewStore(db).Proxy()
	if err != nil {
		return server.MediaThumbnail{}, fmt.Errorf("读取代理配置: %w", err)
	}
	app, err := accountsvc.New(db).AppOrDefault()
	if err != nil {
		return server.MediaThumbnail{}, err
	}
	session := mtproto.Session{App: app, SessionPath: acct.SessionPath, Proxy: proxy}

	callCtx, cancel := context.WithTimeout(ctx, utils.DefaultNetworkTimeout)
	defer cancel()

	thumb, userID, ok, err := session.AvatarThumb(callCtx)
	if err != nil {
		if hasCache {
			return server.MediaThumbnail{Data: cached, ContentType: "image/jpeg"}, nil
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return server.MediaThumbnail{}, err
		}
		return server.MediaThumbnail{}, fmt.Errorf("%w: %v", server.ErrAccountLiveFailed, err)
	}

	// 旧账号第一次取头像时才知道 user.id：顺手绑定并切到 user_id 命名的缓存。
	if userID > 0 && userID != telegramID {
		_ = accountsvc.New(db).BindTelegramID(acct.ID, userID)
		newPath, pathErr := avatarCachePath(userID)
		if pathErr != nil {
			return server.MediaThumbnail{}, pathErr
		}
		if !hasCache {
			cached, hasCache = loadAvatar(acct.ID, userID, newPath)
		}
		path = newPath
		if hasCache && avatarFresh(path) {
			return server.MediaThumbnail{Data: cached, ContentType: "image/jpeg"}, nil
		}
	}

	if !ok {
		// 头像被移除时清掉旧文件，前端回退到首字母。
		if path != "" {
			_ = os.Remove(path)
		}
		return server.MediaThumbnail{}, server.ErrThumbnailNotFound
	}
	if path == "" {
		return server.MediaThumbnail{}, server.ErrThumbnailNotFound
	}

	fetcher, err := server.ThumbnailFetcher(callCtx, session)
	if err != nil {
		if hasCache {
			return server.MediaThumbnail{Data: cached, ContentType: "image/jpeg"}, nil
		}
		return server.MediaThumbnail{}, fmt.Errorf("%w: %v", server.ErrAccountLiveFailed, err)
	}
	data, err := fetcher.Download(callCtx, thumb)
	if err != nil {
		if hasCache {
			return server.MediaThumbnail{Data: cached, ContentType: "image/jpeg"}, nil
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return server.MediaThumbnail{}, err
		}
		return server.MediaThumbnail{}, fmt.Errorf("%w: %v", server.ErrAccountLiveFailed, err)
	}

	// 写盘失败只影响下次是否重新联网，本次仍返回图片。
	_ = utils.WriteFileAtomic(path, data)
	return server.MediaThumbnail{Data: data, ContentType: "image/jpeg"}, nil
}

// loadAvatar 读取按 user_id 命名的缓存；不存在时把旧版本按本地账号 ID
// 命名的文件迁移过来，保证升级后不重新联网。
func loadAvatar(localID, telegramID int64, path string) ([]byte, bool) {
	if data, ok := utils.ReadFileBytes(path); ok {
		return data, true
	}
	legacy, err := avatarCachePath(localID)
	if err != nil || legacy == path {
		return nil, false
	}
	data, ok := utils.ReadFileBytes(legacy)
	if !ok {
		return nil, false
	}
	if err := os.Rename(legacy, path); err != nil {
		// 跨盘/权限等场景退回复制，失败也不影响本次返回。
		if err := utils.WriteFileAtomic(path, data); err != nil {
			return data, true
		}
		_ = os.Remove(legacy)
	}
	return data, true
}

// avatarCachePath 返回账号头像的缓存路径，id 是 Telegram user.id。
func avatarCachePath(telegramID int64) (string, error) {
	dir, err := appdir.DataDir()
	if err != nil {
		return "", fmt.Errorf("解析数据目录: %w", err)
	}
	return filepath.Join(dir, "avatar", fmt.Sprintf("%d.jpg", telegramID)), nil
}

// peerAvatarCachePath 返回群聊/频道/其他用户头像的缓存路径。
//
// 文件名带 photoID：头像更新后自然落到新文件，不需要额外的失效逻辑。
func peerAvatarCachePath(peer mtproto.Peer, photoID int64) (string, error) {
	dir, err := appdir.DataDir()
	if err != nil {
		return "", fmt.Errorf("解析数据目录: %w", err)
	}
	return filepath.Join(
		dir,
		"avatar",
		fmt.Sprintf("peer-%s-%d-%d.jpg", peer.Kind, peer.ID, photoID),
	), nil
}

// peerAvatarURL 登记一张群聊/频道/发送者头像，返回可直接放进响应的地址。
//
// 缓存路径取不到时返回空串，前端回退到名称首字母。
func peerAvatarURL(session mtproto.Session, avatar *mtproto.PeerPhoto) string {
	path, err := peerAvatarCachePath(avatar.Peer, avatar.PhotoID)
	if err != nil {
		return ""
	}
	token := server.RegisterPeerPhoto(session, avatar.Peer, avatar.PhotoID, path)
	return "/api/v1/media/thumbnail/" + token
}

// avatarFresh 判断缓存是否还在 TTL 内。
func avatarFresh(path string) bool {
	info, err := os.Stat(path)
	return err == nil && time.Since(info.ModTime()) < avatarCacheTTL
}

// removeAvatarCache 删除账号的头像文件（user_id 命名与旧版本地 ID 命名两种）。
func removeAvatarCache(acct database.Account) {
	if id := acct.TelegramIDValue(); id > 0 {
		if path, err := avatarCachePath(id); err == nil {
			_ = os.Remove(path)
		}
	}
	if path, err := avatarCachePath(acct.ID); err == nil {
		_ = os.Remove(path)
	}
}
