package mtproto

import "github.com/ma6254/tg_down/internal/link"

// ExpandPlan 是消息正文链接的展开计划。
type ExpandPlan struct {
	Targets   []link.Target
	Skipped   int  // 不是 Telegram 消息链接的条数
	Truncated bool // 是否因超过上限被截断
}

// PlanLinkExpansion 从已解析结果里收集正文链接的展开目标：
// 跳过非消息链接与已出现过的链接（含输入本身），并限制总量。
//
// CLI 与 HTTP 接口共用这套策略，保证「只展开一层」的语义一致。
func PlanLinkExpansion(results []LinkResult, input []link.Target, max int) ExpandPlan {
	seen := make(map[string]struct{}, len(input))
	for _, target := range input {
		seen[target.Key()] = struct{}{}
	}

	var plan ExpandPlan
	for _, result := range results {
		if result.Err != nil {
			continue
		}
		for _, messageLink := range result.Links {
			target, err := link.Parse(messageLink.URL)
			if err != nil {
				plan.Skipped++
				continue
			}

			key := target.Key()
			if _, ok := seen[key]; ok {
				continue
			}
			if max > 0 && len(plan.Targets) >= max {
				plan.Truncated = true
				continue
			}

			seen[key] = struct{}{}
			plan.Targets = append(plan.Targets, target)
		}
	}
	return plan
}
