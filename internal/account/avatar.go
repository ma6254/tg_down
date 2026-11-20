package account

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/ma6254/tg_down/internal/avatar"
	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
	"github.com/ma6254/tg_down/internal/utils"
)

// ErrNoAvatar 表示账号没有可用头像（未上传、被隐藏，或对端删除了头像）。
var ErrNoAvatar = errors.New("账号没有头像")

// Avatar 返回账号头像的 JPEG 字节。
//
// 读顺序：新鲜缓存 → 联网取新头像（成功则写盘）→ 失败时退回旧缓存。
// 头像按 Telegram user.id 归档（换手机号也不变）；旧版本按本地账号 ID
// 存的文件会在首次访问时自动迁移。
func (s *Service) Avatar(ctx context.Context, id int64) ([]byte, error) {
	row, err := s.GetByID(id)
	if err != nil {
		return nil, err
	}

	telegramID := row.TelegramIDValue()
	var (
		path     string
		cached   []byte
		hasCache bool
	)
	if telegramID > 0 {
		path, err = avatar.Path(telegramID)
		if err != nil {
			return nil, err
		}
		cached, hasCache = avatar.Load(row.ID, telegramID, path)
		if hasCache && avatar.Fresh(path) {
			return cached, nil
		}
	}

	// 非 mtproto 账号不联网，只能用已有缓存。
	if row.Kind != database.AccountKindMTProto {
		if hasCache {
			return cached, nil
		}
		return nil, ErrNoAvatar
	}
	if !utils.FileExists(row.SessionPath) {
		if hasCache {
			return cached, nil
		}
		return nil, fmt.Errorf("%w: 账号 #%d 的 session 文件不存在", ErrNoSession, row.ID)
	}

	proxy, err := config.NewStore(s.db).Proxy()
	if err != nil {
		return nil, fmt.Errorf("读取代理配置: %w", err)
	}
	app, err := s.AppOrDefault()
	if err != nil {
		return nil, err
	}
	session := mtproto.Session{App: app, SessionPath: row.SessionPath, Proxy: proxy}

	callCtx, cancel := context.WithTimeout(ctx, utils.DefaultNetworkTimeout)
	defer cancel()

	thumb, userID, ok, err := session.AvatarThumb(callCtx)
	if err != nil {
		if hasCache {
			return cached, nil
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrLiveFailed, err)
	}

	// 旧账号第一次取头像时才知道 user.id：顺手绑定并切到 user_id 命名的缓存。
	if userID > 0 && userID != telegramID {
		_ = s.BindTelegramID(row.ID, userID)
		newPath, pathErr := avatar.Path(userID)
		if pathErr != nil {
			return nil, pathErr
		}
		if !hasCache {
			cached, hasCache = avatar.Load(row.ID, userID, newPath)
		}
		path = newPath
		if hasCache && avatar.Fresh(path) {
			return cached, nil
		}
	}

	if !ok {
		// 头像被移除时清掉旧文件，前端回退到名称首字母。
		if path != "" {
			_ = os.Remove(path)
		}
		return nil, ErrNoAvatar
	}
	if path == "" {
		return nil, ErrNoAvatar
	}

	fetcher, err := mtproto.SharedThumbnailFetcher(callCtx, session)
	if err != nil {
		if hasCache {
			return cached, nil
		}
		return nil, fmt.Errorf("%w: %v", ErrLiveFailed, err)
	}
	data, err := fetcher.Download(callCtx, thumb)
	if err != nil {
		if hasCache {
			return cached, nil
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrLiveFailed, err)
	}

	// 写盘失败只影响下次是否重新联网，本次仍返回图片。
	_ = avatar.Save(path, data)
	return data, nil
}
