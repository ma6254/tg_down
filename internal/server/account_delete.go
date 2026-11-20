package server

import (
	"errors"
	"net/http"
)

// AccountDeleteResult 是 DELETE /api/v1/accounts/{id} 的响应。
type AccountDeleteResult struct {
	OK bool `json:"ok"`
	// RemovedSession 表示本地 session 文件是否被清理。
	RemovedSession bool `json:"removed_session"`
}

// handleAccountDelete 处理 DELETE /api/v1/accounts/{id}：
// 只删除本地账号记录与 session 文件，不触碰 Telegram 端会话。
func (s *Server) handleAccountDelete(w http.ResponseWriter, r *http.Request) {
	if s.cfg.DeleteAccount == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "删除账号功能未接入")
		return
	}

	id, ok := accountIDFromPath(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "账号 ID 必须是正整数")
		return
	}

	result, err := s.cfg.DeleteAccount(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrAccountNotFound) {
			writeError(w, http.StatusNotFound, "not_found", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "删除账号失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}
