package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ssePingInterval 是保活间隔；中间的代理常把长时间无数据的连接掐掉。
const ssePingInterval = 15 * time.Second

// handleDownloadEvents 处理 GET /api/v1/events：以 SSE 推送下载任务状态。
//
// 事件类型：
//   - task.update  完整任务快照（新建、开始、暂停、完成、失败）
//   - task.progress {id, offset, speed, size}
//   - task.deleted {id}
//   - ping         保活
//
// 连接断开由客户端负责重连；重连后应重新拉一次任务列表对齐。
func (s *Server) handleDownloadEvents(w http.ResponseWriter, r *http.Request) {
	if s.cfg.EventHub == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "实时事件未接入")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal_error", "当前连接不支持流式响应")
		return
	}

	events := s.cfg.EventHub.Subscribe(r.Context())

	header := w.Header()
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	// 关闭 Nginx 等反向代理的缓冲，否则事件会被攒着不发。
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	if err := writeSSE(w, "ping", map[string]any{}); err != nil {
		return
	}
	flusher.Flush()

	ticker := time.NewTicker(ssePingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if err := writeSSE(w, event.Type, event.Data); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if err := writeSSE(w, "ping", map[string]any{}); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// writeSSE 写一条事件帧；data 始终是单行 JSON。
func writeSSE(w io.Writer, event string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
	return err
}
