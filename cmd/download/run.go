package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	accounts "github.com/ma6254/tg_down/internal/account"
	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/link"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
	"github.com/ma6254/tg_down/internal/utils"
)

// resolveTimeout 给批量查询留出余量：建立连接 + 每条消息一次历史请求。
func resolveTimeout(count int) time.Duration {
	return 60*time.Second + time.Duration(count)*5*time.Second
}

// maxExpandLinks 是一次运行最多展开的正文链接数，避免索引帖把账号送进 FloodWait。
const maxExpandLinks = 200

// item 是一条待展示的结果；expanded 表示它来自正文链接展开。
type item struct {
	result   mtproto.LinkResult
	expanded bool
}

// runDownload 解析链接、查询消息并按文件展示；下载执行在 R2 接入。
func runDownload(cmd *cobra.Command, opts *options) error {
	out := cmd.OutOrStdout()

	parsed := link.ParseAll(opts.URLs)
	fmt.Fprintf(out, "\n解析 %d 条链接：\n", len(parsed))

	targets := make([]link.Target, 0, len(parsed))
	parseFailed := 0
	for i, result := range parsed {
		fmt.Fprintf(out, "%2d. %s\n", i+1, result.Raw)
		if result.Err != nil {
			parseFailed++
			fmt.Fprintf(out, "    解析失败：%v\n", result.Err)
			continue
		}
		fmt.Fprintf(out, "    %s\n", describeTarget(result.Target))
		targets = append(targets, result.Target)
	}
	if len(targets) == 0 {
		return fmt.Errorf("%d 条链接无法解析", parseFailed)
	}

	session, err := currentSession()
	if err != nil {
		return err
	}

	resolve := func(batch []link.Target) ([]mtproto.LinkResult, error) {
		fmt.Fprintf(out, "\n连接 Telegram 查询 %d 条消息…\n", len(batch))
		ctx, cancel := context.WithTimeout(cmd.Context(), resolveTimeout(len(batch)))
		defer cancel()
		return session.ResolveLinks(ctx, batch)
	}

	results, err := resolve(targets)
	if err != nil {
		return err
	}

	items := make([]item, 0, len(results))
	for _, result := range results {
		items = append(items, item{result: result})
	}

	expandedCount := 0
	if opts.ExpandTextLinks {
		plan := collectExpandedTargets(results, targets)
		if plan.Skipped > 0 {
			fmt.Fprintf(out, "（正文里有 %d 条链接不是消息链接，已跳过）\n", plan.Skipped)
		}
		if plan.Truncated {
			fmt.Fprintf(out, "（展开数量超过上限 %d 条，已截断）\n", maxExpandLinks)
		}

		if len(plan.Targets) == 0 {
			fmt.Fprintln(out, "（正文里没有可展开的消息链接）")
		} else {
			fmt.Fprintf(out, "\n展开正文中的 %d 条链接…\n", len(plan.Targets))
			childResults, err := resolve(plan.Targets)
			if err != nil {
				return err
			}
			for _, result := range childResults {
				items = append(items, item{result: result, expanded: true})
			}
			expandedCount = len(childResults)
		}
	}

	ok, empty, failed := 0, 0, 0
	fmt.Fprintln(out, "\n消息内容：")
	for i, it := range items {
		result := it.result
		marker := ""
		if it.expanded {
			marker = " [展开]"
		}
		fmt.Fprintf(out, "%2d.%s %s\n", i+1, marker, result.Target.Raw)
		if result.Err != nil {
			failed++
			fmt.Fprintf(out, "    查询失败：%v\n", result.Err)
			continue
		}
		ok++
		if result.Media == nil {
			empty++
		}
		printResult(out, result)
	}

	fmt.Fprintf(out, "\n共 %d 条", len(items))
	if expandedCount > 0 {
		fmt.Fprintf(out, "（含展开 %d 条）", expandedCount)
	}
	fmt.Fprintf(out, "：成功 %d", ok)
	if empty > 0 {
		fmt.Fprintf(out, "（其中 %d 条没有可下载的媒体）", empty)
	}
	fmt.Fprintf(out, "，失败 %d", failed)
	if parseFailed > 0 {
		fmt.Fprintf(out, "；另有 %d 条链接无法解析", parseFailed)
	}
	fmt.Fprintln(out, "。")

	if opts.ExportLinks != "" {
		count, skipped, err := exportLinks(opts.ExportLinks, items)
		if err != nil {
			return err
		}
		if count == 0 {
			fmt.Fprintf(out, "\n已创建空文件 %s：没有可导出的链接。\n", opts.ExportLinks)
		} else {
			fmt.Fprintf(out, "\n已导出 %d 条链接到 %s。\n", count, opts.ExportLinks)
		}
		if skipped > 0 {
			fmt.Fprintf(out, "（另有 %d 条超过单文件上限，未导出）\n", skipped)
		}
	}

	jobs, overLimit := mediaJobs(items)
	if opts.DryRun {
		if len(jobs) > 0 {
			fmt.Fprintf(out, "\n--dry-run：跳过下载 %d 个文件。\n", len(jobs))
		}
		return previewError(failed, parseFailed)
	}
	if len(jobs) == 0 {
		fmt.Fprintln(out, "\n没有可下载的媒体文件。")
		return previewError(failed, parseFailed)
	}

	dir := "downloads"
	if opts.defaults.DownloadDir != nil {
		if value := strings.TrimSpace(opts.defaults.DownloadDir()); value != "" {
			dir = value
		}
	}
	concurrency := 1
	if opts.defaults.MaxConcurrency != nil {
		concurrency = opts.defaults.MaxConcurrency()
	}
	if concurrency < 1 || concurrency > 8 {
		concurrency = 1
	}
	if opts.Threads < 1 || opts.Threads > 8 {
		return fmt.Errorf("--threads 需在 1-8 之间")
	}

	fmt.Fprintf(out, "\n开始下载 %d 个文件到 %s（并发 %d，单文件线程 %d）…\n",
		len(jobs), dir, concurrency, opts.Threads)
	if overLimit > 0 {
		fmt.Fprintf(out, "（另有 %d 个文件超过单文件上限，已跳过）\n", overLimit)
	}

	var totalBytes int64
	for _, job := range jobs {
		totalBytes += job.Size
	}
	progress := newDownloadProgress(totalBytes, len(jobs), concurrency)
	defer progress.close()

	downloadResults, err := session.DownloadMedia(cmd.Context(), jobs, mtproto.DownloadOptions{
		RootDir:     dir,
		Concurrency: concurrency,
		Threads:     opts.Threads,
		Force:       opts.Force,
		Flat:        opts.Flat,
		OnStart:     progress.start,
		OnProgress:  progress.tick,
		OnDone:      progress.done,
		OnRetry:     progress.retry,
	})
	if err != nil {
		progress.abort()
		return err
	}
	progress.finish()

	downloaded, skippedFiles, failedFiles := printDownloadResults(out, downloadResults)
	fmt.Fprintf(out, "\n下载结果：成功 %d，跳过 %d，失败 %d。\n",
		downloaded, skippedFiles, failedFiles)

	if failedFiles > 0 {
		return fmt.Errorf("%d 个文件下载失败", failedFiles)
	}
	return previewError(failed, parseFailed)
}

