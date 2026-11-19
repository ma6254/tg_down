package server

import (
	"errors"
	"net/http"
	"strconv"
)

// 切换当前账号时业务层可以返回的哨兵错误，handler 据此选择状态码。
var (
	// ErrAccountNotFound 表示账号 ID 不存在。
	ErrAccountNotFound = errors.New("账号不存在")
	// ErrAccountNoSession 表示账号存在但 session 文件缺失，无法切换。
	ErrAccountNoSession = errors.New("账号的 session 文件不可用")
)

// Account 是账号列表项。
//
// 只暴露展示需要的字段：session 路径等本地信息不外传。
type Account struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"` // mtproto | botapi
	Name      string `json:"name"`
	Username  string `json:"username,omitempty"`
	Phone     string `json:"phone,omitempty"`
	State     string `json:"state"` // unauthed | authed | error
	LastError string `json:"last_error,omitempty"`
	CreatedAt int64  `json:"created_at"` // Unix 秒
	Current   bool   `json:"current"`    // 是否为当前选中账号
}

// AccountDetail 是 GET /api/v1/accounts/{id} 的响应：在列表项之上补充
// 更新时间与 session 文件是否可用，等价于 CLI 的 account info。
type AccountDetail struct {
	Account
	UpdatedAt      int64 `json:"updated_at,omitempty"` // Unix 秒
	SessionPresent bool  `json:"session_present"`      // session 文件是否存在
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

// accountIDFromPath 解析路径里的 {id}，非法时返回 ok=false。
func accountIDFromPath(r *http.Request) (id int64, ok bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
