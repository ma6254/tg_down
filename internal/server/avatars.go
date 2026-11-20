package server

import (
	"context"
	"errors"
	"net/http"
)

// handleAccountAvatar 处理 GET /api/v1/accounts/{id}/avatar：
// 返回账号头像，字节由业务层缓存到磁盘；没有头像时 404。
func (s *Server) handleAccountAvatar(w http.ResponseWriter, r *http.Request) {
	if s.cfg.GetAccountAvatar == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "头像功能未接入")
		return
	}

	id, ok := accountIDFromPath(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "账号 ID 必须是正整数")
		return
	}

	avatar, err := s.cfg.GetAccountAvatar(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, ErrAccountNotFound), errors.Is(err, ErrThumbnailNotFound):
			writeError(w, http.StatusNotFound, "not_found", err.Error())
		case errors.Is(err, ErrAccountNoSession):
			writeError(w, http.StatusConflict, "conflict", err.Error())
		case errors.Is(err, context.DeadlineExceeded):
			writeError(w, http.StatusGatewayTimeout, "avatar_timeout", "头像下载超时，请稍后重试")
		case errors.Is(err, ErrAccountLiveFailed):
			writeError(w, http.StatusBadGateway, "avatar_failed", err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "读取头像失败: "+err.Error())
		}
		return
	}

	contentType := avatar.ContentType
	if contentType == "" {
		contentType = "image/jpeg"
	}
	w.Header().Set("Content-Type", contentType)
	// 服务端有 24h 文件缓存，这里让浏览器也缓存 1 小时。
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(avatar.Data)
}
