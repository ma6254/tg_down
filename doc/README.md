# tg_down 文档

Telegram 下载工具（Go + React + TypeScript），单用户本地优先，可带 token 暴露到局域网/公网。

| 文档 | 内容 |
|---|---|
| [plan.md](./plan.md) | 按「现状 — 规划 — 路线图 — 风险」组织的完整规划，含实现状态标注 |
| [api.md](./api.md) | HTTP API 契约（REST + SSE），逐节标注「已实现 / 规划中」 |
| [reference.md](./reference.md) | 参考实现 `omni_toolbox` 的复用清单与迁移顺序（历史参考，路径以实际代码为准） |

## 当前状态（2026-11-22）

- **已实现**：MTProto 账号（CLI 登录、tdata 探测导入、切换、详情、在线资料、头像、登出/删除）、配置读写、
  链接解析与预览、下载任务（队列、`.part` 续传、暂停/继续/重试、SSE 进度）、内嵌 Web 前端。
- **未实现**：Bot API 接入、会话/消息浏览、HTTP 手机号登录三步、`FLOOD_WAIT` 自动挂起恢复、
  下载后大小/哈希校验。
- **规划中**：会话界面（群 / 频道 / 私聊 / 收藏夹，仿 Telegram Desktop，见 [plan.md §3.1](./plan.md#31-会话界面仿-telegram-desktop规划)）；
  群 / 频道监控（检测新消息并按过滤条件自动下载，随 `tg_down serve` 常驻运行，见 [plan.md §3.2](./plan.md#32-群--频道监控规划)）；
  RSS 输出（把监控内容发布为 RSS 2.0（Media RSS）feed，见 [plan.md §3.3](./plan.md#33-rss-输出服务规划)）；
  下载页输入草稿（含上次解析摘要缓存，存数据库，见 [plan.md §3.4](./plan.md#34-下载页输入草稿规划)）；
  下载输入历史（最近解析过的批次，见 [plan.md §3.5](./plan.md#35-下载输入历史规划)）。
- **下一步**：先做会话界面（M2a–M2c）；随后按需补 Bot API、群 / 频道监控与 RSS 输出；详见 [plan.md](./plan.md)。

## 已确认的决策

1. **双后端都要**：MTProto 用户账号（gotd/td）+ Bot API 两种接入方式，统一成"账号"抽象，前后端都按"账号"抽象（Bot API 尚未实现）。
2. **单用户**：不做多租户、不做注册；默认只监听 `127.0.0.1`，对外暴露时启用 `auth.token`。
3. **前端**：React + TypeScript + Vite + Tailwind CSS。

## 目录约定

```
tg_down/
  tg_down_server/   # Go 后端
    doc/            # 本文档目录
  tg_down_web/      # React 前端
```

## 下一步

先做会话界面（M2a–M2c：读取接口 + 仿 Telegram Desktop 的列表/消息流/媒体与批量下载）；
之后按需进入群 / 频道监控与 RSS 输出（[plan.md §3.2](./plan.md#32-群--频道监控规划)、[§3.3](./plan.md#33-rss-输出服务规划)）。
下载页输入草稿（[§3.4](./plan.md#34-下载页输入草稿规划)）是独立小改进，可与任一阶段并行。
在此之前，Web 端只提供链接解析与下载。
具体差异与剩余项见 [plan.md](./plan.md) 与 [api.md](./api.md) 的「规划中」章节。
