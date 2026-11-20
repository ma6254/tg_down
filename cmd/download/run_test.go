package download

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ma6254/tg_down/internal/link"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// TestWriteText 确认正文首行带标签、后续行缩进，且换行保留。
func TestWriteText(t *testing.T) {
	var buf bytes.Buffer
	writeText(&buf, "第一行\n第二行\r\n")

	got := buf.String()
	if !strings.Contains(got, "文本：第一行") {
		t.Errorf("缺少首行标签: %q", got)
	}
	if !strings.Contains(got, "          第二行") {
		t.Errorf("续行未缩进: %q", got)
	}

	// 空行不应留下只有缩进的空白行。
	buf.Reset()
	writeText(&buf, "第一行\n\n第二行")
	if strings.Contains(buf.String(), "          \n") {
		t.Errorf("空行不应输出缩进: %q", buf.String())
	}
}

// TestWriteTextEmpty 确认空白正文不产生输出。
func TestWriteTextEmpty(t *testing.T) {
	var buf bytes.Buffer
	writeText(&buf, "  \n\t ")
	if buf.Len() != 0 {
		t.Errorf("空白正文不应输出，实际 %q", buf.String())
	}
}

// TestWriteTextComplete 确认正文完整输出、不截断。
func TestWriteTextComplete(t *testing.T) {
	const lines = 12
	var buf bytes.Buffer
	writeText(&buf, strings.Repeat("一行\n", lines))

	got := buf.String()
	if strings.Contains(got, "已省略") {
		t.Errorf("不应出现截断提示: %q", got)
	}
	if count := strings.Count(got, "一行"); count != lines {
		t.Errorf("输出 %d 行，期望 %d 行", count, lines)
	}
}

// TestPrintResultTextOnly 确认纯文本消息也会输出正文与提示。
func TestPrintResultTextOnly(t *testing.T) {
	var buf bytes.Buffer
	printResult(&buf, mtproto.LinkResult{
		Chat: mtproto.Peer{Title: "某讨论组"},
		Text: "这是链接里的文本",
	})

	got := buf.String()
	if !strings.Contains(got, "会话：某讨论组") {
		t.Errorf("缺少会话: %q", got)
	}
	if !strings.Contains(got, "这是链接里的文本") {
		t.Errorf("缺少正文: %q", got)
	}
	if !strings.Contains(got, "没有可下载的媒体") {
		t.Errorf("缺少无媒体提示: %q", got)
	}
}

// TestPrintResultLinks 确认正文内嵌链接会带上可见文字一起输出。
func TestPrintResultLinks(t *testing.T) {
	var buf bytes.Buffer
	printResult(&buf, mtproto.LinkResult{
		Chat:  mtproto.Peer{Title: "某频道"},
		Text:  "第1集 | 第2集",
		Links: []mtproto.MessageLink{{Text: "第1集", URL: "https://t.me/c/1/2?comment=3"}},
	})

	got := buf.String()
	if !strings.Contains(got, "链接（1）") {
		t.Errorf("缺少链接标题: %q", got)
	}
	if !strings.Contains(got, "第1集 → https://t.me/c/1/2?comment=3") {
		t.Errorf("缺少链接内容: %q", got)
	}
}

// TestMediaJobsIncludesExpanded 确认展开出来的媒体也会进入下载任务。
func TestMediaJobsIncludesExpanded(t *testing.T) {
	media := func(name string, downloadable bool) *mtproto.Media {
		return &mtproto.Media{FileName: name, Size: 10, Downloadable: downloadable}
	}

	items := []item{
		{result: mtproto.LinkResult{
			Chat:  mtproto.Peer{Title: "频道"},
			Media: media("post.mp4", true),
		}},
		{result: mtproto.LinkResult{
			Chat:  mtproto.Peer{Title: "讨论组"},
			Media: media("ep1.mp4", true),
		}, expanded: true},
		{result: mtproto.LinkResult{Media: media("big.mp4", false)}},
		{result: mtproto.LinkResult{Err: errors.New("查询失败")}},
	}

	jobs, overLimit := mediaJobs(items)
	if len(jobs) != 2 {
		t.Fatalf("任务数 = %d，期望 2: %+v", len(jobs), jobs)
	}
	if overLimit != 1 {
		t.Errorf("overLimit = %d，期望 1", overLimit)
	}
	if jobs[1].FileName != "ep1.mp4" || jobs[1].ChatTitle != "讨论组" {
		t.Errorf("展开任务不正确: %+v", jobs[1])
	}
}

