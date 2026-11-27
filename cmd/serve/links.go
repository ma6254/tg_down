package serve

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	accountsvc "github.com/ma6254/tg_down/internal/account"
	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/link"
	"github.com/ma6254/tg_down/internal/msgcache"
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

// linkSession 是预览用到的 MTProto 能力。
//
// 抽成接口而不是直接依赖 mtproto.Session，是为了让单元测试注入假实现，
// 验证「命中缓存后不再读消息」这类行为。
type linkSession interface {
	ResolveLinks(ctx context.Context, targets []link.Target) ([]mtproto.LinkResult, error)
	ResolveTargets(ctx context.Context, targets []link.Target) (mtproto.ResolveResult, error)
	ReadMessages(ctx context.Context, items []mtproto.ResolvedTarget, maxSize int64) ([]mtproto.LinkResult, error)
}

// linkCache 是消息缓存的读写能力；为 nil 表示不启用缓存。
type linkCache interface {
	LoadPeers(accountID int64, targets []link.Target) (map[int]mtproto.Peer, int64, error)
	SavePeers(accountID, maxSize int64, entries []msgcache.PeerEntry) error
	Lookup(chatID, accountID int64, ids []int) (map[int]msgcache.Entry, error)
	Save(chatID, accountID int64, results []mtproto.LinkResult) error
	MarkGone(chatID, accountID int64, ids []int) error
	Touch(chatID int64, ids []int) error
}

// previewDeps 是预览的依赖。
type previewDeps struct {
	// session 用于把结果转换成接口结构（缩略图 token 需要它）。
	session mtproto.Session
	// rpc 负责联网；生产环境就是 session 本身。
	rpc linkSession
	// cache 为 nil 时退化成「每次都联网」的老路径。
	cache     linkCache
	accountID int64
	// forceRefresh 为 true 时不读缓存，全部重新拉取（失败不降级旧快照）。
	forceRefresh bool
}

// previewLinks 解析 Telegram 消息链接，等价于 CLI 的：
//
//	tg_down download -u <链接> [--expand-text-links]
//
// 命中缓存时只做会话定位，不再读取消息；未命中的消息批量读回后写进缓存。
// 缓存读写失败只记日志，不影响预览结果。
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

	session, accountID, err := previewSession(db)
	if err != nil {
		return server.PreviewResponse{}, err
	}

	deps := previewDeps{
		session:      session,
		rpc:          session,
		accountID:    accountID,
		forceRefresh: req.Refresh,
	}
	if enabled, err := config.NewStore(db).Bool(config.KeyMessageCacheEnabled); err != nil {
		// 配置读不出来时按默认（开启）处理，缓存本身有 TTL 兜底。
		log.Printf("读取消息缓存开关失败（按开启处理）: %v", err)
		deps.cache = msgcache.New(db)
	} else if enabled {
		deps.cache = msgcache.New(db)
	}

	return deps.preview(ctx, req, items, targets)
}

// preview 执行一次预览：解析主链接，按需展开正文链接。
func (d previewDeps) preview(ctx context.Context, req server.PreviewRequest,
	items []server.PreviewItem, targets []link.Target) (server.PreviewResponse, error) {
	results, err := d.results(ctx, targets)
	if err != nil {
		return server.PreviewResponse{}, err
	}
	items = append(items, server.NewPreviewItems(results, false, d.session)...)

	var skippedLinks int
	var truncated bool
	if req.ExpandTextLinks {
		plan := mtproto.PlanLinkExpansion(results, targets, maxPreviewExpand)
		skippedLinks, truncated = plan.Skipped, plan.Truncated

		if len(plan.Targets) > 0 {
			expanded, err := d.results(ctx, plan.Targets)
			if err != nil {
				return server.PreviewResponse{}, err
			}
			items = append(items, server.NewPreviewItems(expanded, true, d.session)...)
		}
	}

	resp := server.NewPreviewResponse(items, skippedLinks)
	resp.Summary.Truncated = truncated
	return resp, nil
}

// results 返回与 targets 一一对应的解析结果。
func (d previewDeps) results(ctx context.Context, targets []link.Target) ([]mtproto.LinkResult, error) {
	// 关闭缓存时走一步到位的 ResolveLinks：一次连接同时定位并读回全部消息。
	if d.cache == nil {
		return d.readAll(ctx, targets)
	}
	return d.readCached(ctx, targets)
}

