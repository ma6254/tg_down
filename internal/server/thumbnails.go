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
	if s.cfg.GetMediaThumbnail == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "缩略图功能未接入")
		return
	}

	token := r.PathValue("token")
	if token == "" || len(token) > 64 {
		writeError(w, http.StatusBadRequest, "bad_request", "缩略图 token 不合法")
		return
	}

	thumbnail, err := s.cfg.GetMediaThumbnail(r.Context(), token)
	if err != nil {
		switch {
		case errors.Is(err, ErrThumbnailNotFound):
			writeError(w, http.StatusNotFound, "not_found", err.Error())
		case errors.Is(err, context.DeadlineExceeded):
			writeError(w, http.StatusGatewayTimeout, "thumbnail_timeout", "缩略图下载超时，请稍后重试")
		default:
			writeError(w, http.StatusBadGateway, "thumbnail_failed", "缩略图下载失败: "+err.Error())
		}
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
