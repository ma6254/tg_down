# tg_down 文档

Telegram 下载工具（Go + React + TypeScript），单用户本地优先，可带 token 暴露到局域网/公网。

| 文档 | 内容 |
|---|---|
| [plan.md](./plan.md) | 选型、架构、目录、数据模型、模块设计、里程碑、风险 |
| [api.md](./api.md) | HTTP API 草案（REST + SSE），含双后端（MTProto / Bot API） |
| [reference.md](./reference.md) | 参考实现 `omni_toolbox` 的复用清单、必须重写项与迁移顺序 |

## 已确认的决策

1. **双后端都要**：MTProto 用户账号（gotd/td）+ Bot API 两种接入方式，统一成 `tgclient` 接口，前后端都按"账号"抽象。
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

M0 脚手架：Go 路由 + SQLite 建表 + Vite 代理 + `go:embed` 打包跑通。见 [plan.md](./plan.md) 里程碑。
