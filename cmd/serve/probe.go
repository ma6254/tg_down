package serve

import (
	"context"
	"fmt"

	accountsvc "github.com/ma6254/tg_down/internal/account"
	"github.com/ma6254/tg_down/internal/server"
	"github.com/ma6254/tg_down/internal/utils"
)

// probeSessionDir 是探测导入时保存 session 的目录，与 CLI 的 --session-dir 默认值一致。
const probeSessionDir = "./sessions"

// probeLogin 把 CLI 的 `account login --probe` 搬到 HTTP：
// 读取本机 Telegram Desktop 的 tdata，逐个账号连接 Telegram 校验，
// 再把有效账号写入 sessions/ 与数据库（req.Import 为 false 时只探测不落库）。
func probeLogin(ctx context.Context, req server.ProbeRequest) (server.ProbeResponse, error) {
	db, err := utils.OpenDatabase()
	if err != nil {
		return server.ProbeResponse{}, fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	result, err := accountsvc.New(db).Probe(ctx, accountsvc.ProbeOptions{
		TDataPath:  req.TDataPath,
		Passcode:   req.TDataPasscode,
		Import:     probeImportEnabled(req),
		SessionDir: probeSessionDir,
	})
	if err != nil {
		return server.ProbeResponse{}, err
	}
	return server.NewProbeResponse(result), nil
}

// probeImportEnabled 解析 import 开关，缺省为 true（与 CLI 行为一致）。
func probeImportEnabled(req server.ProbeRequest) bool {
	return req.Import == nil || *req.Import
}
