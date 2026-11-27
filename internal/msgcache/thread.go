package msgcache

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/link"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// ThreadTTL 是「帖子 → 讨论组线程」缓存的有效期。
//
// 映射几乎不变（除非帖子被删或讨论组迁移），比会话缓存给得长得多；
// 评论内容本身不缓存，所以不会因此看到过期评论。
const ThreadTTL = 30 * 24 * time.Hour

// ThreadKey 返回评论线程在缓存里的键；没有消息 ID 时返回 false。
//
// 键里不带账号之外的信息：同一个帖子在不同账号下指向同一条讨论消息，
// 但账号之间的可见性可能不同，所以缓存行仍然按账号隔离。
func ThreadKey(target link.Target) (string, bool) {
	if target.MessageID <= 0 {
		return "", false
	}
	post := strconv.Itoa(target.MessageID)
	switch {
	case target.Username != "":
		return "t:u:" + strings.ToLower(target.Username) + "#" + post, true
	case target.ChannelID != 0:
		return "t:c:" + strconv.FormatInt(target.ChannelID, 10) + "#" + post, true
	}
	return "", false
}

// LoadThread 读取线程定位结果；没有或已过期时返回 nil。
func (c *Cache) LoadThread(accountID int64, target link.Target) *mtproto.CommentThread {
	key, ok := ThreadKey(target)
	if !ok {
		return nil
	}
	rows, err := c.threads.LoadThreads(accountID, []string{key})
	if err != nil {
		return nil
	}
	row, ok := rows[key]
	if !ok || c.now().After(row.ExpiresAt) {
		return nil
	}

	var thread mtproto.CommentThread
	if err := json.Unmarshal([]byte(row.Payload), &thread); err != nil {
		return nil
	}
	if thread.ThreadID <= 0 {
		return nil
	}
	return &thread
}

// SaveThread 写入线程定位结果；失败只返回错误，由调用方决定是否记日志。
func (c *Cache) SaveThread(accountID int64, target link.Target, thread mtproto.CommentThread) error {
	if thread.ThreadID <= 0 {
		return nil
	}
	key, ok := ThreadKey(target)
	if !ok {
		return nil
	}

	payload, err := json.Marshal(thread)
	if err != nil {
		return err
	}
	now := c.now()
	return c.threads.SaveThreads([]database.ThreadCache{{
		AccountID: accountID,
		LookupKey: key,
		Payload:   string(payload),
		FetchedAt: now,
		ExpiresAt: now.Add(ThreadTTL),
	}})
}

// PruneThreads 清理过期的线程定位结果。
func (c *Cache) PruneThreads(limit int) (int64, error) {
	return c.threads.PruneExpiredThreads(c.now(), limit)
}