// TestCollectExpandedTargets 确认去重、跳过非消息链接与失败结果。
func TestCollectExpandedTargets(t *testing.T) {
	input := []link.Target{{Username: "ch", MessageID: 1}}
	results := []mtproto.LinkResult{
		{Links: []mtproto.MessageLink{
			{URL: "https://t.me/ch/1"},     // 与输入重复
			{URL: "https://t.me/ch/2"},     // 保留
			{URL: "https://t.me/ch/2"},     // 重复
			{URL: "https://example.com/x"}, // 非 Telegram 消息链接
			{URL: "https://t.me/ch/3"},     // 保留
		}},
		{Err: errors.New("查询失败"), Links: []mtproto.MessageLink{{URL: "https://t.me/ch/4"}}},
	}

	plan := collectExpandedTargets(results, input)
	if len(plan.Targets) != 2 {
		t.Fatalf("展开目标 = %d 条，期望 2: %+v", len(plan.Targets), plan.Targets)
	}
	if plan.Skipped != 1 {
		t.Errorf("Skipped = %d，期望 1", plan.Skipped)
	}
	if plan.Truncated {
		t.Error("不应触发截断")
	}
	if plan.Targets[0].MessageID != 2 || plan.Targets[1].MessageID != 3 {
		t.Errorf("顺序不正确: %+v", plan.Targets)
	}
}

// TestCollectExpandedTargetsCap 确认超过上限时截断并标记。
func TestCollectExpandedTargetsCap(t *testing.T) {
	links := make([]mtproto.MessageLink, 0, maxExpandLinks+1)
	for i := 0; i <= maxExpandLinks; i++ {
		links = append(links, mtproto.MessageLink{
			URL: fmt.Sprintf("https://t.me/ch/%d", i+1),
		})
	}

	plan := collectExpandedTargets([]mtproto.LinkResult{{Links: links}}, nil)
	if len(plan.Targets) != maxExpandLinks {
		t.Fatalf("展开目标 = %d 条，期望上限 %d", len(plan.Targets), maxExpandLinks)
	}
	if !plan.Truncated {
		t.Error("超过上限时应标记截断")
	}
}

// TestExportLinks 确认只导出可下载媒体、去重、跳过超限与失败项。
func TestExportLinks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "links.txt")
	items := []item{
		{result: mtproto.LinkResult{
			Target: link.Target{Username: "ch", MessageID: 1},
			Media:  &mtproto.Media{Downloadable: true},
		}},
		{result: mtproto.LinkResult{ // 重复
			Target: link.Target{Username: "ch", MessageID: 1},
			Media:  &mtproto.Media{Downloadable: true},
		}},
		{result: mtproto.LinkResult{ // 超过大小上限
			Target: link.Target{Username: "ch", MessageID: 2},
			Media:  &mtproto.Media{Downloadable: false},
		}},
		{result: mtproto.LinkResult{ // 没有媒体
			Target: link.Target{Username: "ch", MessageID: 3},
		}},
		{result: mtproto.LinkResult{ // 查询失败
			Target: link.Target{Username: "ch", MessageID: 4},
			Media:  &mtproto.Media{Downloadable: true},
			Err:    errors.New("查询失败"),
		}},
	}

	count, skipped, err := exportLinks(path, items)
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	if count != 1 || skipped != 1 {
		t.Fatalf("count = %d, skipped = %d，期望 1/1", count, skipped)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取导出文件失败: %v", err)
	}
	if string(content) != "https://t.me/ch/1\n" {
		t.Errorf("导出内容 = %q", string(content))
	}
}
