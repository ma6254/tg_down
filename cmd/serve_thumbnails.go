package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ma6254/tg_down/internal/server"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

const (
	// thumbnailTTL 让 token 在预览后的一段时间内仍可下载；过期后前端退回类型图标。
	thumbnailTTL = 30 * time.Minute
	// thumbnailTimeout 限制单张缩略图的下载时间，避免请求长时间挂起。
	thumbnailTimeout = 30 * time.Second
)

// thumbnailEntry 缓存一次预览里某张缩略图的定位信息与下载结果。
type thumbnailEntry struct {
	session mtproto.Session
	thumb   mtproto.Thumb
	expires time.Time
	data    []byte
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

// thumbnailFetchers 按 session 缓存常驻连接，列表里的封面共用同一条连接。
var (
	thumbnailFetchersMu sync.Mutex
	thumbnailFetchers   = make(map[string]*mtproto.ThumbnailFetcher)
)

// add 登记一张缩略图并返回不可猜测的 token，顺便清理过期项。
func (s *thumbnailStore) add(session mtproto.Session, thumb mtproto.Thumb) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for token, entry := range s.items {
		if now.After(entry.expires) {
			delete(s.items, token)
		}
	}

	token := randomToken()
	s.items[token] = &thumbnailEntry{
		session: session,
		thumb:   thumb,
		expires: now.Add(thumbnailTTL),
	}
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

// randomToken 生成 128 位随机 token。
func randomToken() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand 基本不会失败；退化到纳秒时间戳，至少保证唯一。
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// getMediaThumbnail 按 token 下载缩略图，下载成功后缓存字节，后续直接命中。
func getMediaThumbnail(ctx context.Context, token string) (server.MediaThumbnail, error) {
	entry, ok := thumbnails.lookup(token)
	if !ok {
		return server.MediaThumbnail{}, server.ErrThumbnailNotFound
	}

	thumbnailDownloadMu.Lock()
	defer thumbnailDownloadMu.Unlock()

	// 等锁期间可能已有另一个请求把图下好。
	entry, ok = thumbnails.lookup(token)
	if !ok {
		return server.MediaThumbnail{}, server.ErrThumbnailNotFound
	}
	if len(entry.data) > 0 {
		return server.MediaThumbnail{Data: entry.data, ContentType: "image/jpeg"}, nil
	}

	callCtx, cancel := context.WithTimeout(ctx, thumbnailTimeout)
	defer cancel()

	fetcher, err := thumbnailFetcherFor(callCtx, entry.session)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return server.MediaThumbnail{}, err
		}
		return server.MediaThumbnail{}, fmt.Errorf("连接 Telegram: %w", err)
	}

	data, err := fetcher.Download(callCtx, entry.thumb)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return server.MediaThumbnail{}, err
		}
		return server.MediaThumbnail{}, fmt.Errorf("下载缩略图: %w", err)
	}

	entry.data = data
	return server.MediaThumbnail{Data: data, ContentType: "image/jpeg"}, nil
}

// thumbnailFetcherFor 返回该 session 的常驻下载器，连接已退出时重建。
func thumbnailFetcherFor(ctx context.Context, session mtproto.Session) (*mtproto.ThumbnailFetcher, error) {
	key := session.SessionPath + "|" + session.Proxy

	thumbnailFetchersMu.Lock()
	defer thumbnailFetchersMu.Unlock()

	if fetcher, ok := thumbnailFetchers[key]; ok && fetcher.Alive() {
		return fetcher, nil
	}

	fetcher, err := mtproto.StartThumbnailFetcher(ctx, session)
	if err != nil {
		return nil, err
	}
	thumbnailFetchers[key] = fetcher
	return fetcher, nil
}
