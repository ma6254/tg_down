package mtproto

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

// DownloadThumbnail 下载媒体缩略图（JPEG 字节）。
//
// 缩略图很小，单次下载即可；调用方负责缓存与限制并发。
func (s Session) DownloadThumbnail(ctx context.Context, thumb Thumb) ([]byte, error) {
	location, err := thumb.location()
	if err != nil {
		return nil, err
	}

	opts := s.options()
	if opts.APIID <= 0 || opts.APIHash == "" {
		return nil, errors.New("缺少 api 凭据")
	}
	if opts.SessionPath == "" {
		return nil, errors.New("缺少 session 路径")
	}
	if _, err := os.Stat(opts.SessionPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("未找到 session 文件 %s，请先执行 account login", opts.SessionPath)
		}
		return nil, fmt.Errorf("读取 session 文件: %w", err)
	}

	client, err := newClient(opts)
	if err != nil {
		return nil, err
	}

	var data []byte
	err = client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return friendlyError(fmt.Errorf("查询登录状态: %w", err))
		}
		if !status.Authorized {
			return errors.New("session 已失效，请重新执行 account login")
		}

		var buf bytes.Buffer
		if _, err := client.Download(location).Stream(ctx, &buf); err != nil {
			return fmt.Errorf("下载缩略图: %w", friendlyError(err))
		}
		data = buf.Bytes()
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, errors.New("缩略图为空")
	}
	return data, nil
}

// location 把缩略图描述转换成 Telegram 的文件位置。
func (t Thumb) location() (tg.InputFileLocationClass, error) {
	switch t.Kind {
	case ThumbKindDocument:
		return &tg.InputDocumentFileLocation{
			ID:            t.FileID,
			AccessHash:    t.AccessHash,
			FileReference: t.FileReference,
			ThumbSize:     t.SizeType,
		}, nil
	case ThumbKindPhoto:
		return &tg.InputPhotoFileLocation{
			ID:            t.FileID,
			AccessHash:    t.AccessHash,
			FileReference: t.FileReference,
			ThumbSize:     t.SizeType,
		}, nil
	}
	return nil, fmt.Errorf("未知缩略图类型 %q", t.Kind)
}

// ThumbnailFetcher 维持一条常驻连接，连续下载缩略图。
//
// 缩略图很小，但每张都新建客户端需要重新连接与鉴权（约数秒），
// 列表里几十张图就会慢到不可用；这里复用同一个客户端，串行下载。
type ThumbnailFetcher struct {
	client *telegram.Client

	ready     chan struct{}
	readyOnce sync.Once
	readyErr  error
	done      chan struct{}

	cancel context.CancelFunc
	mu     sync.Mutex
}

// StartThumbnailFetcher 建立连接并等待授权状态确认，供后续串行下载使用。
func StartThumbnailFetcher(ctx context.Context, s Session) (*ThumbnailFetcher, error) {
	opts := s.options()
	if opts.APIID <= 0 || opts.APIHash == "" {
		return nil, errors.New("缺少 api 凭据")
	}
	if opts.SessionPath == "" {
		return nil, errors.New("缺少 session 路径")
	}
	if _, err := os.Stat(opts.SessionPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("未找到 session 文件 %s，请先执行 account login", opts.SessionPath)
		}
		return nil, fmt.Errorf("读取 session 文件: %w", err)
	}

	client, err := newClient(opts)
	if err != nil {
		return nil, err
	}

	runCtx, cancel := context.WithCancel(context.Background())
	fetcher := &ThumbnailFetcher{
		client: client,
		ready:  make(chan struct{}),
		done:   make(chan struct{}),
		cancel: cancel,
	}

	go func() {
		defer close(fetcher.done)
		err := client.Run(runCtx, func(ctx context.Context) error {
			status, err := client.Auth().Status(ctx)
			if err != nil {
				return friendlyError(fmt.Errorf("查询登录状态: %w", err))
			}
			if !status.Authorized {
				return errors.New("session 已失效，请重新执行 account login")
			}
			fetcher.signalReady(nil)
			<-ctx.Done()
			return nil
		})
		if err != nil {
			fetcher.signalReady(err)
		}
	}()

	select {
	case <-fetcher.ready:
		if fetcher.readyErr != nil {
			cancel()
			return nil, fetcher.readyErr
		}
		return fetcher, nil
	case <-ctx.Done():
		cancel()
		return nil, ctx.Err()
	case <-time.After(30 * time.Second):
		cancel()
		return nil, errors.New("连接 Telegram 超时")
	}
}

// Alive 表示客户端仍在运行。
func (f *ThumbnailFetcher) Alive() bool {
	select {
	case <-f.done:
		return false
	default:
		return true
	}
}

// Close 关闭常驻连接。
func (f *ThumbnailFetcher) Close() {
	if f.cancel != nil {
		f.cancel()
	}
}

// Download 串行下载一张缩略图。
func (f *ThumbnailFetcher) Download(ctx context.Context, thumb Thumb) ([]byte, error) {
	location, err := thumb.location()
	if err != nil {
		return nil, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	var buf bytes.Buffer
	if _, err := f.client.Download(location).Stream(ctx, &buf); err != nil {
		return nil, fmt.Errorf("下载缩略图: %w", friendlyError(err))
	}
	if buf.Len() == 0 {
		return nil, errors.New("缩略图为空")
	}
	return buf.Bytes(), nil
}

// signalReady 只通知一次启动结果；Run 退出时也可能再调用，用 Once 兜底。
func (f *ThumbnailFetcher) signalReady(err error) {
	f.readyOnce.Do(func() {
		f.readyErr = err
		close(f.ready)
	})
}
