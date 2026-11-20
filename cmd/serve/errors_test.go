package serve

import (
	"errors"
	"fmt"
	"testing"

	accountsvc "github.com/ma6254/tg_down/internal/account"
	"github.com/ma6254/tg_down/internal/download/manager"
	"github.com/ma6254/tg_down/internal/server"
)

// TestMapDownloadError 确认领域错误被翻译成接口层的哨兵错误。
func TestMapDownloadError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{"任务不存在", manager.ErrNotFound, server.ErrDownloadNotFound},
		{"状态冲突", manager.ErrConflict, server.ErrDownloadConflict},
		{"缩略图缺失", manager.ErrThumbnailNotFound, server.ErrThumbnailNotFound},
		{"没有当前账号", manager.ErrNoAccount, server.ErrNoCurrentAccount},
		{"session 不可用", manager.ErrNoSession, server.ErrAccountNoSession},
		{"解析失败", manager.ErrPreviewFailed, server.ErrPreviewFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 包装一层，模拟调用方加上下文信息的情形。
			err := mapDownloadError(fmt.Errorf("上下文: %w", tt.err))
			if !errors.Is(err, tt.want) {
				t.Errorf("err = %v，期望包含 %v", err, tt.want)
			}
		})
	}

	if mapDownloadError(nil) != nil {
		t.Error("nil 应原样返回 nil")
	}
	other := errors.New("boom")
	if got := mapDownloadError(other); !errors.Is(got, other) {
		t.Errorf("未知错误应原样返回，实际 %v", got)
	}
}

// TestMapAccountError 确认账号领域错误的映射。
func TestMapAccountError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{"账号不存在", accountsvc.ErrNotFound, server.ErrAccountNotFound},
		{"session 不可用", accountsvc.ErrNoSession, server.ErrAccountNoSession},
		{"联网失败", accountsvc.ErrLiveFailed, server.ErrAccountLiveFailed},
		{"桌面账号禁止注销", accountsvc.ErrDesktopCannotLogout, server.ErrAccountDesktopLogout},
		{"没有头像", accountsvc.ErrNoAvatar, server.ErrThumbnailNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := mapAccountError(tt.err); !errors.Is(err, tt.want) {
				t.Errorf("err = %v，期望包含 %v", err, tt.want)
			}
		})
	}
}
