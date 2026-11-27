package mtproto

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

// connectTimeout 是等待连接就绪的上限：建立 TCP/TLS、完成 MTProto 握手
// 并确认登录状态。超过这个时间按连接失败处理，避免请求一直挂着。
const connectTimeout = 25 * time.Second

// poolKey 标识一条常驻连接：session 文件、代理与应用凭据任一变化都要重建。
type poolKey struct {
	sessionPath string
	proxy       string
	apiID       int
	apiHash     string
}

// poolEntry 是一条常驻连接。
//
// Run 在后台跑一个循环，客户端自己负责断线重连；请求通过 client.API()
// 并发调用，不再各自建连。
type poolEntry struct {
	key         poolKey
	fingerprint [32]byte

	client *telegram.Client
	cancel context.CancelFunc

	// ready 在「已授权、可以发请求」时关闭；done 在连接彻底退出时关闭。
	ready chan struct{}
	done  chan struct{}

	user     *tg.User
	readyErr error
	once     sync.Once
}

var (
	poolMu sync.Mutex
	pool   = make(map[poolKey]*poolEntry)
)

// acquire 返回一条就绪的常驻连接，没有就新建。
//
// 首次调用会等握手与授权检查完成；后续调用直接复用，耗时只剩一次 RPC。
func (s Session) acquire(ctx context.Context) (*telegram.Client, *tg.User, error) {
	opts := s.options()
	if err := checkLoginOptions(opts); err != nil {
		return nil, nil, err
	}

	// 等待连接就绪也要有上限：底层断网时 Run 可能长时间不返回。
	waitCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	// 第一次用现有条目；如果它刚好在此刻退出，重建一次再试。
	for attempt := 0; attempt < 2; attempt++ {
		entry, err := getOrCreateEntry(opts)
		if err != nil {
			return nil, nil, err
		}

		select {
		case <-entry.ready:
			if entry.readyErr != nil {
				dropEntry(entry)
				return nil, nil, entry.readyErr
			}
			return entry.client, entry.user, nil
		case <-entry.done:
			// 还没就绪就退出了：清掉，下一次尝试重建。
			dropEntry(entry)
		case <-waitCtx.Done():
			return nil, nil, waitCtx.Err()
		}
	}
	return nil, nil, errors.New("连接未就绪")
}

// checkLoginOptions 校验连接参数；错误文案与旧实现保持一致。
func checkLoginOptions(opts LoginOptions) error {
	if opts.APIID <= 0 || opts.APIHash == "" {
		return errors.New("缺少 api 凭据")
	}
	if opts.SessionPath == "" {
		return errors.New("缺少 session 路径")
	}
	if _, err := os.Stat(opts.SessionPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("未找到 session 文件 %s，请先执行 account login", opts.SessionPath)
		}
		return fmt.Errorf("读取 session 文件: %w", err)
	}
	return nil
}

// getOrCreateEntry 返回可用的连接条目，必要时新建。
//
// 用 session 文件的**内容指纹**判断是否换过登录：gotd 每次建立连接都会
// 无条件重写这个文件（内容通常不变），按 mtime 判断会把正常连接当成
// 「账号变了」而反复销毁重建；内容哈希只在真的换了 auth key 时才变。
func getOrCreateEntry(opts LoginOptions) (*poolEntry, error) {
	fingerprint, err := sessionFingerprint(opts.SessionPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// session 被删了（退出登录），顺手关掉残留连接。
			closeEntries(func(key poolKey) bool { return key.sessionPath == opts.SessionPath })
			return nil, fmt.Errorf("未找到 session 文件 %s，请先执行 account login", opts.SessionPath)
		}
		return nil, fmt.Errorf("读取 session 文件: %w", err)
	}

	key := poolKey{
		sessionPath: opts.SessionPath,
		proxy:       opts.Proxy,
		apiID:       opts.APIID,
		apiHash:     opts.APIHash,
	}

	poolMu.Lock()
	defer poolMu.Unlock()

	if entry, ok := pool[key]; ok {
		select {
		case <-entry.done:
			// 已退出，往下重建。
		default:
			if entry.fingerprint == fingerprint {
				return entry, nil
			}
			// session 内容变了（重新登录/切换账号），关掉旧连接。
			log.Printf("session 已更换，重建 Telegram 连接")
			entry.cancel()
		}
		delete(pool, key)
	}

	entry, err := newPoolEntry(key, opts, fingerprint)
	if err != nil {
		return nil, err
	}
	pool[key] = entry
	return entry, nil
}

