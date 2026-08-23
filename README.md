# cpa-resin-sticky-bind

CLIProxyAPI 原生插件: 为每份凭证绑定稳定的 Resin sticky 代理 Account, 写入凭证级 `proxy_url`.

| 项 | 值 |
|----|----|
| 插件 ID | `cpa-resin-sticky-bind` |
| 版本 | `0.1.3` |
| 协议 | MIT |
| Fork 自 | [ArchmageTony/cpa-resin-sticky-bind](https://github.com/ArchmageTony/cpa-resin-sticky-bind) |

## 行为

1. 启动后与固定间隔枚举 CPA 凭证 (`host.auth.list`)
2. 对可持久化且 `proxy_url` 为空的凭证生成 Account
3. 写入:

```text
socks5h://<Platform>.<Account>:<token>@resin:2260
```

4. 默认不覆盖已有 `proxy_url`
5. 日志中 token 始终脱敏

## 安装 (CPA 插件商店)

```yaml
plugins:
  enabled: true
  dir: "plugins"
  store-sources:
    - "https://raw.githubusercontent.com/satifanie/cpa-resin-sticky-bind/main/registry.json"
  configs:
    cpa-resin-sticky-bind:
      enabled: true
      resin_proxy_url: "socks5h://resin:2260"
      proxy_token_env: "RESIN_PROXY_TOKEN"
      default_platform: "default"
      account_strategy: "auth_id"
      sync_interval_seconds: 30
      only_if_empty: true
      overwrite_existing: false
```

1. 将上述配置写入 CPA `config.yaml` 并重启
2. 确保 CPA 容器能访问 GitHub Release (或改用手动安装)
3. 管理中心 → 插件商店 → 安装 `CPA Resin Sticky Bind`
4. 向 CPA 注入 `RESIN_PROXY_TOKEN` (与 Resin 同值), 或在 `resin_proxy_url` 中写 password

## 手动安装

1. 从 Release 下载 `cpa-resin-sticky-bind_{version}_{goos}_{goarch}.zip`
2. 解压后将库文件放到:

```text
plugins/linux/amd64/cpa-resin-sticky-bind.so
```

3. 合并配置并重启 CPA

## 配置说明

| 字段 | 说明 |
|------|------|
| `resin_proxy_url` | 完整代理入口, 内网示例 `socks5h://resin:2260` |
| `proxy_token_env` | URL 无 password 时读取的环境变量名 |
| `default_platform` | 默认 Platform |
| `platform_by_provider` | 按 provider 覆盖 Platform |
| `platform_by_auth_id` | 按 auth id 覆盖 Platform (最高优先级) |
| `account_strategy` | `auth_id` / `email` / `sub` / `filename` |
| `only_if_empty` | 仅空 `proxy_url` 写入 |
| `overwrite_existing` | 是否允许覆盖已有 `proxy_url` |

不需要配置 CPA Management 地址. 凭证读写走进程内 Host Callback.

## 本地开发

```bash
git clone --depth 1 https://github.com/router-for-me/CLIProxyAPI ./CLIProxyAPI-src
# go.mod 已含 replace => ./CLIProxyAPI-src

go test ./internal/stickybind/ -count=1
CGO_ENABLED=1 go build -buildmode=c-shared -o bin/cpa-resin-sticky-bind.so .
```

## 安全

- 禁止提交真实 `RESIN_PROXY_TOKEN` / 含 password 的生产配置
- 日志与错误输出不打印 token
- 示例配置仅使用占位符
