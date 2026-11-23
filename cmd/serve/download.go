package serve

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ma6254/tg_down/internal/download/manager"
	"github.com/ma6254/tg_down/internal/server"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// newDownloadManager 创建下载队列，并把接口层需要的能力注入进去。
//
// 队列在 internal/download/manager；这里只负责把领域事件转成 SSE、
// 把领域结果转成接口 DTO，以及错误归类。
func newDownloadManager(rootDir string, workers int, hub *server.EventHub) *manager.Manager {
	return manager.New(manager.Config{
		RootDir: rootDir,
		Workers: workers,
		OnEvent: func(event manager.Event) {
			hub.Publish(server.NewDownloadEvent(event))
		},
		// 任务卡片封面：token 存储与可下载地址都由 internal/server 负责。
		ThumbnailURL: func(session mtproto.Session, thumb mtproto.Thumb) string {
			return "/api/v1/media/thumbnail/" + server.RegisterThumbnail(session, thumb)
		},
	})
}

// mapDownloadError 把队列的领域错误翻译成 server 的哨兵错误。
func mapDownloadError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, manager.ErrNotFound):
		return fmt.Errorf("%w: %v", server.ErrDownloadNotFound, err)
	case errors.Is(err, manager.ErrConflict):
		return fmt.Errorf("%w: %v", server.ErrDownloadConflict, err)
	case errors.Is(err, manager.ErrThumbnailNotFound):
		return fmt.Errorf("%w: %v", server.ErrThumbnailNotFound, err)
	case errors.Is(err, manager.ErrFileNotFound):
		return fmt.Errorf("%w: %v", server.ErrDownloadFileNotFound, err)
	case errors.Is(err, manager.ErrNoAccount):
		return fmt.Errorf("%w: %v", server.ErrNoCurrentAccount, err)
	case errors.Is(err, manager.ErrNoSession):
		return fmt.Errorf("%w: %v", server.ErrAccountNoSession, err)
	case errors.Is(err, manager.ErrPreviewFailed):
		return fmt.Errorf("%w: %v", server.ErrPreviewFailed, err)
	}
	return err
}

// createDownloads 是 Config.CreateDownloads 的实现：领域请求进、接口 DTO 出。
func createDownloads(mgr *manager.Manager) func(context.Context, server.DownloadCreateRequest) (server.DownloadCreateResponse, error) {
	return func(ctx context.Context, req server.DownloadCreateRequest) (server.DownloadCreateResponse, error) {
		result, err := mgr.Create(ctx, manager.CreateRequest{
			Links:          req.Links,
			Overwrite:      req.Overwrite,
			Single:         req.Single,
			AllowOverLimit: req.AllowOverLimit,
		})
		if err != nil {
			return server.DownloadCreateResponse{}, mapDownloadError(err)
		}
		return server.NewDownloadCreateResponse(result), nil
	}
}

// listDownloads 是 Config.ListDownloads 的实现。
func listDownloads(mgr *manager.Manager) func(context.Context, server.DownloadListQuery) ([]server.DownloadTask, error) {
	return func(ctx context.Context, query server.DownloadListQuery) ([]server.DownloadTask, error) {
		tasks, err := mgr.List(ctx, manager.ListQuery{Status: query.Status, Limit: query.Limit})
		if err != nil {
			return nil, mapDownloadError(err)
		}
		return server.NewDownloadTasks(tasks), nil
	}
}

// downloadStats 是 Config.GetDownloadStats 的实现：领域总览进、接口 DTO 出。
func downloadStats(mgr *manager.Manager) func(context.Context) server.DownloadStats {
	return func(context.Context) server.DownloadStats {
		return server.NewDownloadStats(mgr.Stats())
	}
}

// downloadSpeedHistory 是 Config.GetDownloadSpeedHistory 的实现。
func downloadSpeedHistory(mgr *manager.Manager) func(context.Context, server.DownloadSpeedHistoryQuery) (server.DownloadSpeedHistory, error) {
	return func(_ context.Context, query server.DownloadSpeedHistoryQuery) (server.DownloadSpeedHistory, error) {
		return server.NewDownloadSpeedHistory(mgr.SpeedHistory(manager.SpeedHistoryQuery{
			From:   query.From,
			To:     query.To,
			Bucket: query.Bucket,
		})), nil
	}
}

// resetDownloadSpeedStats 是 Config.ResetDownloadSpeedStats 的实现。
func resetDownloadSpeedStats(mgr *manager.Manager) func(context.Context) time.Time {
	return func(context.Context) time.Time {
		return mgr.ResetSpeedStats()
	}
}

// getDownload 是 Config.GetDownload 的实现。
func getDownload(mgr *manager.Manager) func(context.Context, uint) (server.DownloadTask, error) {
	return func(ctx context.Context, id uint) (server.DownloadTask, error) {
		task, err := mgr.Get(ctx, id)
		if err != nil {
			return server.DownloadTask{}, mapDownloadError(err)
		}
		return server.NewDownloadTask(task), nil
	}
}

// controlDownload 是 Config.ControlDownload 的实现。
func controlDownload(mgr *manager.Manager) func(context.Context, uint, string) (server.DownloadTask, error) {
	return func(ctx context.Context, id uint, action string) (server.DownloadTask, error) {
		task, err := mgr.Control(ctx, id, action)
		if err != nil {
			return server.DownloadTask{}, mapDownloadError(err)
		}
		return server.NewDownloadTask(task), nil
	}
}

// downloadThumbnail 是 Config.GetDownloadThumbnail 的实现。
func downloadThumbnail(mgr *manager.Manager) func(context.Context, uint) (server.MediaThumbnail, error) {
	return func(ctx context.Context, id uint) (server.MediaThumbnail, error) {
		data, err := mgr.Thumbnail(ctx, id)
		if err != nil {
			return server.MediaThumbnail{}, mapDownloadError(err)
		}
		return server.NewMediaThumbnail(data), nil
	}
}

// downloadFile 是 Config.GetDownloadFile 的实现：打开已完成任务的文件，
// 由 HTTP 层负责 Range 与关闭。
func downloadFile(mgr *manager.Manager) func(context.Context, uint) (server.DownloadFile, error) {
	return func(ctx context.Context, id uint) (server.DownloadFile, error) {
		file, info, err := mgr.OpenFile(ctx, id)
		if err != nil {
			return server.DownloadFile{}, mapDownloadError(err)
		}
		return server.DownloadFile{
			Content: file,
			Name:    info.Name,
			MIME:    info.MIME,
			Size:    info.Size,
			ModTime: info.ModTime,
		}, nil
	}
}