// sessionFingerprint 计算 session 文件内容的 SHA-256。
func sessionFingerprint(path string) ([32]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(data), nil
}

// newPoolEntry 构造客户端并在后台启动连接。
func newPoolEntry(key poolKey, opts LoginOptions, fingerprint [32]byte) (*poolEntry, error) {
	client, err := newClient(opts)
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	entry := &poolEntry{
		key:         key,
		fingerprint: fingerprint,
		client:      client,
		cancel:      cancel,
		ready:       make(chan struct{}),
		done:        make(chan struct{}),
	}
	log.Printf("建立 Telegram 常驻连接（%s）", filepath.Base(key.sessionPath))
	go entry.run(runCtx)
	return entry, nil
}

// run 保持连接直到被取消；客户端内部的断线重连由 gotd 负责。
func (e *poolEntry) run(ctx context.Context) {
	defer close(e.done)

	err := e.client.Run(ctx, func(ctx context.Context) error {
		status, err := e.client.Auth().Status(ctx)
		if err != nil {
			e.fail(friendlyError(fmt.Errorf("查询登录状态: %w", err)))
			return err
		}
		if !status.Authorized {
			e.fail(errors.New("session 已失效，请重新执行 account login"))
			return errors.New("session 已失效")
		}
		e.user = status.User

		// 连接建立与授权检查期间 gotd 会重写 session 文件（内容通常不变，
		// 但首次连接会补上 auth key / DC 信息）。这里刷新一次基准指纹，
		// 否则下一次请求会把这次写入误判成「换了账号」。
		e.refreshFingerprint()
		e.markReady()

		// 回调返回会让 Run 退出，所以在这里等取消信号。
		<-ctx.Done()
		return nil
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		e.fail(err)
	}
}

// refreshFingerprint 重新读取 session 内容作为新的比对基准。
func (e *poolEntry) refreshFingerprint() {
	fingerprint, err := sessionFingerprint(e.key.sessionPath)
	if err != nil {
		return
	}
	poolMu.Lock()
	e.fingerprint = fingerprint
	poolMu.Unlock()
}

// markReady 标记连接可用，重复调用无副作用。
func (e *poolEntry) markReady() {
	e.once.Do(func() { close(e.ready) })
}

// fail 记录就绪失败并唤醒等待者。
func (e *poolEntry) fail(err error) {
	e.once.Do(func() {
		e.readyErr = err
		close(e.ready)
	})
}

// dropEntry 从池里移除指定条目（如果还在池里）。
func dropEntry(entry *poolEntry) {
	poolMu.Lock()
	if cur, ok := pool[entry.key]; ok && cur == entry {
		delete(pool, entry.key)
	}
	poolMu.Unlock()
	entry.cancel()
}

// closeEntries 关闭并移除匹配的条目，返回被关闭的条目。
func closeEntries(match func(poolKey) bool) []*poolEntry {
	poolMu.Lock()
	entries := make([]*poolEntry, 0, len(pool))
	for key, entry := range pool {
		if match(key) {
			entries = append(entries, entry)
			delete(pool, key)
		}
	}
	poolMu.Unlock()

	for _, entry := range entries {
		entry.cancel()
	}
	return entries
}

// CloseConnections 关闭所有常驻连接，供进程退出时调用。
//
// 等待连接收尾最多三秒，避免退出流程被网络卡住。
func CloseConnections() {
	entries := closeEntries(func(poolKey) bool { return true })
	if len(entries) == 0 {
		return
	}

	timeout := time.After(3 * time.Second)
	for _, entry := range entries {
		select {
		case <-entry.done:
		case <-timeout:
			return
		}
	}
}
