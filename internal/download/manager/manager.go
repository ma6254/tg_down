package manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	accountsvc "github.com/ma6254/tg_down/internal/account"
	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/database"
	downloadsvc "github.com/ma6254/tg_down/internal/download"
	"github.com/ma6254/tg_down/internal/download/runner"
	"github.com/ma6254/tg_down/internal/link"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
	"github.com/ma6254/tg_down/internal/thumbcache"
	"github.com/ma6254/tg_down/internal/utils"
)

// 下载任务队列与执行参数。
const (
	// downloadQueueSize 是等待队列长度；超出的创建请求会短暂阻塞等待入队。
	downloadQueueSize = 256
	// downloadPoolSize 是单个文件的多连接数，服务端比 CLI 保守一些。
	downloadPoolSize = 4
	// downloadThreads 是单文件的分块并发数。
	downloadThreads = 4
	// downloadProgressInterval 是进度落库的间隔，避免 SQLite 被高频写打满。
	downloadProgressInterval = 1500 * time.Millisecond
	// downloadResolveTimeout 是任务执行前重新解析消息的超时。
	downloadResolveTimeout = 90 * time.Second
	// taskThumbnailTimeout 限制任务卡片缩略图的下载时间。
	taskThumbnailTimeout = 30 * time.Second
	// 批量解析超时按链接数放宽：建立连接 + 每条一次批量取消息。
	resolveBaseTimeout = 60 * time.Second
	resolvePerLink     = 5 * time.Second
	resolveMaxTimeout  = 5 * time.Minute
)

// Manager 负责任务队列、并发执行与暂停/恢复。
//
// 任务状态持久化在 SQLite；进度只放在内存里，按固定间隔落库，
// 避免每个分块都写一次数据库。
type Manager struct {
	rootDir string
	workers int

	ctx          context.Context
	cancel       context.CancelFunc
	queue        chan uint
	wg           sync.WaitGroup
	onEvent      func(Event)
	thumbnailURL func(mtproto.Session, mtproto.Thumb) string

	mu     sync.Mutex
	runs   map[uint]*downloadRun
	closed bool
}

// downloadRun 是一个正在执行的任务的运行时状态。
type downloadRun struct {
	cancel context.CancelFunc
	// done 在 worker 结束本任务时关闭，pause/delete 用它等待状态落库。
	done chan struct{}

	mu          sync.Mutex
	paused      bool
	deleted     bool
	offset      int64
	speed       int64
	size        int64
	lastTime    time.Time
	lastDone    int64
	lastPersist time.Time
}

// New 创建下载管理器。
//
// onEvent 在任务状态变化时被调用（转成 SSE 由调用方决定）；
// thumbnailURL 为任务卡片封面登记一次性 token 地址，返回空串表示不展示封面。
func New(cfg Config) *Manager {
	workers := cfg.Workers
	if workers < 1 {
		workers = 1
	}
	if workers > 8 {
		workers = 8
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		rootDir:      cfg.RootDir,
		workers:      workers,
		ctx:          ctx,
		cancel:       cancel,
		queue:        make(chan uint, downloadQueueSize),
		runs:         make(map[uint]*downloadRun),
		onEvent:      cfg.OnEvent,
		thumbnailURL: cfg.ThumbnailURL,
	}
}

// Start 启动 worker，并把上次进程残留的 running 任务标记为 paused。
func (m *Manager) Start() error {
	db, err := utils.OpenDatabase()
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.ResetRunningDownloads(); err != nil {
		return err
	}

	for i := 0; i < m.workers; i++ {
		m.wg.Add(1)
		go m.worker()
	}

	// 上次进程退出时还在排队的任务继续执行；running 的已在上一步转为 paused。
	pending, err := db.ListDownloadTasks(database.DownloadStatusPending, 1000)
	if err != nil {
		return err
	}
	go func() {
		for _, task := range pending {
			m.enqueue(task.ID)
		}
	}()
	return nil
}

// Stop 取消所有运行中的任务并等待 worker 退出；状态会落库为 paused。
func (m *Manager) Stop(ctx context.Context) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.cancel()
	m.mu.Unlock()

	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// mediaURL 为缩略图登记 token 并返回可访问地址；未注入登记函数时不带封面。
