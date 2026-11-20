// Package link 解析 Telegram 消息链接。
//
// 只做纯字符串解析、不访问网络：把一条链接拆成「目标会话 + 消息 ID」，
// 供下载流程后续解析 peer 与读取消息使用。评论链接（?comment=）只记录评论
// 消息 ID；真正定位到讨论组需要联网查频道的 linked chat，这一步不在本包内。
package link

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Target 是一条消息链接解析后的定位信息。
//
// Username 与 ChannelID 互斥：公开会话用用户名定位，私有频道用 t.me/c/ 里的裸 ID。
type Target struct {
	Raw       string // 原始输入（已去除首尾空白），便于回显
	Username  string // 公开会话用户名，小写、不含 @
	ChannelID int64  // 私有频道的裸 ID，不含 -100 前缀
	MessageID int    // 帖子 / 消息 ID
	TopicID   int    // 论坛话题 ID，0 表示不是话题链接
	CommentID int    // 评论消息 ID（?comment=），0 表示直接指向帖子
}

// IsComment 表示链接指向频道帖子下的评论，而不是帖子本身。
func (t Target) IsComment() bool { return t.CommentID > 0 }

// IsPrivate 表示链接用 t.me/c/ 的裸频道 ID 定位，而不是公开用户名。
func (t Target) IsPrivate() bool { return t.ChannelID > 0 }

// Key 返回用于去重的稳定标识：同一条消息的评论链接与帖子链接互不冲突，
// 话题路径不影响目标（同一条消息可能有多种链接写法）。
func (t Target) Key() string {
	peer := "u:" + t.Username
	if t.Username == "" {
		peer = "c:" + strconv.FormatInt(t.ChannelID, 10)
	}
	return peer + ":" + strconv.Itoa(t.MessageID) + ":" + strconv.Itoa(t.CommentID)
}

// URL 返回这条目标的规范链接，便于写回文件后再次使用。
//
// 话题路径会被省略：消息 ID 已经能定位，评论链接保留 ?comment=。
func (t Target) URL() string {
	var b strings.Builder
	b.WriteString("https://t.me/")
	if t.Username != "" {
		b.WriteString(t.Username)
	} else {
		b.WriteString("c/")
		b.WriteString(strconv.FormatInt(t.ChannelID, 10))
	}
	b.WriteString("/")
	b.WriteString(strconv.Itoa(t.MessageID))
	if t.CommentID > 0 {
		b.WriteString("?comment=")
		b.WriteString(strconv.Itoa(t.CommentID))
	}
	return b.String()
}

