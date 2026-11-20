package server

import (
	"context"
	"errors"
	"net/http"
)

// handleAccountLogout 处理 POST /api/v1/accounts/{id}/logout：
// 尽力吊销 Telegram session，并删除本地账号记录与文件。
func (s *Server) handleAccountLogout(w http.ResponseWriter, r *http.Request) {
	if s.cfg.LogoutAccount == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "登出功能未接入")
		return
	}

	id, ok := accountIDFromPath(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "账号 ID 必须是正整数")
		return
	}

	result, err := s.cfg.LogoutAccount(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, ErrAccountNotFound):
			writeError(w, http.StatusNotFound, "not_found", err.Error())
		case errors.Is(err, ErrAccountDesktopLogout):
			writeError(w, http.StatusConflict, "conflict", err.Error())
		case errors.Is(err, context.DeadlineExceeded):
			writeError(w, http.StatusGatewayTimeout, "logout_timeout", "登出超时，请稍后重试")
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "登出失败: "+err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, result)
}
