package server

import "net/http"

// CacheStats 是本地缓存的规模，单位为行数。
type CacheStats struct {
	// Messages 是消息快照（message_cache）的行数。
	Messages int64 `json:"messages"`
	// Peers 是会话定位（peer_cache）的行数。
	Peers int64 `json:"peers"`
	// Threads 是评论线程定位（thread_cache）的行数。
	Threads int64 `json:"threads"`
}

// handleCacheStats 处理 GET /api/v1/cache：返回缓存规模。
func (s *Server) handleCacheStats(w http.ResponseWriter, r *http.Request) {
	if s.cfg.GetCacheStats == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "缓存管理未接入")
		return
	}
	stats, err := s.cfg.GetCacheStats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "读取缓存规模失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// handleCacheClear 处理 DELETE /api/v1/cache：清空本地缓存并返回删除的行数。
//
// 只删数据库行，磁盘缩略图缓存有独立生命周期；删完之后的第一次预览会重新
// 联网，耗时回到冷启动水平。
func (s *Server) handleCacheClear(w http.ResponseWriter, r *http.Request) {
	if s.cfg.ClearCache == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "缓存管理未接入")
		return
	}
	removed, err := s.cfg.ClearCache(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "清空缓存失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, removed)
}