// usernameRE 是公开用户名的保守校验：字母开头，后接字母/数字/下划线。
// Telegram 对用户名还有长度与保留名限制，这里只拦明显不是用户名的输入。
var usernameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,31}$`)

// Parse 解析一条 Telegram 消息链接。
//
// 支持的形态：
//
//	https://t.me/<用户名>/<消息ID>
//	https://t.me/<用户名>/<消息ID>?comment=<评论ID>
//	https://t.me/<用户名>/<话题ID>/<消息ID>
//	https://t.me/c/<频道ID>/<消息ID>
//	https://t.me/c/<频道ID>/<话题ID>/<消息ID>
//
// 同时接受 http://、省略协议、telegram.me / telegram.dog 域名，以及网页预览的
// /s/ 前缀。返回的错误都是可直接展示给用户的中文说明。
func Parse(raw string) (Target, error) {
	target := Target{Raw: strings.TrimSpace(raw)}
	if target.Raw == "" {
		return Target{}, fmt.Errorf("链接为空")
	}

	u, err := normalizeURL(target.Raw)
	if err != nil {
		return Target{}, err
	}

	segments := splitPath(u.Path)
	if len(segments) == 0 {
		return Target{}, fmt.Errorf("链接里没有消息路径：%s", target.Raw)
	}

	// 网页预览地址 t.me/s/<用户名>/<消息ID>，s 只是路径前缀。
	if len(segments) > 1 && segments[0] == "s" {
		segments = segments[1:]
	}

	// 邀请链接只有会话、没有消息，无法定位要下载的文件。
	if segments[0] == "joinchat" || strings.HasPrefix(segments[0], "+") {
		return Target{}, fmt.Errorf("邀请链接不包含消息，无法定位要下载的文件")
	}

	var messageID string
	if segments[0] == "c" {
		if len(segments) != 3 && len(segments) != 4 {
			return Target{}, fmt.Errorf("频道链接格式应为 t.me/c/<频道ID>/<消息ID> 或 t.me/c/<频道ID>/<话题ID>/<消息ID>")
		}
		if target.ChannelID, err = parseChannelID(segments[1]); err != nil {
			return Target{}, err
		}
		if len(segments) == 4 {
			if target.TopicID, err = parseID(segments[2], "话题 ID"); err != nil {
				return Target{}, err
			}
			messageID = segments[3]
		} else {
			messageID = segments[2]
		}
	} else {
		if len(segments) != 2 && len(segments) != 3 {
			return Target{}, fmt.Errorf("消息链接格式应为 t.me/<用户名>/<消息ID>")
		}
		username := segments[0]
		if !usernameRE.MatchString(username) {
			return Target{}, fmt.Errorf("用户名 %q 不合法；私有频道请用 t.me/c/<频道ID>/<消息ID>", username)
		}
		target.Username = strings.ToLower(username)
		if len(segments) == 3 {
			if target.TopicID, err = parseID(segments[1], "话题 ID"); err != nil {
				return Target{}, err
			}
			messageID = segments[2]
		} else {
			messageID = segments[1]
		}
	}

	if target.MessageID, err = parseID(messageID, "消息 ID"); err != nil {
		return Target{}, err
	}

	query := u.Query()
	if comment := strings.TrimSpace(query.Get("comment")); comment != "" {
		if target.CommentID, err = parseID(comment, "评论消息 ID"); err != nil {
			return Target{}, err
		}
	}
	// 话题既能出现在路径里（/用户/话题/消息），也能出现在查询串里（?thread=）。
	// 路径优先；两者同时出现时 thread 会被忽略。
	if target.TopicID == 0 {
		if thread := strings.TrimSpace(query.Get("thread")); thread != "" {
			if target.TopicID, err = parseID(thread, "话题 ID"); err != nil {
				return Target{}, err
			}
		}
	}

	return target, nil
}

// Result 是批量解析中一条链接的结果。
type Result struct {
	Raw    string
	Target Target
	Err    error
}

// ParseAll 逐条解析，单条失败不影响其它链接，返回顺序与输入一致。
func ParseAll(raws []string) []Result {
	results := make([]Result, 0, len(raws))
	for _, raw := range raws {
		target, err := Parse(raw)
		results = append(results, Result{
			Raw:    strings.TrimSpace(raw),
			Target: target,
			Err:    err,
		})
	}
	return results
}

// normalizeURL 补全协议、校验域名，返回可用于读取路径与查询串的 URL。
func normalizeURL(raw string) (*url.URL, error) {
	s := raw
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("链接格式不合法: %w", err)
	}

	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	case "tg":
		return nil, fmt.Errorf("暂不支持 tg:// 链接，请改用 https://t.me/... 形式")
	default:
		return nil, fmt.Errorf("不支持的链接协议 %q", u.Scheme)
	}

	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	switch host {
	case "t.me", "telegram.me", "telegram.dog":
	default:
		return nil, fmt.Errorf("不是 Telegram 消息链接：%s", u.Host)
	}
	return u, nil
}

// splitPath 按 / 切分并丢掉空段，兼容结尾斜杠与重复斜杠。
func splitPath(path string) []string {
	parts := strings.Split(path, "/")
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			segments = append(segments, part)
		}
	}
	return segments
}

// parseID 解析消息 / 话题 / 评论 ID：必须是正整数且放得进 int32。
func parseID(raw, what string) (int, error) {
	s := strings.TrimSpace(raw)
	id, err := strconv.ParseInt(s, 10, 32)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%s 不合法：%q（应为正整数）", what, raw)
	}
	return int(id), nil
}

// parseChannelID 解析 t.me/c/ 后面的频道 ID，它比消息 ID 宽，保留在 int64。
func parseChannelID(raw string) (int64, error) {
	s := strings.TrimSpace(raw)
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("频道 ID 不合法：%q（t.me/c/ 后面应为纯数字）", raw)
	}
	return id, nil
}
