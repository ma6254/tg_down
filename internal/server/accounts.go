package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
)

// 账号相关业务层可以返回的哨兵错误，handler 据此选择状态码。
var (
	// ErrAccountNotFound 表示账号 ID 不存在。
	ErrAccountNotFound = errors.New("账号不存在")
	// ErrAccountNoSession 表示账号存在但 session 文件缺失，无法切换或联网查询。
	ErrAccountNoSession = errors.New("账号的 session 文件不可用")
	// ErrAccountLiveFailed 表示联网查询失败（网络不可达、Telegram 侧报错、session 已失效等）。
	ErrAccountLiveFailed = errors.New("联网查询失败")
)

// Account 是账号列表项。
//
// 只暴露展示需要的字段：session 路径等本地信息不外传。
type Account struct {
	ID   int64  `json:"id"`
	Kind string `json:"kind"` // mtproto | botapi
	// Source 是账号来源：manual（手动登录）| desktop（桌面导入）| bot；旧数据省略。
	Source    string `json:"source,omitempty"`
	Name      string `json:"name"`
	Username  string `json:"username,omitempty"`
	Phone     string `json:"phone,omitempty"`
	State     string `json:"state"` // unauthed | authed | error
	LastError string `json:"last_error,omitempty"`
	// TelegramID 是 Telegram 的 user.id；未绑定或未联网时省略。
	TelegramID int64 `json:"telegram_id,omitempty"`
	CreatedAt  int64 `json:"created_at"` // Unix 秒
	Current    bool  `json:"current"`    // 是否为当前选中账号
}

// AccountDetail 是 GET /api/v1/accounts/{id} 的响应：在列表项之上补充
// 更新时间与 session 文件是否可用，等价于 CLI 的 account info。
type AccountDetail struct {
	Account
	UpdatedAt      int64 `json:"updated_at,omitempty"` // Unix 秒
	SessionPresent bool  `json:"session_present"`      // session 文件是否存在
}

// AccountBirthday 是在线资料里公开的生日；Year 为 0 表示对方只公开了月日。
type AccountBirthday struct {
	Day   int `json:"day"`
	Month int `json:"month"`
	Year  int `json:"year,omitempty"`
}

// AccountLiveInfo 是 GET /api/v1/accounts/{id}/live 的响应：在本地详情之外，
// 联网查询 Telegram 得到的最新资料与已登录客户端，等价于 CLI 的
// account info <id> --live。查询结果只在内存里返回，不写数据库。
type AccountLiveInfo struct {
	// UserID 是 Telegram 用户 ID，与本地账号记录的自增 id 无关。
	UserID   int64            `json:"user_id"`
	Name     string           `json:"name"`
	Username string           `json:"username,omitempty"`
	Phone    string           `json:"phone,omitempty"` // Telegram 返回的号码，展示时请打码
	Bot      bool             `json:"bot,omitempty"`
	Premium  bool             `json:"premium,omitempty"`
	About    string           `json:"about,omitempty"`
	Birthday *AccountBirthday `json:"birthday,omitempty"`
	// Devices 复用探测接口的客户端结构，前端可以用同一套展示逻辑。
	Devices   []ProbeDevice `json:"devices"`
	FetchedAt int64         `json:"fetched_at"` // 查询时间，Unix 秒
}

// AccountsResponse 是 GET /api/v1/accounts 的响应。
type AccountsResponse struct {
	Accounts []Account `json:"accounts"`
	// CurrentAccountID 在没有选中账号时省略。
	CurrentAccountID int64 `json:"current_account_id,omitempty"`
}

// handleAccountList 处理 GET /api/v1/accounts：返回数据库里的账号列表。
func (s *Server) handleAccountList(w http.ResponseWriter, r *http.Request) {
	if s.cfg.ListAccounts == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "账号列表功能未接入")
		return
	}

	accounts, err := s.cfg.ListAccounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "读取账号列表失败: "+err.Error())
		return
	}
	if accounts == nil {
		accounts = []Account{}
	}

	resp := AccountsResponse{Accounts: accounts}
	for _, account := range accounts {
		if account.Current {
			resp.CurrentAccountID = account.ID
			break
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// SelectAccountResponse 是 POST /api/v1/accounts/{id}/select 的响应。
type SelectAccountResponse struct {
	OK               bool  `json:"ok"`
	CurrentAccountID int64 `json:"current_account_id"`
}

// AccountLogoutResult 是 POST /api/v1/accounts/{id}/logout 的响应。
type AccountLogoutResult struct {
	OK bool `json:"ok"`
	// Revoked 表示 Telegram 端是否成功注销；网络失败时本地仍会删除。
	Revoked bool `json:"revoked"`
	// RemovedSession 表示本地 session 文件是否由删除流程清理。
	RemovedSession bool `json:"removed_session"`
}

// handleAccountSelect 处理 POST /api/v1/accounts/{id}/select：
// 把指定账号设为当前账号，等价于 CLI 的 account switch。
func (s *Server) handleAccountSelect(w http.ResponseWriter, r *http.Request) {
	if s.cfg.SelectAccount == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "切换账号功能未接入")
		return
	}

	id, ok := accountIDFromPath(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "账号 ID 必须是正整数")
		return
	}

	if err := s.cfg.SelectAccount(r.Context(), id); err != nil {
		switch {
		case errors.Is(err, ErrAccountNotFound):
			writeError(w, http.StatusNotFound, "not_found", err.Error())
		case errors.Is(err, ErrAccountNoSession):
			writeError(w, http.StatusConflict, "conflict", err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "切换账号失败: "+err.Error())
		}
		return
	}

	writeJSON(w, http.StatusOK, SelectAccountResponse{OK: true, CurrentAccountID: id})
}

// handleAccountGet 处理 GET /api/v1/accounts/{id}：返回单个账号的详情。
func (s *Server) handleAccountGet(w http.ResponseWriter, r *http.Request) {
	if s.cfg.GetAccount == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "账号详情功能未接入")
		return
	}

	id, ok := accountIDFromPath(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "账号 ID 必须是正整数")
		return
	}

	detail, err := s.cfg.GetAccount(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrAccountNotFound) {
			writeError(w, http.StatusNotFound, "not_found", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "读取账号详情失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// handleAccountLive 处理 GET /api/v1/accounts/{id}/live：
// 联网查询账号的最新资料与已登录客户端，等价于 CLI 的 account info <id> --live。
func (s *Server) handleAccountLive(w http.ResponseWriter, r *http.Request) {
	if s.cfg.GetAccountLive == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "在线资料功能未接入")
		return
	}

	id, ok := accountIDFromPath(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "账号 ID 必须是正整数")
		return
	}

	info, err := s.cfg.GetAccountLive(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, ErrAccountNotFound):
			writeError(w, http.StatusNotFound, "not_found", err.Error())
		case errors.Is(err, ErrAccountNoSession):
			writeError(w, http.StatusConflict, "conflict", err.Error())
		case errors.Is(err, context.DeadlineExceeded):
			writeError(w, http.StatusGatewayTimeout, "live_timeout", "联网查询超时，请稍后重试")
		case errors.Is(err, ErrAccountLiveFailed):
			writeError(w, http.StatusBadGateway, "live_failed", err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "联网查询失败: "+err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// accountIDFromPath 解析路径里的 {id}，非法时返回 ok=false。
func accountIDFromPath(r *http.Request) (id int64, ok bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