// readAll 是不启用缓存时的路径：定位之后一次读回全部消息。
func (d previewDeps) readAll(ctx context.Context, targets []link.Target) ([]mtproto.LinkResult, error) {
	var results []mtproto.LinkResult
	err := withPreviewTimeout(ctx, len(targets), func(callCtx context.Context) error {
		var err error
		results, err = d.rpc.ResolveLinks(callCtx, targets)
		return err
	})
	if err != nil {
		return nil, wrapPreviewError(err)
	}
	return results, nil
}

// readCached 是启用缓存时的路径。
//
// 会话定位与消息读取都先查本地：定位命中就不必连 Telegram，
// 消息命中就不必读消息；两者都命中时整轮预览零 RPC。
func (d previewDeps) readCached(ctx context.Context, targets []link.Target) ([]mtproto.LinkResult, error) {
	started := time.Now()
	results := make([]mtproto.LinkResult, len(targets))
	peers := make([]mtproto.Peer, len(targets))
	hasPeer := make([]bool, len(targets))
	for i, target := range targets {
		results[i] = mtproto.LinkResult{Target: target}
	}

	// 1. 会话定位：先查本地缓存（顺带拿到写入时记录的单文件上限）。
	maxSize := int64(0)
	if !d.forceRefresh {
		cached, size, err := d.cache.LoadPeers(d.accountID, targets)
		if err != nil {
			log.Printf("读取会话缓存失败（按未命中处理）: %v", err)
		}
		for i, peer := range cached {
			peers[i], hasPeer[i] = peer, true
		}
		maxSize = size
	}
	peerHits := 0
	for _, ok := range hasPeer {
		if ok {
			peerHits++
		}
	}

	// 2. 缺的部分一次性联网定位：只解析没命中的链接，仍然共用一条连接。
	var missing []int
	for i := range targets {
		if !hasPeer[i] {
			missing = append(missing, i)
		}
	}
	var resolveCost time.Duration
	if len(missing) > 0 {
		sub := make([]link.Target, len(missing))
		for k, i := range missing {
			sub[k] = targets[i]
		}

		begin := time.Now()
		resolved, err := d.resolve(ctx, sub)
		resolveCost = time.Since(begin)
		if err != nil {
			return nil, err
		}
		if resolved.MaxSize > 0 {
			maxSize = resolved.MaxSize
		}

		entries := make([]msgcache.PeerEntry, 0, len(missing))
		for k, item := range resolved.Targets {
			i := missing[k]
			if item.Err != nil {
				results[i].Err = item.Err
				continue
			}
			peers[i], hasPeer[i] = item.Peer, true
			if key, ok := msgcache.PeerKey(targets[i]); ok {
				entries = append(entries, msgcache.PeerEntry{Key: key, Peer: item.Peer})
			}
		}
		if err := d.cache.SavePeers(d.accountID, maxSize, entries); err != nil {
			log.Printf("写入会话缓存失败: %v", err)
		}
	}
	if maxSize <= 0 {
		maxSize = mtproto.MaxFileSize
	}

	// 3. 消息缓存：按会话分组查询，只把未命中的交给联网读取。
	groups, order := groupByPeer(targets, peers, hasPeer, results)
	var readCost time.Duration
	for _, key := range order {
		group := groups[key]

		// 强制刷新时不读缓存：全部进 pending，失败按错误返回而不是降级快照。
		var entries map[int]msgcache.Entry
		if !d.forceRefresh {
			var err error
			entries, err = d.cache.Lookup(group.chatID, d.accountID, group.ids)
			if err != nil {
				// 缓存读失败不影响预览：整组按未命中处理。
				log.Printf("读取消息缓存失败（按未命中处理）: %v", err)
				entries = nil
			}
		}

		var pendingIdx []int
		var pendingItems []mtproto.ResolvedTarget
		var touched []int

		for k, i := range group.indexes {
			results[i].Chat = group.peer
			entry, ok := entries[group.ids[k]]
			if !ok {
				pendingIdx = append(pendingIdx, i)
				pendingItems = append(pendingItems, mtproto.ResolvedTarget{
					Target: targets[i], Peer: group.peer, MessageID: group.ids[k],
				})
				continue
			}

			results[i] = entry.Result
			results[i].Target = targets[i]
			results[i].Cached = true
			results[i].CachedAt = entry.FetchedAt
			results[i].Gone = entry.Status == database.MessageStatusGone
			applyMaxSize(&results[i], maxSize)
			touched = append(touched, group.ids[k])
			if entry.Fresh {
				continue
			}
			// 过期快照先留着：联网失败时降级返回，标记 Stale。
			pendingIdx = append(pendingIdx, i)
			pendingItems = append(pendingItems, mtproto.ResolvedTarget{
				Target: targets[i], Peer: group.peer, MessageID: group.ids[k],
			})
		}

		if len(pendingItems) > 0 {
			begin := time.Now()
			d.refetch(ctx, group.chatID, maxSize, pendingIdx, pendingItems, results)
			readCost += time.Since(begin)
		}
		d.touch(group.chatID, touched)
	}

	logPreview(len(targets), peerHits, resolveCost, readCost, time.Since(started))
	return results, nil
}