// mediaJobs 从展示项里挑出可下载的媒体（含 --expand-text-links 展开出来的消息）；
// 超过单文件上限的单独计数。
func mediaJobs(items []item) ([]mtproto.MediaJob, int) {
	jobs := make([]mtproto.MediaJob, 0, len(items))
	overLimit := 0
	for _, it := range items {
		result := it.result
		if result.Err != nil || result.Media == nil {
			continue
		}
		if !result.Media.Downloadable {
			overLimit++
			continue
		}
		jobs = append(jobs, mtproto.MediaJob{
			ChatTitle: result.Chat.Title,
			FileName:  result.Media.FileName,
			Size:      result.Media.Size,
			Media:     result.Media,
		})
	}
	return jobs, overLimit
}

// printDownloadResults 输出每个文件的最终状态并返回计数。
func printDownloadResults(out io.Writer, results []mtproto.DownloadResult) (ok, skipped, failed int) {
	fmt.Fprintln(out, "\n下载明细：")
	for _, result := range results {
		switch {
		case result.Err != nil:
			failed++
			fmt.Fprintf(out, "  [失败] %s：%v\n", result.FileName, result.Err)
		case result.Skipped:
			skipped++
			fmt.Fprintf(out, "  [跳过] %s（已存在）\n", result.FileName)
		case result.Resumed:
			ok++
			fmt.Fprintf(out, "  [续传完成] %s → %s\n", result.FileName, result.Path)
		default:
			ok++
			fmt.Fprintf(out, "  [完成] %s → %s\n", result.FileName, result.Path)
		}
	}
	return ok, skipped, failed
}

// previewError 把预览阶段的失败汇总成退出错误；没有失败时返回 nil。
func previewError(failed, parseFailed int) error {
	if failed+parseFailed == 0 {
		return nil
	}
	return fmt.Errorf("%d 条链接处理失败", failed+parseFailed)
}

// expandPlan 是正文链接展开计划；策略实现在 mtproto，CLI 与 HTTP 接口共用。
type expandPlan = mtproto.ExpandPlan

// collectExpandedTargets 按统一策略收集可展开目标，上限由 CLI 决定。
func collectExpandedTargets(results []mtproto.LinkResult, input []link.Target) expandPlan {
	return mtproto.PlanLinkExpansion(results, input, maxExpandLinks)
}