func (m *Manager) mediaURL(session mtproto.Session, thumb mtproto.Thumb) string {
	if m.thumbnailURL == nil {
		return ""
	}
	token := m.thumbnailURL(session, thumb)
	if token == "" {
		return ""
	}
	return "/api/v1/media/thumbnail/" + token
}

// publishEvent 把事件交给调用方（cmd 侧转成 SSE 广播）。
func (m *Manager) publishEvent(event Event) {
	if m.onEvent == nil {
		return
	}
	m.onEvent(event)
}

// publishTaskUpdate 读取任务最新状态并广播整条记录，前端直接 upsert。
func (m *Manager) publishTaskUpdate(id uint) {
	db, err := utils.OpenDatabase()
	if err != nil {
		return
	}
	defer db.Close()

	task, err := db.GetDownloadTask(id)
	if err != nil {
		return
	}
	m.publishEvent(Event{Type: EventTaskUpdate, Task: m.snapshot(task)})
}

// publishProgress 广播一条轻量进度；前端只更新 offset/speed。
func (m *Manager) publishProgress(id uint, offset, speed, size int64) {
	m.publishEvent(Event{Type: EventTaskProgress, ID: id, Offset: offset, Speed: speed, Size: size})
}

func (m *Manager) worker() {
	defer m.wg.Done()
	for {
		select {
		case <-m.ctx.Done():
			return
		case id := <-m.queue:
			m.execute(id)
		}
	}
}

// enqueue 把任务放进等待队列；队列会阻塞，直到有空位或服务停止。
func (m *Manager) enqueue(id uint) {
	select {
	case m.queue <- id:
	case <-m.ctx.Done():
	}
}