// refetch 读取未命中/已过期的消息，成功则刷新缓存，失败则降级旧快照。
func (d previewDeps) refetch(ctx context.Context, chatID int64, maxSize int64,
	pendingIdx []int, pendingItems []mtproto.ResolvedTarget,
	results []mtproto.LinkResult) {
	fetched, err := d.read(ctx, pendingItems, maxSize)
	if err != nil {
		// 整体失败（连接/鉴权）：有旧快照的降级展示，其余如实报错。
		for _, i := range pendingIdx {
			if results[i].Cached {
				results[i].Stale = true
				continue
			}
			results[i].Err = err
		}
		return
	}

	var saved []mtproto.LinkResult
	var goneIDs []int
	for k, result := range fetched {
		i := pendingIdx[k]
		if result.Err != nil {
			if !results[i].Cached {
				results[i].Err = result.Err
				continue
			}
			results[i].Stale = true
			if mtproto.IsMessageGone(result.Err) {
				results[i].Gone = true
				goneIDs = append(goneIDs, pendingItems[k].MessageID)
			}
			continue
		}
		results[i] = result
		saved = append(saved, result)
	}

	d.save(chatID, saved)
	d.markGone(chatID, goneIDs)
}

// resolve 定位链接，超时与错误语义与旧实现一致。
func (d previewDeps) resolve(ctx context.Context, targets []link.Target) (mtproto.ResolveResult, error) {
	var resolved mtproto.ResolveResult
	err := withPreviewTimeout(ctx, len(targets), func(callCtx context.Context) error {
		var err error
		resolved, err = d.rpc.ResolveTargets(callCtx, targets)
		return err
	})
	if err != nil {
		return mtproto.ResolveResult{}, wrapPreviewError(err)
	}
	return resolved, nil
}

// read 读取消息，超时与错误语义与旧实现一致。
func (d previewDeps) read(ctx context.Context, items []mtproto.ResolvedTarget, maxSize int64) ([]mtproto.LinkResult, error) {
	if len(items) == 0 {
		return nil, nil
	}
	var results []mtproto.LinkResult
	err := withPreviewTimeout(ctx, len(items), func(callCtx context.Context) error {
		var err error
		results, err = d.rpc.ReadMessages(callCtx, items, maxSize)
		return err
	})
	if err != nil {
		return nil, wrapPreviewError(err)
	}
	return results, nil
}

// save 尽力写入缓存；失败只记日志。
func (d previewDeps) save(chatID int64, results []mtproto.LinkResult) {
	if len(results) == 0 {
		return
	}
	if err := d.cache.Save(chatID, d.accountID, results); err != nil {
		log.Printf("写入消息缓存失败: %v", err)
	}
}

// markGone 尽力记录「消息不存在」的结论；失败只记日志。
func (d previewDeps) markGone(chatID int64, ids []int) {
	if len(ids) == 0 {
		return
	}
	if err := d.cache.MarkGone(chatID, d.accountID, ids); err != nil {
		log.Printf("标记消息失效失败: %v", err)
	}
}

// touch 尽力刷新最近使用时间；失败只记日志。
func (d previewDeps) touch(chatID int64, ids []int) {
	if len(ids) == 0 {
		return
	}
	if err := d.cache.Touch(chatID, ids); err != nil {
		log.Printf("更新消息缓存时间失败: %v", err)
	}
}

// targetGroup 是同一个会话下的一批链接。
type targetGroup struct {
	chatID  int64
	peer    mtproto.Peer
	indexes []int
	// ids 与 indexes 一一对应，是每条链接要读的消息 ID。
	ids []int
}

