package server

import (
	"time"

	"github.com/ma6254/tg_down/internal/download/manager"
)

// NewDownloadTask 把任务快照转换成接口结构。
func NewDownloadTask(task manager.Task) DownloadTask {
	row := task.Row
	return DownloadTask{
		ID:           row.ID,
		Link:         row.Link,
		AccountID:    row.AccountID,
		ChatID:       row.ChatID,
		ChatTitle:    row.ChatTitle,
		MessageID:    row.MessageID,
		FileName:     row.FileName,
		FileSize:     row.FileSize,
		Kind:         row.Kind,
		MIME:         row.MIME,
		Status:       row.Status,
		Offset:       task.Offset,
		Speed:        task.Speed,
		Error:        row.Error,
		SavePath:     row.SavePath,
		Overwrite:    row.Overwrite,
		ThumbnailURL: row.ThumbnailURL,
		PreviewURL:   row.PreviewURL,
		CreatedAt:    row.CreatedAt.Unix(),
		UpdatedAt:    row.UpdatedAt.Unix(),
	}
}

// NewDownloadTasks 批量转换；空列表返回空切片而不是 nil，前端按数组遍历。
func NewDownloadTasks(tasks []manager.Task) []DownloadTask {
	out := make([]DownloadTask, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, NewDownloadTask(task))
	}
	return out
}

// NewDownloadStats 把下载队列总览转换成接口结构。
func NewDownloadStats(stats manager.Stats) DownloadStats {
	return DownloadStats{
		TotalSpeed:   stats.TotalSpeed,
		RunningTasks: stats.Running,
	}
}

// NewDownloadSpeedHistory 把领域历史速度转换成接口结构。
func NewDownloadSpeedHistory(history manager.SpeedHistory) DownloadSpeedHistory {
	points := make([]DownloadSpeedPoint, 0, len(history.Points))
	for _, point := range history.Points {
		points = append(points, DownloadSpeedPoint{
			At:           point.At.Unix(),
			TotalSpeed:   point.Speed,
			RunningTasks: point.Running,
		})
	}
	return DownloadSpeedHistory{
		From:           history.From.Unix(),
		To:             history.To.Unix(),
		Bucket:         int(history.Bucket / time.Second),
		SampleInterval: int(history.SampleInterval / time.Second),
		StatsSince:     history.StatsSince.Unix(),
		AverageSpeed:   history.AverageSpeed,
		PeakSpeed:      history.PeakSpeed,
		Points:         points,
	}
}

// NewDownloadCreateResponse 把创建结果转换成接口结构。
func NewDownloadCreateResponse(result manager.CreateResult) DownloadCreateResponse {
	resp := DownloadCreateResponse{
		Created:   result.Created,
		Restarted: result.Restarted,
		Existing:  result.Existing,
	}
	for _, skip := range result.Skipped {
		resp.Skipped = append(resp.Skipped, DownloadSkip{Link: skip.Link, Reason: skip.Reason})
	}
	for _, failure := range result.Failed {
		resp.Failed = append(resp.Failed, DownloadFailure{Link: failure.Link, Error: failure.Error})
	}
	return resp
}

// NewDownloadEvent 把领域事件转换成 SSE 事件。
//
// 事件名与前端约定一致：task.update 带整条任务，task.progress 只带增量，
// task.deleted 只带 ID；前端按 Type 决定如何处理 Data。
func NewDownloadEvent(event manager.Event) DownloadEvent {
	switch event.Type {
	case manager.EventTaskUpdate:
		return DownloadEvent{Type: event.Type, Data: NewDownloadTask(event.Task)}
	case manager.EventTaskProgress:
		return DownloadEvent{Type: event.Type, Data: map[string]any{
			"id":     event.ID,
			"offset": event.Offset,
			"speed":  event.Speed,
			"size":   event.Size,
		}}
	case manager.EventTaskDeleted:
		return DownloadEvent{Type: event.Type, Data: map[string]any{"id": event.ID}}
	}
	return DownloadEvent{Type: event.Type, Data: map[string]any{"id": event.ID}}
}