// exportLinks 把有可下载媒体的消息链接写入 txt，每行一条，按 Target.Key 去重。
// 返回写入条数与因超过单文件上限被跳过的条数。
func exportLinks(path string, items []item) (int, int, error) {
	seen := make(map[string]struct{}, len(items))
	lines := make([]string, 0, len(items))
	skipped := 0

	for _, it := range items {
		result := it.result
		if result.Err != nil || result.Media == nil {
			continue
		}
		if !result.Media.Downloadable {
			skipped++
			continue
		}

		key := result.Target.Key()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		lines = append(lines, result.Target.URL())
	}

	var content []byte
	if len(lines) > 0 {
		content = []byte(strings.Join(lines, "\n") + "\n")
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return 0, skipped, fmt.Errorf("导出链接到 %s: %w", path, err)
	}
	return len(lines), skipped, nil
}

// printResult 输出一条消息的会话、文件与正文。
func printResult(out io.Writer, result mtproto.LinkResult) {
	fmt.Fprintf(out, "    会话：%s\n", result.Chat.Title)

	media := result.Media
	if media == nil {
		writeText(out, result.Text)
		printLinks(out, result.Links)
		fmt.Fprintln(out, "    （该消息没有可下载的媒体）")
		return
	}

	fmt.Fprintf(out, "    文件：%s\n", media.FileName)
	fmt.Fprintf(out, "    大小：%s（账号上限 %s）\n",
		utils.FormatBytes(media.Size), utils.FormatBytes(media.MaxSize))

	kind := mediaKindLabel(media.Kind)
	if media.MIME != "" {
		fmt.Fprintf(out, "    类型：%s · %s\n", kind, media.MIME)
	} else {
		fmt.Fprintf(out, "    类型：%s\n", kind)
	}
	if media.Duration > 0 {
		fmt.Fprintf(out, "    时长：%s\n", formatDuration(media.Duration))
	}
	if media.Width > 0 && media.Height > 0 {
		fmt.Fprintf(out, "    尺寸：%d × %d\n", media.Width, media.Height)
	}
	if media.GroupedID != 0 {
		fmt.Fprintf(out, "    相册：分组 %d（整组下载待实现）\n", media.GroupedID)
	}
	if !media.Downloadable {
		fmt.Fprintln(out, "    提示：超过单文件上限，无法下载")
	}

	writeText(out, result.Text)
	printLinks(out, result.Links)
}

// printLinks 输出正文里内嵌的链接：文字标签与目标地址不同时两个都显示。
func printLinks(out io.Writer, links []mtproto.MessageLink) {
	if len(links) == 0 {
		return
	}

	fmt.Fprintf(out, "    链接（%d）：\n", len(links))
	for _, item := range links {
		if item.Text != "" && item.Text != item.URL {
			fmt.Fprintf(out, "      · %s → %s\n", item.Text, item.URL)
			continue
		}
		fmt.Fprintf(out, "      · %s\n", item.URL)
	}
}

// writeText 输出消息正文：保留换行并统一缩进，不截断。
func writeText(out io.Writer, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			fmt.Fprintln(out)
			continue
		}
		prefix := "          "
		if i == 0 {
			prefix = "    文本："
		}
		fmt.Fprintf(out, "%s%s\n", prefix, line)
	}
}

// currentSession 读取当前账号的 session 与代理，组装 MTProto 连接参数。
func currentSession() (mtproto.Session, error) {
	db, err := utils.OpenDatabase()
	if err != nil {
		return mtproto.Session{}, err
	}
	defer db.Close()

	service := accounts.New(db)
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
		return mtproto.Session{}, fmt.Errorf("账号 #%d 是 %s，按链接查询暂只支持 mtproto", id, account.Kind)
	}
	if strings.TrimSpace(account.SessionPath) == "" {
		return mtproto.Session{}, fmt.Errorf("账号 #%d 没有 session 文件，请先执行 account login", id)
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

// describeTarget 把解析结果压成一行，方便肉眼核对。
func describeTarget(target link.Target) string {
	peer := "@" + target.Username
	if target.Username == "" {
		peer = fmt.Sprintf("频道 %d", target.ChannelID)
	}

	parts := []string{fmt.Sprintf("消息 %d", target.MessageID)}
	if target.TopicID > 0 {
		parts = append(parts, fmt.Sprintf("话题 %d", target.TopicID))
	}
	if target.CommentID > 0 {
		parts = append(parts, fmt.Sprintf("评论 %d", target.CommentID))
	}
	return peer + " · " + strings.Join(parts, " · ")
}

// mediaKindLabel 返回中文媒体类型。
func mediaKindLabel(kind mtproto.MediaKind) string {
	switch kind {
	case mtproto.MediaKindPhoto:
		return "图片"
	case mtproto.MediaKindVideo:
		return "视频"
	case mtproto.MediaKindAudio:
		return "音频"
	default:
		return "文件"
	}
}

// formatDuration 把秒数格式化成 mm:ss 或 h:mm:ss。
func formatDuration(seconds int) string {
	if seconds < 3600 {
		return fmt.Sprintf("%02d:%02d", seconds/60, seconds%60)
	}
	return fmt.Sprintf("%d:%02d:%02d", seconds/3600, (seconds%3600)/60, seconds%60)
}