// groupByPeer 按会话给已定位的链接分组，保持首次出现的顺序。
//
// 已经定位失败的链接不进组：它们直接以 Err 的形式返回给前端。
func groupByPeer(targets []link.Target, peers []mtproto.Peer, hasPeer []bool,
	results []mtproto.LinkResult) (map[string]*targetGroup, []string) {
	groups := make(map[string]*targetGroup)
	order := make([]string, 0, len(targets))
	for i := range targets {
		if !hasPeer[i] || results[i].Err != nil {
			continue
		}
		key := fmt.Sprintf("%s:%d", peers[i].Kind, peers[i].ID)
		group, ok := groups[key]
		if !ok {
			group = &targetGroup{chatID: peers[i].ID, peer: peers[i]}
			groups[key] = group
			order = append(order, key)
		}
		group.indexes = append(group.indexes, i)
		group.ids = append(group.ids, messageIDFor(targets[i]))
	}
	return groups, order
}

// messageIDFor 返回链接要读的消息 ID：评论链接读的是讨论组里的评论。
func messageIDFor(target link.Target) int {
	if target.CommentID > 0 {
		return target.CommentID
	}
	return target.MessageID
}

// logPreview 记录一轮预览的耗时构成，只在明显慢时输出。
//
// 定位耗时大于 0 说明这轮连过 Telegram；全部命中时三项都是 0，
// 用来判断「慢」到底慢在连、解析还是读消息。
func logPreview(total, peerHits int, resolveCost, readCost, elapsed time.Duration) {
	if elapsed < 500*time.Millisecond {
		return
	}
	log.Printf("预览 %d 条耗时 %s：会话缓存命中 %d/%d，定位 %s，读消息 %s",
		total, elapsed.Round(time.Millisecond), peerHits, total,
		resolveCost.Round(time.Millisecond), readCost.Round(time.Millisecond))
}

// applyMaxSize 用当前账号的上限重算可下载标记。
//
// 缓存行可能由另一个账号写入（普通账号 2GB / Premium 4GB），命中后不能沿用旧值。
func applyMaxSize(result *mtproto.LinkResult, maxSize int64) {
	if maxSize <= 0 {
		return
	}
	if result.Media != nil {
		result.Media.MaxSize = maxSize
		result.Media.Downloadable = result.Media.Size <= maxSize
	}
	for _, media := range result.Medias {
		if media == nil {
			continue
		}
		media.MaxSize = maxSize
		media.Downloadable = media.Size <= maxSize
	}
}

// withPreviewTimeout 按链接数计算超时后执行 fn。
func withPreviewTimeout(ctx context.Context, links int, fn func(context.Context) error) error {
	timeout := previewBaseTimeout + time.Duration(links)*previewPerLink
	if timeout > previewMaxTimeout {
		timeout = previewMaxTimeout
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return fn(callCtx)
}

// wrapPreviewError 把联网失败翻译成 server 的哨兵错误；超时原样返回。
func wrapPreviewError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%w: %v", server.ErrPreviewFailed, err)
}

// previewSession 用当前账号构造解析用的 session，并返回账号 ID（缓存按账号判断可见性）。
func previewSession(db *database.DB) (mtproto.Session, int64, error) {
	service := accountsvc.New(db)
	id, ok, err := service.CurrentAccountID()
	if err != nil {
		return mtproto.Session{}, 0, err
	}
	if !ok {
		return mtproto.Session{}, 0, fmt.Errorf("%w：先执行 account switch <账号ID>", server.ErrNoCurrentAccount)
	}

	account, err := service.Get(id)
	if err != nil {
		return mtproto.Session{}, 0, fmt.Errorf("读取账号 #%d: %w", id, err)
	}
	if account.Kind != database.AccountKindMTProto {
		return mtproto.Session{}, 0, fmt.Errorf("%w：账号 #%d 是 %s，按链接解析暂只支持 mtproto",
			server.ErrNoCurrentAccount, account.ID, account.Kind)
	}
	if !utils.FileExists(account.SessionPath) {
		return mtproto.Session{}, 0, fmt.Errorf("%w：账号 #%d 的 session 文件不存在，请重新登录",
			server.ErrAccountNoSession, account.ID)
	}

	proxy, err := config.NewStore(db).Proxy()
	if err != nil {
		return mtproto.Session{}, 0, fmt.Errorf("读取代理配置: %w", err)
	}
	app, err := service.AppOrDefault()
	if err != nil {
		return mtproto.Session{}, 0, err
	}
	return mtproto.Session{App: app, SessionPath: account.SessionPath, Proxy: proxy}, id, nil
}
