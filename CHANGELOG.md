# Changelog

## 0.1.3

- 仓库迁移至 `satifanie/cpa-resin-sticky-bind`: registry / 插件元数据 / 文档地址统一
- go module path 更新为 `github.com/satifanie/cpa-resin-sticky-bind`
- LICENSE 保留原作者版权并追加 fork 者

## 0.1.2

- 修复状态页 404: 接受 host 传入的完整 resource 路径
- 同时注册 Resources 与 legacy GET+Menu 路由

## 0.1.1

- 修复: CPA 要求至少一种 capability, 注册空 capabilities 导致 `invalid metadata or no capabilities`
- 声明 `management_api` 并提供状态页 / 手动 sync
- 支持 `socks5h://<token>@host:port` (token 写在 username)

## 0.1.0

- 首个可用版本
- Scheduler 风格周期对账: `host.auth.list` / `get` / `save`
- 完整 `resin_proxy_url` 配置 + `proxy_token_env`
- Account 策略: `auth_id` / `email` / `sub` / `filename`
- Platform: 全局默认 + provider / auth_id 覆盖
- 默认不覆盖已有 `proxy_url`
- token 脱敏日志
- 插件商店 `registry.json` 与多架构 CI 发版