// create 解析链接、创建任务并立即入队。
func (m *Manager) Create(ctx context.Context, req CreateRequest) (CreateResult, error) {
	resp := CreateResult{}

	db, err := utils.OpenDatabase()
	if err != nil {
		return resp, fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	service := accountsvc.New(db)
	accountID, ok, err := service.CurrentAccountID()
	if err != nil {
		return resp, err
	}
	if !ok {
		return resp, fmt.Errorf("%w：先执行 account switch <账号ID>", ErrNoAccount)
	}
	account, err := service.Get(accountID)
	if err != nil {
		return resp, fmt.Errorf("读取账号 #%d: %w", accountID, err)
	}
	if account.Kind != database.AccountKindMTProto {
		return resp, fmt.Errorf("%w：账号 #%d 是 %s，下载暂只支持 mtproto",
			ErrNoAccount, accountID, account.Kind)
	}
	if !utils.FileExists(account.SessionPath) {
		return resp, fmt.Errorf("%w：账号 #%d 的 session 文件不存在，请重新登录",
			ErrNoSession, accountID)
	}

	proxy, err := config.NewStore(db).Proxy()
	if err != nil {
		return resp, fmt.Errorf("读取代理配置: %w", err)
	}
	app, err := service.AppOrDefault()
	if err != nil {
		return resp, err
	}
	session := mtproto.Session{App: app, SessionPath: account.SessionPath, Proxy: proxy}

	parsed := link.ParseAll(req.Links)
	targets := make([]link.Target, 0, len(parsed))
	rawByKey := make(map[string]string, len(parsed))
	for _, result := range parsed {
		if result.Err != nil {
			resp.Failed = append(resp.Failed, Failure{
				Link:  result.Raw,
				Error: result.Err.Error(),
			})
			continue
		}
		key := result.Target.Key()
		if _, seen := rawByKey[key]; seen {
			continue
		}
		rawByKey[key] = result.Raw
		targets = append(targets, result.Target)
	}
	if len(targets) == 0 {
		if len(resp.Failed) > 0 {
			return resp, nil
		}
		return resp, fmt.Errorf("%w：没有解析出可下载的链接", ErrConflict)
	}

	timeout := resolveBaseTimeout + time.Duration(len(targets))*resolvePerLink
	if timeout > resolveMaxTimeout {
		timeout = resolveMaxTimeout
	}
	resolveCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	results, err := session.ResolveLinks(resolveCtx, targets)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return resp, err
		}
		return resp, fmt.Errorf("%w: %v", ErrPreviewFailed, err)
	}

	for _, result := range results {
		raw := result.Target.Raw
		if result.Err != nil {
			resp.Failed = append(resp.Failed, Failure{Link: raw, Error: result.Err.Error()})
			continue
		}

		// 相册链接会展开成多条任务：每张图/视频各有一条消息。
		medias := result.Medias
		if len(medias) == 0 && result.Media != nil {
			medias = []*mtproto.Media{result.Media}
		}
		if len(medias) == 0 {
			resp.Skipped = append(resp.Skipped, Skip{Link: raw, Reason: "该消息没有可下载的媒体"})
			continue
		}

		for _, media := range medias {
			link := media.Link
			if link == "" {
				link = raw
			}
			if !media.Downloadable {
				resp.Skipped = append(resp.Skipped, Skip{Link: link, Reason: "超过当前账号的单文件上限"})
				continue
			}
			messageID := media.MessageID
			if messageID == 0 {
				messageID = result.MessageID
			}
			thumbnailURL := ""
			thumbKey := ""
			if media.Thumb != nil {
				thumbnailURL = m.mediaURL(session, *media.Thumb)
				thumbKey = thumbcache.Key(*media.Thumb)
			}
			previewURL := ""
			previewKey := ""
			if media.Preview != nil {
				previewURL = m.mediaURL(session, *media.Preview)
				previewKey = thumbcache.Key(*media.Preview)
			}

			task := &database.DownloadTask{
				Link:         link,
				AccountID:    accountID,
				ChatID:       result.Chat.ID,
				ChatTitle:    result.Chat.Title,
				MessageID:    messageID,
				FileName:     media.FileName,
				FileSize:     media.Size,
				Kind:         string(media.Kind),
				MIME:         media.MIME,
				Status:       database.DownloadStatusPending,
				Overwrite:    req.Overwrite,
				ThumbnailURL: thumbnailURL,
				PreviewURL:   previewURL,
				ThumbKey:     thumbKey,
				PreviewKey:   previewKey,
			}
			created, err := db.CreateDownloadTask(task)
			if err != nil {
				resp.Failed = append(resp.Failed, Failure{Link: link, Error: err.Error()})
				continue
			}
			if created {
				resp.Created = append(resp.Created, task.ID)
				m.publishTaskUpdate(task.ID)
				m.enqueue(task.ID)
				continue
			}
			resp.Existing = append(resp.Existing, task.ID)
		}
	}
	return resp, nil
}

// taskThumbnailSlots 限制同时抓取缩略图的数量：列表首屏可能一次可见多行，
// 串行会让图片排队几十秒，完全并发又会打开过多 Telegram 连接。
var taskThumbnailSlots = make(chan struct{}, 3)

