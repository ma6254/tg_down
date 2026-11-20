package server

import (
	"context"
	"errors"
	"net/http"
)

// ErrThumbnailNotFound 表示缩略图 token 不存在或已过期。
var ErrThumbnailNotFound = errors.New("缩略图不存在或已过期")

// MediaThumbnail 是一张缩略图的二进制内容。
type MediaThumbnail struct {
	Data        []byte
	ContentType string
}

// handleMediaThumbnail 处理 GET /api/v1/media/thumbnail/{token}：
// 按解析接口返回的 token 下载媒体缩略图，浏览器可长期缓存。
func (s *Server) handleMediaThumbnail(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" || len(token) > 64 {
		writeError(w, http.StatusBadRequest, "bad_request", "缩略图 token 不合法")
		return
	}

	thumbnail, err := fetchMediaThumbnail(r.Context(), token)
	if err != nil {
		writeThumbnailError(w, err)
		return
	}

	contentType := thumbnail.ContentType
	if contentType == "" {
		contentType = "image/jpeg"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(thumbnail.Data)
}

// handleDownloadThumbnail 处理 GET /api/v1/downloads/{id}/thumbnail：
// 任务卡片的封面，首次访问按需解析消息并下载，之后走数据库缓存。
func (s *Server) handleDownloadThumbnail(w http.ResponseWriter, r *http.Request) {
	if s.cfg.GetDownloadThumbnail == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "缩略图功能未接入")
		return
	}

	id, ok := parseDownloadID(w, r)
	if !ok {
		return
	}

	thumbnail, err := s.cfg.GetDownloadThumbnail(r.Context(), id)
	if err != nil {
		writeThumbnailError(w, err)
		return
	}

	contentType := thumbnail.ContentType
	if contentType == "" {
		contentType = "image/jpeg"
	}
	w.Header().Set("Content-Type", contentType)
	// 字节缓存到数据库后内容不再变化，可让浏览器缓存久一点。
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(thumbnail.Data)
}

// writeThumbnailError 把缩略图相关错误映射成 HTTP 响应。
//
// 两个缩略图端点共用一套映射：token 失效/任务不存在 404、超时 504、
// 账号状态问题 409，其余（网络、下载）统一 502。
func writeThumbnailError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrDownloadNotFound), errors.Is(err, ErrThumbnailNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "thumbnail_timeout", "缩略图下载超时，请稍后重试")
	case errors.Is(err, ErrNoCurrentAccount), errors.Is(err, ErrAccountNoSession):
		writeError(w, http.StatusConflict, "conflict", err.Error())
	default:
		writeError(w, http.StatusBadGateway, "thumbnail_failed", "缩略图下载失败: "+err.Error())
	}
}
