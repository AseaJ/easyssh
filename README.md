# go-zs — 证书自动化托管工具

[![Go Version](https://img.shields.io/badge/Go-1.25-blue)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
<!-- 仓库创建后取消注释并替换路径:
[![CI](https://github.com/<用户名>/go-zs/actions/workflows/ci.yml/badge.svg)](https://github.com/<用户名>/go-zs/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/<用户名>/go-zs)](https://github.com/<用户名>/go-zs/releases)
-->

go-zs 是一个证书自动化托管工具:自动签发(ACME)、自动续期、自动部署到反向代理(本地/SSH 远程),并提供简洁的桌面仪表盘(GUI)与 headless CLI 双形态。

> ⚠️ 当前为开发版本(0.1.0-dev)。默认连接 Let's Encrypt **staging** 环境,请先跑通后再切换到正式环境。

## 平台支持

| 形态 | Windows | Linux | macOS |
|---|---|---|---|
| CLI(headless) | ✅ | ✅(交叉编译) | ✅(交叉编译) |
| GUI(Wails) | ✅ | ⚠️ 需 webkit2gtk,密钥持久化暂缺 | ⚠️ 需 webkit2gtk |

- Linux/macOS 的 CLI 可在 Windows 上交叉编译(`GOOS=linux GOARCH=amd64 go build ./cmd/go-zs`)
- Linux/macOS GUI 桌面端:密钥持久化(Windows 注册表)在非 Windows 平台暂未实现,密钥需通过环境变量注入

## 功能特性

- **自动签发/续期**:基于 go-acme/lego,支持 Let's Encrypt / ZeroSSL 等 ACME CA
- **双挑战方式**:http-01(80 端口)、dns-01(cloudflare / dnspod / alidns)
- **生命周期状态机**:6h 扫描、剩余 30 天触发续期、失败退避重试(1h/6h/24h/72h)、SAN 变更自动重签
- **自动部署**:
  - `nginx`:原子替换 + `nginx -t` 校验 + reload,失败回滚
  - `ssh`:SFTP 推送到远程 + 远程原子替换 + 远程 reload,失败回滚(密钥认证,支持 ssh-agent)
  - `file`:复制到指定目录
  - `webhook`:推送事件通知
- **幂等可靠**:证书指纹驱动,未变化不部署;部署失败下次扫描自动补部署
- **数据安全**:私钥 0600、密钥引用 `{{env:VAR}}` 不落盘、SSH 加密通道、数据全部本地
- **双形态**:
  - CLI(headless):`serve` 守护进程 / `issue` / `renew` / `list` / `inspect` / `validate-config` / `export` / `import`
  - GUI(Wails):简洁仪表盘,概览卡片 + 三色证书列表 + 一键续期/部署 + 导出/导入
- **配置迁移**:`export` / `import` 打包配置与可选密钥/证书产物为 `.zsbundle`(口令加密),多客户端共同管理同一套证书
- **告警**:签发/部署失败推送 webhook(可配置)

## 架构

```
┌──────────────────────────────────────────────┐
│  GUI (Wails 桌面端) / CLI (headless)          │
└──────────────────┬───────────────────────────┘
                   ▼
│  core (纯 Go)                                │
│  config → certmgr(状态机) → scheduler(调度)    │
│  → acme(签发) → storage(存储) → deploy(部署)   │
│  → notify(告警)                                │
└──────────────────────────────────────────────┘
```

## 构建

环境要求:Go 1.25+;GUI 另需 Node.js 18+、Wails v2 依赖(Windows: WebView2;Linux: webkit2gtk)。

```bash
# CLI
go build -o go-zs.exe ./cmd/go-zs

# GUI(桌面应用)
cd cmd/go-zs-app
wails build   # 产出 build/bin/go-zs(.exe)
```

## 快速开始

```bash
# 1. 准备配置
cp go-zs.yaml.example go-zs.yaml
#    编辑 go-zs.yaml:CA、邮箱、域名、挑战方式、部署目标

# 2. 校验配置(注意:默认 staging 环境)
go-zs validate-config -c go-zs.yaml

# 3. 手动签发一次(建议先对 staging 验证流程)
go-zs issue <name> -c go-zs.yaml

# 4. 查看状态
go-zs list -c go-zs.yaml
go-zs inspect <name> -c go-zs.yaml

# 5. 守护进程模式(常驻:扫描/续期/部署)
go-zs serve -c go-zs.yaml --pidfile go-zs.pid

# 6. 手动强制续期 / 启动桌面应用
go-zs renew <name> -c go-zs.yaml
go-zs-app          # GUI
```

## 导出 / 导入(多客户端共用配置)

`.zsbundle` 单文件包,按**范围分级**导出,敏感数据用口令加密(AES-256-GCM + scrypt 派生)。

| 范围 | 内容 | 口令 |
|---|---|---|
| `config` | 配置本身(密钥保持 `{{env:VAR}}` 引用) | 不需要 |
| `secrets` | 环境变量密钥明文 + ACME 账号密钥 | 必须 |
| `certs` | 证书产物(cert/fullchain/chain/privkey) | 必须 |
| `ssh-keys` | SSH 私钥内容 | 必须 |

```bash
# 导出:仅配置(可安全分发)
go-zs export -c go-zs.yaml -o cfg.zsbundle --scope config

# 导出:配置 + 加密密钥(口令至少 10 字符,非纯数字)
go-zs export -c go-zs.yaml -o full.zsbundle --scope config,secrets,certs,ssh-keys --password 'StrongPass!42'

# 导入(先预览,再确认;冲突策略 append=改名追加 / skip=跳过 / overwrite=覆盖)
go-zs import -f full.zsbundle -c go-zs.yaml --conflict append --password 'StrongPass!42'
```

注意:
- **导入不含 SSH 私钥时,新客户端无法 SSH 部署**(SSH 靠本机私钥 + 远程 `authorized_keys` 公钥配对),需手动配密钥;导入会给出提示。
- 相对路径(`storage.dir`、`key`、`account_key`)在导入端按目标配置目录重新解析。
- 导入不会迁移运行时状态(`meta.json` 的部署指纹/失败计数),首次扫描自动按幂等机制补部署。

## 配置说明

```yaml
ca:
  server: https://acme-staging-v02.api.letsencrypt.org/directory  # 跑通后切正式
  email: ops@example.com
  account_key: ./data/account.key    # 首次运行自动生成

certificates:
  - name: example
    domains: [example.com, www.example.com]
    challenge: http-01               # 或 dns-01
    # challenge: dns-01
    # dns_provider: dnspod           # cloudflare / dnspod / alidns
    # dns_provider_opts:
    #   api_token: "{{env:DNSPOD_TOKEN}}"   # 密钥只引用环境变量
    storage:
      dir: ./certs/example           # 产物:cert/fullchain/chain/privkey/meta.json
    deploy:
      - type: nginx
        reload_cmd: nginx -s reload
        # cert_path: /etc/nginx/ssl/fullchain.pem   # 可选:复制到指定路径
        # key_path: /etc/nginx/ssl/privkey.pem
      - type: ssh
        host: 203.0.113.10
        port: 22
        user: deploy
        key: ~/.ssh/id_ed25519
        known_hosts: ./known_hosts   # 必需!远程主机指纹,未配置则拒绝连接
        remote_path: /etc/nginx/ssl/example/
        reload_cmd: nginx -t && nginx -s reload
        # cert_filename: fullchain.pem   # 可选:远程证书文件名(默认 fullchain.pem)
        # key_filename: privkey.pem      # 可选:远程私钥文件名(默认 privkey.pem)

schedule:
  check_interval: 6h
  renew_before: 30d
  retry_backoff: [1h, 6h, 24h, 72h]

notify:
  # webhook: https://ops.example.com/alert   # 签发/部署失败推送
```

存储目录产物:

```
certs/example/
├── cert.pem        # 仅 leaf
├── fullchain.pem   # leaf + intermediate(反代通用)
├── chain.pem       # 仅 intermediate
├── privkey.pem     # 0600
└── meta.json       # 状态(到期时间/指纹/失败计数/部署记录)
```

## 安全说明

- 开源的是**代码**,不是数据:证书、私钥、账号密钥全部留在使用者本机
- 私钥落盘权限 0600;`{{env:VAR}}` 引用密钥,配置文件中不出现明文
- SSH 部署走加密通道,私钥传输不落明文临时文件;**必须配置 `known_hosts`**,未配置时拒绝连接(防中间人):`ssh-keyscan <host> >> known_hosts`
- 生产使用前务必切换到正式 CA,并在真实域名上验证
- **导出包**含密钥/私钥时用口令加密(AES-256-GCM,scrypt 派生);口令即唯一防线,丢失无法恢复。含敏感数据的 `.zsbundle` 请经安全渠道(加密传输/当面拷贝)传递,切勿明文放网盘/邮件

## 演进路线

- [x] MVP:签发(http-01/dns-01)+ 调度 + 存储 + nginx/ssh/file 部署 + CLI + GUI 仪表盘 + webhook 告警
- [ ] v2:tencent-cloud provisioner、webhook 部署类型、私钥加密存储(AES-GCM/keyring)
- [ ] v3:多 CA、KMS、OCSP stapling、HTTP API server

## 许可证

MIT