// thumbnail 返回任务卡片用的缩略图。
//
// 优先读 <数据目录>/thumbnail/ 里的文件缓存：键在创建任务时算好，
// 与进程、token 无关，重启后仍然可用。缓存缺失时重新解析消息、
// 下载封面并写盘，同时把键补进数据库，后续请求不再联网。
func (m *Manager) Thumbnail(ctx context.Context, id uint) ([]byte, error) {
	task, err := loadTask(id)
	if err != nil {
		return nil, err
	}
	if data, ok := thumbcache.Get(task.ThumbKey); ok {
		return data, nil
	}

	select {
	case taskThumbnailSlots <- struct{}{}:
		defer func() { <-taskThumbnailSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// 等锁期间可能已有别的请求写好缓存或补了键。
	task, err = loadTask(id)
	if err != nil {
		return nil, err
	}
	if data, ok := thumbcache.Get(task.ThumbKey); ok {
		return data, nil
	}

	session, err := taskSession()
	if err != nil {
		return nil, err
	}
	parsed := link.ParseAll([]string{task.Link})
	if len(parsed) != 1 || parsed[0].Err != nil {
		return nil, ErrThumbnailNotFound
	}

	resolveCtx, cancelResolve := context.WithTimeout(ctx, downloadResolveTimeout)
	results, err := session.ResolveLinks(resolveCtx, []link.Target{parsed[0].Target})
	cancelResolve()
	if err != nil {
		return nil, err
	}
	if len(results) != 1 || results[0].Err != nil {
		return nil, ErrThumbnailNotFound
	}

	media := pickTaskMedia(results[0], task.MessageID)
	if media == nil {
		return nil, ErrThumbnailNotFound
	}
	thumb := media.Thumb
	if thumb == nil {
		thumb = media.Preview
	}
	if thumb == nil {
		return nil, ErrThumbnailNotFound
	}

	// 历史任务没有登记键，解析后先看同一张图是否已被预览页缓存。
	key := thumbcache.Key(*thumb)
	if data, ok := thumbcache.Get(key); ok {
		m.saveThumbKey(id, key)
		return data, nil
	}

	fetchCtx, cancelFetch := context.WithTimeout(ctx, taskThumbnailTimeout)
	defer cancelFetch()
	fetcher, err := mtproto.SharedThumbnailFetcher(fetchCtx, session)
	if err != nil {
		return nil, err
	}
	data, err := fetcher.Download(fetchCtx, *thumb)
	if err != nil {
		return nil, err
	}

	// 写盘失败不影响本次返回，下次请求会重试。
	_ = thumbcache.Put(key, data)
	m.saveThumbKey(id, key)
	return data, nil
}

// saveThumbKey 把缓存键写回任务；失败只影响下次是否重新解析。
func (m *Manager) saveThumbKey(id uint, key string) {
	db, err := utils.OpenDatabase()
	if err != nil {
		return
	}
	defer db.Close()
	_ = db.UpdateDownloadTask(id, map[string]any{"thumb_key": key})
}

// loadTask 打开数据库读取任务并立即关闭连接。
func loadTask(id uint) (database.DownloadTask, error) {
	db, err := utils.OpenDatabase()
	if err != nil {
		return database.DownloadTask{}, fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	task, err := db.GetDownloadTask(id)
	if err != nil {
		if errors.Is(err, database.ErrDownloadNotFound) {
			return database.DownloadTask{}, ErrNotFound
		}
		return database.DownloadTask{}, err
	}
	return task, nil
}

// pickTaskMedia 优先返回任务对应的那条媒体（相册成员按 MessageID 匹配）。
func pickTaskMedia(result mtproto.LinkResult, messageID int) *mtproto.Media {
	if messageID > 0 {
		for _, media := range result.Medias {
			if media.MessageID == messageID {
				return media
			}
		}
	}
	if result.Media != nil {
		return result.Media
	}
	if len(result.Medias) > 0 {
		return result.Medias[0]
	}
	return nil
}

// list 返回任务列表，运行中的任务用内存里的实时进度覆盖。
func (m *Manager) List(ctx context.Context, query ListQuery) ([]Task, error) {
	db, err := utils.OpenDatabase()
	if err != nil {
		return nil, fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	tasks, err := db.ListDownloadTasks(query.Status, query.Limit)
	if err != nil {
		return nil, err
	}

	out := make([]Task, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, m.snapshot(task))
	}
	return out, nil
}

// get 返回单个任务，带运行中的实时进度。
func (m *Manager) Get(ctx context.Context, id uint) (Task, error) {
	db, err := utils.OpenDatabase()
	if err != nil {
		return Task{}, fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	task, err := db.GetDownloadTask(id)
	if err != nil {
		if errors.Is(err, database.ErrDownloadNotFound) {
			return Task{}, ErrNotFound
		}
		return Task{}, err
	}
	return m.snapshot(task), nil
}

// control 执行 pause / resume / retry。
func (m *Manager) Control(ctx context.Context, id uint, action string) (Task, error) {
	switch action {
	case "pause":
		return m.pause(id)
	case "resume":
		return m.resume(id, false)
	case "retry":
		return m.resume(id, true)
	default:
		return Task{}, fmt.Errorf("%w：未知操作 %q", ErrConflict, action)
	}
}

// pause 取消运行中的任务；等待中的任务直接标记为暂停。
func (m *Manager) pause(id uint) (Task, error) {
	db, err := utils.OpenDatabase()
	if err != nil {
		return Task{}, fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	task, err := db.GetDownloadTask(id)
	if err != nil {
		if errors.Is(err, database.ErrDownloadNotFound) {
			return Task{}, ErrNotFound
		}
		return Task{}, err
	}
	if task.Status != database.DownloadStatusRunning && task.Status != database.DownloadStatusPending {
		return Task{}, fmt.Errorf("%w：只有运行中或等待中的任务可以暂停", ErrConflict)
	}

	m.mu.Lock()
	run := m.runs[id]
	m.mu.Unlock()
	if run != nil {
		run.markPaused()
		run.cancel()
		// 等 worker 落库状态，避免刚暂停的响应仍显示 running。
		select {
		case <-run.done:
		case <-time.After(3 * time.Second):
		}
	}

	if err := db.SetDownloadPaused(id, ""); err != nil {
		return Task{}, err
	}
	m.publishTaskUpdate(id)
	return m.Get(context.Background(), id)
}

// resume 把暂停（或失败重试）的任务重新入队。
func (m *Manager) resume(id uint, retry bool) (Task, error) {
	db, err := utils.OpenDatabase()
	if err != nil {
		return Task{}, fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	task, err := db.GetDownloadTask(id)
	if err != nil {
		if errors.Is(err, database.ErrDownloadNotFound) {
			return Task{}, ErrNotFound
		}
		return Task{}, err
	}
	if retry {
		if task.Status != database.DownloadStatusFailed {
			return Task{}, fmt.Errorf("%w：只有失败的任务可以重试", ErrConflict)
		}
	} else if task.Status != database.DownloadStatusPaused {
		return Task{}, fmt.Errorf("%w：只有暂停的任务可以恢复", ErrConflict)
	}

	if err := db.UpdateDownloadTask(id, map[string]any{
		"status": database.DownloadStatusPending,
		"error":  "",
		"speed":  0,
	}); err != nil {
		return Task{}, err
	}
	m.publishTaskUpdate(id)
	m.enqueue(id)
	return m.Get(context.Background(), id)
}

// remove 删除任务记录，可选删除已下载文件与 .part 临时文件。
func (m *Manager) Remove(id uint, deleteFile bool) error {
	db, err := utils.OpenDatabase()
	if err != nil {
		return fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	task, err := db.GetDownloadTask(id)
	if err != nil {
		if errors.Is(err, database.ErrDownloadNotFound) {
			return ErrNotFound
		}
		return err
	}

	m.mu.Lock()
	run := m.runs[id]
	m.mu.Unlock()
	if run != nil {
		run.markDeleted()
		run.cancel()
		// 等 worker 退出再删文件，否则 Windows 上文件句柄仍被占用。
		select {
		case <-run.done:
		case <-time.After(3 * time.Second):
		}
	}

	// worker 退出后重新读一次，拿到它写入的最新 save_path。
	if latest, err := db.GetDownloadTask(id); err == nil {
		task.SavePath = latest.SavePath
	}

	if err := db.DeleteDownloadTask(id); err != nil {
		if errors.Is(err, database.ErrDownloadNotFound) {
			return ErrNotFound
		}
		return err
	}
	m.publishEvent(Event{Type: EventTaskDeleted, ID: id})

	// .part 永远清理；最终文件只在 delete_file=true 时删除。
	if task.SavePath != "" {
		_ = os.Remove(task.SavePath + ".part")
		if deleteFile {
			_ = os.Remove(task.SavePath)
		}
	}
	return nil
}

// snapshot 把数据库任务与内存里的实时进度合成一条可展示状态。
func (m *Manager) snapshot(task database.DownloadTask) Task {
	out := Task{Row: task, Offset: task.Offset, Speed: task.Speed}

	m.mu.Lock()
	run := m.runs[task.ID]
	m.mu.Unlock()
	// 只有数据库仍标记为 running 时才用内存进度覆盖。
	// 完成/失败/暂停的落库先于 worker 退出，若这里无条件覆盖，
	// done 事件会被改写成 running%100，前端要等下一次轮询才知道已完成。
	if run != nil && task.Status == database.DownloadStatusRunning {
		offset, speed := run.progress()
		out.Offset = offset
		out.Speed = speed
	}
	return out
}

// execute 执行一条任务：重新解析消息拿最新的文件引用，然后下载。
func (m *Manager) execute(id uint) {
	db, err := utils.OpenDatabase()
	if err != nil {
		return
	}
	task, err := db.GetDownloadTask(id)
	db.Close()
	if err != nil || task.Status != database.DownloadStatusPending {
		return
	}

	runCtx, cancel := context.WithCancel(m.ctx)
	now := time.Now()
	run := &downloadRun{
		cancel:      cancel,
		done:        make(chan struct{}),
		size:        task.FileSize,
		lastTime:    now,
		lastPersist: now,
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		cancel()
		return
	}
	if _, busy := m.runs[id]; busy {
		m.mu.Unlock()
		cancel()
		return
	}
	m.runs[id] = run
	m.mu.Unlock()

	defer func() {
		cancel()
		m.mu.Lock()
		delete(m.runs, id)
		m.mu.Unlock()
		close(run.done)
	}()

	if err := m.updateTask(id, map[string]any{
		"status": database.DownloadStatusRunning,
		"speed":  0,
		"error":  "",
	}); err != nil {
		return
	}
	m.publishTaskUpdate(id)

	fail := func(message string) {
		if run.isDeleted() {
			return
		}
		if run.isPaused() || errors.Is(runCtx.Err(), context.Canceled) {
			m.pauseAfterCancel(id, run)
			return
		}
		_ = m.updateTask(id, map[string]any{
			"status": database.DownloadStatusFailed,
			"speed":  0,
			"error":  message,
		})
		m.publishTaskUpdate(id)
	}

	session, err := taskSession()
	if err != nil {
		fail(err.Error())
		return
	}

	parsed := link.ParseAll([]string{task.Link})
	if len(parsed) != 1 || parsed[0].Err != nil {
		fail("链接无法解析")
		return
	}

	resolveCtx, cancelResolve := context.WithTimeout(runCtx, downloadResolveTimeout)
	result, err := session.ResolveLink(resolveCtx, parsed[0].Target)
	cancelResolve()
	if err != nil {
		fail(friendlyTaskError(err))
		return
	}
	if result.Err != nil {
		fail(result.Err.Error())
		return
	}
	media := result.Media
	if media == nil {
		fail("该消息没有可下载的媒体")
		return
	}
	if !media.Downloadable {
		fail("超过当前账号的单文件上限")
		return
	}

	runner := runner.Runner{
		Session: session,
		RootDir: m.rootDir,
		// 一个任务就是一个文件：并发交给队列控制，这里固定 1。
		Concurrency: 1,
		Threads:     downloadThreads,
		PoolSize:    downloadPoolSize,
		Force:       task.Overwrite,
		Overwrite:   task.Overwrite,
		Hooks: runner.Hooks{
			OnStart: func(_ mtproto.MediaJob, plan downloadsvc.Plan) {
				run.setOffset(plan.Offset)
				_ = m.updateTask(id, map[string]any{
					"save_path": plan.FinalPath,
					"offset":    plan.Offset,
				})
				m.publishTaskUpdate(id)
			},
			OnProgress: func(_ mtproto.MediaJob, done, _ int64) {
				run.observe(done)
				if run.shouldPersist() {
					offset, speed := run.progress()
					_ = m.updateTask(id, map[string]any{
						"offset": offset,
						"speed":  speed,
					})
					m.publishProgress(id, offset, speed, run.sizeValue())
				}
			},
		},
	}
	results, err := runner.Download(runCtx, []mtproto.MediaJob{{
		ChatTitle: task.ChatTitle,
		FileName:  task.FileName,
		Size:      task.FileSize,
		Media:     media,
	}})
	if err != nil {
		fail(friendlyTaskError(err))
		return
	}
	// 取消可能在开始传输前生效：这时 DownloadMedia 直接返回空结果，不能当成功。
	if runCtx.Err() != nil {
		fail(context.Canceled.Error())
		return
	}
	if len(results) != 1 || (results[0].Err == nil && results[0].Path == "" && !results[0].Skipped) {
		fail("下载没有产生结果，请重试")
		return
	}

	resultTask := results[0]
	switch {
	case resultTask.Err != nil:
		fail(friendlyTaskError(resultTask.Err))
	case resultTask.Skipped:
		_ = m.updateTask(id, map[string]any{
			"status":    database.DownloadStatusDone,
			"offset":    resultTask.Size,
			"speed":     0,
			"save_path": resultTask.Path,
			"error":     "",
		})
		m.publishTaskUpdate(id)
	default:
		_ = m.updateTask(id, map[string]any{
			"status":    database.DownloadStatusDone,
			"offset":    resultTask.Size,
			"speed":     0,
			"save_path": resultTask.Path,
			"error":     "",
		})
		m.publishTaskUpdate(id)
	}
}

// pauseAfterCancel 在任务被取消（暂停/删除/停止服务）后落库状态。
func (m *Manager) pauseAfterCancel(id uint, run *downloadRun) {
	if run.isDeleted() {
		return
	}
	message := ""
	if errors.Is(m.ctx.Err(), context.Canceled) {
		message = "服务停止，任务已暂停"
	}
	_ = m.updateTask(id, map[string]any{
		"status": database.DownloadStatusPaused,
		"speed":  0,
		"error":  message,
		"offset": run.offsetValue(),
	})
	m.publishTaskUpdate(id)
}

// updateTask 更新任务字段；任务已被删除时忽略。
func (m *Manager) updateTask(id uint, fields map[string]any) error {
	db, err := utils.OpenDatabase()
	if err != nil {
		return err
	}
	defer db.Close()
	err = db.UpdateDownloadTask(id, fields)
	if errors.Is(err, database.ErrDownloadNotFound) {
		return nil
	}
	return err
}

// taskSession 读取当前账号的 session 与代理，组装 MTProto 连接参数。
func taskSession() (mtproto.Session, error) {
	db, err := utils.OpenDatabase()
	if err != nil {
		return mtproto.Session{}, err
	}
	defer db.Close()

	service := accountsvc.New(db)
	id, ok, err := service.CurrentAccountID()
	if err != nil {
		return mtproto.Session{}, err
	}
	if !ok {
		return mtproto.Session{}, errors.New("没有当前账号：先执行 account switch <账号ID>")
	}
	account, err := service.Get(id)
	if err != nil {
		return mtproto.Session{}, fmt.Errorf("读取账号 #%d: %w", id, err)
	}
	if account.Kind != database.AccountKindMTProto {
		return mtproto.Session{}, fmt.Errorf("账号 #%d 是 %s，下载暂只支持 mtproto", id, account.Kind)
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

// friendlyTaskError 把常见失败整理成可读文案。
func friendlyTaskError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "file reference"):
		return "文件引用已过期，请重试任务"
	case strings.Contains(lower, "flood_wait"), strings.Contains(lower, "flood wait"):
		return "Telegram 限流：" + message
	}
	return message
}

// markPaused / markDeleted / observe 等运行时状态方法。

func (r *downloadRun) markPaused() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paused = true
}

func (r *downloadRun) markDeleted() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deleted = true
}

func (r *downloadRun) isPaused() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.paused
}

func (r *downloadRun) isDeleted() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.deleted
}

func (r *downloadRun) setOffset(offset int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.offset = offset
	r.lastDone = offset
}

func (r *downloadRun) observe(done int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if elapsed := now.Sub(r.lastTime); elapsed >= 500*time.Millisecond {
		delta := done - r.lastDone
		if delta >= 0 {
			r.speed = int64(float64(delta) / elapsed.Seconds())
		}
		r.lastDone = done
		r.lastTime = now
	}
	r.offset = done
}

func (r *downloadRun) progress() (offset, speed int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.offset, r.speed
}

func (r *downloadRun) offsetValue() int64 {
	offset, _ := r.progress()
	return offset
}

func (r *downloadRun) sizeValue() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.size
}

// shouldPersist 控制进度落库频率；调用 observe 之后使用。
func (r *downloadRun) shouldPersist() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if now.Sub(r.lastPersist) < downloadProgressInterval {
		return false
	}
	r.lastPersist = now
	return true
}
