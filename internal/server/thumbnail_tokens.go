package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
	"github.com/ma6254/tg_down/internal/thumbcache"
	"github.com/ma6254/tg_down/internal/utils"
)

const (
	// thumbnailTTL 让 token 在预览后的一段时间内仍可下载；过期后前端退回类型图标。
	thumbnailTTL = 30 * time.Minute
	// ThumbnailTimeout 限制单张缩略图的下载时间，避免请求长时间挂起。
	// 任务卡片的封面与预览页的封面共用这个预算。
	ThumbnailTimeout = 30 * time.Second
)

// peerThumb 描述一张会话/用户头像（走 inputPeerPhotoFileLocation）。
type peerThumb struct {
	peer    mtproto.Peer
	photoID int64
}

// thumbnailEntry 缓存一次预览里某张缩略图的定位信息与下载结果。
type thumbnailEntry struct {
	session mtproto.Session
	thumb   mtproto.Thumb // 媒体缩略图：磁盘缓存键由媒体身份决定
	peer    *peerThumb    // 会话/用户头像：走 peer photo 下载
	// cachePath 是头像字节的磁盘缓存路径，由登记方给出（与账号头像同在 avatar/ 下）。
	cachePath string
	expires   time.Time
	data      []byte
}

// thumbnailStore 是进程内的缩略图缓存。
type thumbnailStore struct {
	mu    sync.Mutex
	items map[string]*thumbnailEntry
}

var thumbnails = &thumbnailStore{items: make(map[string]*thumbnailEntry)}

// thumbnailDownloadMu 让缩略图串行下载：共用一个常驻客户端，
// 避免多张图同时向 Telegram 发请求。
var thumbnailDownloadMu sync.Mutex

// RegisterThumbnail 登记一张媒体缩略图，返回 token。
//
// token 拼进 /api/v1/media/thumbnail/{token} 即可下载；媒体身份相同的多次
// 登记各自有 token，但共享同一份磁盘缓存（见 thumbcache.Key）。
func RegisterThumbnail(session mtproto.Session, thumb mtproto.Thumb) string {
	return thumbnails.register(session, &thumbnailEntry{thumb: thumb})
}

// RegisterPeerPhoto 登记一张会话/用户头像，返回与缩略图共用的 token。
//
// 头像字节缓存到 cachePath；调用方按 peer 与 photoID 生成路径，
// 与账号头像放在同一个 avatar/ 目录下。
func RegisterPeerPhoto(session mtproto.Session, peer mtproto.Peer, photoID int64, cachePath string) string {
	return thumbnails.register(session, &thumbnailEntry{
		peer:      &peerThumb{peer: peer, photoID: photoID},
		cachePath: cachePath,
	})
}

func (s *thumbnailStore) register(session mtproto.Session, entry *thumbnailEntry) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for token, item := range s.items {
		if now.After(item.expires) {
			delete(s.items, token)
		}
	}

	token := utils.RandomToken()
	entry.session = session
	entry.expires = now.Add(thumbnailTTL)
	s.items[token] = entry
	return token
}

// lookup 读取未过期的条目。
func (s *thumbnailStore) lookup(token string) (*thumbnailEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.items[token]
	if !ok {
		return nil, false
	}
	if time.Now().After(entry.expires) {
		delete(s.items, token)
		return nil, false
	}
	return entry, true
}

// fetchMediaThumbnail 按 token 下载缩略图，下载成功后缓存字节，后续直接命中。
func fetchMediaThumbnail(ctx context.Context, token string) (MediaThumbnail, error) {
	entry, ok := thumbnails.lookup(token)
	if !ok {
		return MediaThumbnail{}, ErrThumbnailNotFound
	}

	thumbnailDownloadMu.Lock()
	defer thumbnailDownloadMu.Unlock()

	// 等锁期间可能已有另一个请求把图下好。
	entry, ok = thumbnails.lookup(token)
	if !ok {
		return MediaThumbnail{}, ErrThumbnailNotFound
	}
	if len(entry.data) > 0 {
		return MediaThumbnail{Data: entry.data, ContentType: "image/jpeg"}, nil
	}

	// 先查磁盘缓存：媒体缩略图在 thumbnail/，群/频道/用户头像在 avatar/。
	// 键由媒体或 peer 身份决定，重启或换 token 后仍能命中。
	var cacheKey string
	if entry.peer != nil {
		if data, ok := utils.ReadFileBytes(entry.cachePath); ok {
			entry.data = data
			return MediaThumbnail{Data: data, ContentType: "image/jpeg"}, nil
		}
	} else {
		cacheKey = thumbcache.Key(entry.thumb)
		if data, ok := thumbcache.Get(cacheKey); ok {
			entry.data = data
			return MediaThumbnail{Data: data, ContentType: "image/jpeg"}, nil
		}
	}

	callCtx, cancel := context.WithTimeout(ctx, ThumbnailTimeout)
	defer cancel()

	fetcher, err := mtproto.SharedThumbnailFetcher(callCtx, entry.session)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return MediaThumbnail{}, err
		}
		return MediaThumbnail{}, fmt.Errorf("连接 Telegram: %w", err)
	}

	var data []byte
	if entry.peer != nil {
		data, err = fetcher.DownloadPeerPhoto(callCtx, entry.peer.peer, entry.peer.photoID)
	} else {
		data, err = fetcher.Download(callCtx, entry.thumb)
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return MediaThumbnail{}, err
		}
		return MediaThumbnail{}, fmt.Errorf("下载缩略图: %w", err)
	}

	// 写盘失败不影响本次返回，下次请求会重试。
	if entry.peer != nil {
		_ = utils.WriteFileAtomic(entry.cachePath, data)
	} else {
		_ = thumbcache.Put(cacheKey, data)
	}
	entry.data = data
	return MediaThumbnail{Data: data, ContentType: "image/jpeg"}, nil
}

// NewMediaThumbnail 把头像/缩略图字节包装成接口结构。
func NewMediaThumbnail(data []byte) MediaThumbnail {
	return MediaThumbnail{Data: data, ContentType: "image/jpeg"}
}
