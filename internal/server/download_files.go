package server

import (
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// DownloadFile 是已下载文件的读取句柄与元信息。
//
// Content 必须从偏移 0 开始可 Seek（http.ServeContent 用它计算大小并处理 Range）；
// handler 结束后会 Close。
type DownloadFile struct {
	Content io.ReadSeekCloser
	Name    string
	MIME    string
	Size    int64
	ModTime time.Time
}

// handleDownloadFile 处理 GET/HEAD /api/v1/downloads/{id}/file。
//
// 只读取已完成任务的文件；Range、If-Modified-Since 与 HEAD 由
// http.ServeContent 统一处理，视频/音频可以直接拖动进度条。
func (s *Server) handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	if s.cfg.GetDownloadFile == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "下载功能未接入")
		return
	}

	id, ok := parseDownloadID(w, r)
	if !ok {
		return
	}

	file, err := s.cfg.GetDownloadFile(r.Context(), id)
	if err != nil {
		writeDownloadError(w, err)
		return
	}
	if file.Content == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "文件读取未返回内容")
		return
	}
	defer func() { _ = file.Content.Close() }()

	contentType := strings.TrimSpace(file.MIME)
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(file.Name))
	}
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}

	disposition := "attachment"
	if isInlineFile(contentType) {
		disposition = "inline"
	}
	if header := mime.FormatMediaType(disposition, map[string]string{"filename": file.Name}); header != "" {
		w.Header().Set("Content-Disposition", header)
	}
	// 文件可被重试覆盖或任务删除，不交给浏览器长期缓存；Range 仍在单次会话内生效。
	w.Header().Set("Cache-Control", "private, no-store")

	http.ServeContent(w, r, file.Name, file.ModTime, file.Content)
}

// isInlineFile 决定浏览器直接预览还是下载附件：图片、音视频与 PDF 用 inline。
func isInlineFile(contentType string) bool {
	switch {
	case strings.HasPrefix(contentType, "image/"),
		strings.HasPrefix(contentType, "video/"),
		strings.HasPrefix(contentType, "audio/"),
		contentType == "application/pdf":
		return true
	default:
		return false
	}
}
