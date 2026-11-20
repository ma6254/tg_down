package serve

import (
	"context"
	"errors"
	"fmt"
	"time"

	accountsvc "github.com/ma6254/tg_down/internal/account"
	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/link"
	"github.com/ma6254/tg_down/internal/server"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
	"github.com/ma6254/tg_down/internal/utils"
)

const (
	// maxPreviewExpand 是接口一次最多展开的正文链接数，与 CLI 保持一致。
	maxPreviewExpand = 200
	// 解析超时按链接数放宽：建立连接 + 每条一次批量取消息。
	previewBaseTimeout = 60 * time.Second
	previewPerLink     = 5 * time.Second
	previewMaxTimeout  = 5 * time.Minute
)

// previewLinks 解析 Telegram 消息链接，等价于 CLI 的：
//
//	tg_down download -u <链接> [--expand-text-links]
//
// 只联网读取消息，不写数据库、不创建下载任务。映射到接口结构的部分
// 在 internal/server（NewPreviewItems / NewPreviewResponse）。
func previewLinks(ctx context.Context, req server.PreviewRequest) (server.PreviewResponse, error) {
	if err := ctx.Err(); err != nil {
		return server.PreviewResponse{}, err
	}

	// 先做纯字符串解析：全部非法时直接返回逐条错误，不必连 Telegram。
	parsed := link.ParseAll(req.Links)
	items := make([]server.PreviewItem, 0, len(parsed))
	targets := make([]link.Target, 0, len(parsed))
	for _, result := range parsed {
		if result.Err != nil {
			items = append(items, server.NewPreviewErrorItem(result.Raw, result.Err))
			continue
		}
		targets = append(targets, result.Target)
	}
	if len(targets) == 0 {
		return server.NewPreviewResponse(items, 0), nil
	}

	db, err := utils.OpenDatabase()
	if err != nil {
		return server.PreviewResponse{}, fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	session, err := previewSession(db)
	if err != nil {
		return server.PreviewResponse{}, err
	}

	results, err := previewResolve(ctx, session, targets)
	if err != nil {
		return server.PreviewResponse{}, err
	}
	items = append(items, server.NewPreviewItems(results, false, session)...)

	var skippedLinks int
	var truncated bool
	if req.ExpandTextLinks {
		plan := mtproto.PlanLinkExpansion(results, targets, maxPreviewExpand)
		skippedLinks, truncated = plan.Skipped, plan.Truncated

		if len(plan.Targets) > 0 {
			expanded, err := previewResolve(ctx, session, plan.Targets)
			if err != nil {
				return server.PreviewResponse{}, err
			}
			items = append(items, server.NewPreviewItems(expanded, true, session)...)
		}
	}

	resp := server.NewPreviewResponse(items, skippedLinks)
	resp.Summary.Truncated = truncated
	return resp, nil
}

// previewSession 用当前账号构造解析用的 session。
func previewSession(db *database.DB) (mtproto.Session, error) {
	service := accountsvc.New(db)
	id, ok, err := service.CurrentAccountID()
	if err != nil {
		return mtproto.Session{}, err
	}
	if !ok {
		return mtproto.Session{}, fmt.Errorf("%w：先执行 account switch <账号ID>", server.ErrNoCurrentAccount)
	}

	account, err := service.Get(id)
	if err != nil {
		return mtproto.Session{}, fmt.Errorf("读取账号 #%d: %w", id, err)
	}
	if account.Kind != database.AccountKindMTProto {
		return mtproto.Session{}, fmt.Errorf("%w：账号 #%d 是 %s，按链接解析暂只支持 mtproto",
			server.ErrNoCurrentAccount, id, account.Kind)
	}
	if !utils.FileExists(account.SessionPath) {
		return mtproto.Session{}, fmt.Errorf("%w：账号 #%d 的 session 文件不存在，请重新登录",
			server.ErrAccountNoSession, account.ID)
	}

	proxy, err := config.NewStore(db).Proxy()
	if err != nil {
		return mtproto.Session{}, fmt.Errorf("读取代理配置: %w", err)
	}
	app, err := service.AppOrDefault()
	if err != nil {
		return mtproto.Session{}, err
	}
	return mtproto.Session{App: app, SessionPath: account.SessionPath, Proxy: proxy}, nil
}

// previewResolve 按链接数计算超时后调用一次批量解析。
func previewResolve(ctx context.Context, session mtproto.Session, targets []link.Target) ([]mtproto.LinkResult, error) {
	timeout := previewBaseTimeout + time.Duration(len(targets))*previewPerLink
	if timeout > previewMaxTimeout {
		timeout = previewMaxTimeout
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	results, err := session.ResolveLinks(callCtx, targets)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", server.ErrPreviewFailed, err)
	}
	return results, nil
}
