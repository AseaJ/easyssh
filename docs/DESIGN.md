# go-zs 设计文档

> 证书自动化托管工具(桌面端 + CLI)
> 状态:方案设计定稿(实现完成度见文末"实现状态")

## 0. 实现状态

| 模块 | 状态 |
|---|---|
| config(分层配置/校验/env 引用/热加载) | ✅ 已实现 |
| certmgr(生命周期状态机/退避/SAN 检测) | ✅ 已实现 |
| scheduler(扫描/限流/退避持久化/补部署) | ✅ 已实现 |
| acme(lego 封装/http-01/dns-01/账号密钥) | ✅ 已实现 |
| storage(原子落盘/meta/0600) | ✅ 已实现 |
| deploy(nginx 本地/ssh 远程/file,含回滚与幂等) | ✅ 已实现 |
| notify(日志/webhook 告警) | ✅ 已实现 |
| CLI(serve/issue/renew/list/inspect/validate-config) | ✅ 已实现 |
| GUI(Wails 仪表盘/操作/热重载) | ✅ 已实现(Windows 构建验证;Linux 待部署环境验证) |
| tencent-cloud provisioner、webhook 部署类型、私钥加密 | ⏳ v2 规划 |


## 1. 项目定位

go-zs 是一个证书自动化托管工具,用于:

- 自动签发证书(ACME 协议,基于 lego)
- 自动续期(剩余 < 30 天触发)
- 自动部署到反向代理(本地 / SSH 远程)
- 提供简洁的桌面仪表盘(GUI)查看关键状态,并支持 headless CLI 模式

形态:Wails 桌面应用 + core 纯 Go 逻辑 + CLI 子命令。

## 2. 设计原则

1. **核心与界面分离**:core 不依赖任何 UI 框架,可测试、可复用。
2. **配置即声明**:一份 YAML 描述所有托管证书,进程按声明执行。
3. **幂等可靠**:指纹驱动,无变化不部署;失败退避重试;部署失败可回滚并告警。
4. **数据安全**:开源的是代码,不是数据;私钥不出本机,可加密存储。
5. **统一抽象**:签发源(Provisioner)和部署目标(Deployer)均为接口,可扩展。

## 3. 总体架构

```
┌──────────────────────────────────────────────┐
│  桌面应用 (Wails GUI)                         │
│  简洁仪表盘 + 配置管理 + 操作按钮               │
└──────────────────┬───────────────────────────┘
                   ▼
┌──────────────────────────────────────────────┐
│  core (纯 Go 逻辑)                            │
│  config / certmgr / scheduler / provisioner   │
│  storage / deploy / notify                    │
└──────────────────────────────────────────────┘
   ├─ 后台调度常驻 (6h 扫描 + 续期 + 部署)
   ├─ 数据全部存本地 (证书/私钥/meta.json/配置)
   └─ CLI 子命令 (headless 模式跑服务器)
```

## 4. 模块划分

```
go-zs/
├── cmd/
│   ├── go-zs/          # CLI (headless): serve / list / renew / inspect / validate-config
│   └── go-zs-app/      # 桌面应用入口 (Wails)
├── internal/
│   ├── config/         # YAML 分层配置 + 校验 + 热加载
│   ├── certmgr/        # 生命周期状态机 (幂等/指纹/SAN 变更检测)
│   ├── scheduler/      # 6h 扫描 / 30d 阈值 / 退避 / 令牌桶限流
│   ├── acme/           # Provisioner 接口 + lego 实现
│   ├── storage/        # 原子落盘 + 私钥 0600 + 可选 AES-GCM 加密
│   ├── deploy/         # Deployer 插件: nginx / file / webhook / ssh
│   ├── notify/         # 告警 (日志 / webhook)
│   └── app/            # Wails bindings (GUI ↔ core 桥接)
├── frontend/           # Wails 前端 (仪表盘)
├── docs/DESIGN.md
└── go-zs.yaml.example
```

## 5. 证书来源(Provisioner)

| 维度 | 决策 |
|---|---|
| 签发底座 | go-acme/lego,封装为 `Provisioner` 接口 |
| 第一版 | 仅 `acme` 实现(Let's Encrypt / ZeroSSL 等) |
| 预留 | `tencent-cloud`(腾讯云 API)等放 v2 |

```go
type Provisioner interface {
    Ensure(ctx context.Context, domains []string) (*Bundle, error) // 签发或续期
}
```

### 挑战方式

- http-01:80 端口放 `.well-known/acme-challenge/<token>`,CA 回访验证
- dns-01:`_acme-challenge.<domain>` 加 TXT 记录,走 lego 的 DNS 服务商插件

两者均为第一版一等公民,按条目配置。

## 6. 证书生命周期状态机

```
        issue/renew 成功              指纹变化
  ┌───────┐        ┌────────┐ deploy ─────► ┌───────┐
  │ IDLE  │──签发──►│ FRESH  │─────────────► │ 部署完成│
  └───▲───┘        └───┬────┘               └───────┘
      │                │ 每日扫描发现剩余 < 30d
      │                ▼
      │             RENEWING
      │                │ 失败
      └──重试成功──────►│
                       ▼
                    RETRY(退避 1h/6h/24h/72h,封顶)
                       │ 超过上限
                       ▼
                    FAIL ──► 告警(notify)
```

规则:

- 本地无证书 → issue
- 剩余有效期 > 30d → 不动
- 剩余有效期 ≤ 30d → renew(重新签发,换新私钥)
- 失败按退避序列重试,状态持久化到 meta.json(重启不清零)
- SAN 集合变化(配置域名增减)→ 主动重签
- 签发/续期后指纹未变 → 不触发 deploy

## 7. 调度器

- 默认每 6h 扫描一次全部条目(可配置)
- 并发签发用令牌桶限流,尊重 CA 速率限制(Let's Encrypt:每域名每周 50 张等)
- 单实例保证:PID 文件防双跑;SIGTERM 优雅退出
- 重启恢复:启动时读 meta.json 重建状态,不重复签发

## 8. 存储

```
certs/<name>/
├── cert.pem        # 仅 leaf
├── fullchain.pem   # leaf + intermediate(反代通用)
├── privkey.pem     # 权限 0600,可选 AES-GCM 加密(口令/keyring)
├── chain.pem       # 仅 intermediate
└── meta.json       # 域名、签发时间、到期时间、指纹、SAN、失败计数、订单号
```

- 原子写入:临时文件 + rename,反代永远读不到半截文件
- 部署失败可回滚(保留 .bak 备份)

## 9. 部署(Deployer 插件)

```go
type Deployer interface {
    Name() string
    Deploy(ctx context.Context, cert *Bundle) error
}
```

### 9.1 nginx(本地)

写临时文件 → 原子 rename → `nginx -t` 校验 → reload → 失败回滚 + 告警。

### 9.2 ssh(远程)

本地生成 → sftp 上传远程临时路径 → 远程原子 rename → 远程执行 `nginx -t && reload` → 失败用远程备份回滚。

配置:

```yaml
deploy:
  - type: ssh
    host: 203.0.113.10
    port: 22
    user: deploy
    key: ~/.ssh/id_ed25519   # 密钥认证优先,支持 ssh-agent
    remote_path: /etc/nginx/ssl/example/
    reload_cmd: nginx -t && nginx -s reload
```

### 9.3 file

仅落盘,供外部 cron 消费。

### 9.4 webhook

POST 事件通知(新证书指纹/路径),由网关自行热加载。

## 10. 监控与状态查询

- `go-zs list`:列出全部托管证书与剩余天数
- `go-zs inspect <name>`:查看单张详情(SAN/NotBefore/NotAfter/指纹/部署状态)
- GUI 仪表盘:概览卡片 + 证书列表(绿/黄/红三色剩余天数)+ 条目详情 + 立即续期/部署按钮

## 11. 配置模型

```yaml
# go-zs.yaml
ca:
  server: https://acme-v02.api.letsencrypt.org/directory  # 默认 staging 先跑通
  email: ops@example.com
  account_key: ./data/account.key

certificates:
  - name: example
    domains: [example.com, www.example.com]
    challenge: http-01          # 或 dns-01
    # dns_provider: cloudflare
    # dns_provider_opts: { api_token: "{{env:CF_TOKEN}}" }
    storage:
      dir: ./certs/example
    deploy:
      - type: nginx
        reload_cmd: nginx -s reload

schedule:
  check_interval: 6h
  renew_before: 30d
  retry_backoff: [1h, 6h, 24h, 72h]
```

### 分层与动态配置

- 全局配置(ca、调度、存储位置)+ 证书条目(域名、挑战、部署目标)
- GUI 表单化编辑,保存后热加载;条目级变更即时生效
- 密钥类配置只填引用 `{{env:VAR}}`,界面掩码显示

## 12. 数据安全(开源前提)

- 私钥/账号密钥不出本机;日志不打印密钥
- 私钥 0600,可选 AES-GCM 加密(口令派生或系统 keyring)
- SSH 传输走加密通道,私钥不落明文临时文件
- 开源的是代码,不是用户数据

## 13. 技术栈

| 层 | 选型 |
|---|---|
| 语言 | Go 1.25+ |
| 签发 | go-acme/lego |
| GUI | Wails v2(Go 后端 + Web 前端) |
| SSH | golang.org/x/crypto/ssh + sftp |
| 配置 | YAML (gopkg.in/yaml.v3) |
| CLI | spf13/cobra |

## 14. 演进路线

| 阶段 | 内容 |
|---|---|
| MVP | core(ACME http-01/dns-01 + 调度 + 存储 + nginx/ssh deploy)+ CLI + Wails 仪表盘 |
| v2 | tencent-cloud provisioner、webhook/k8s deploy、私钥加密、告警通知 |
| v3 | 多 CA、KMS、OCSP stapling、HTTP API server |

## 15. 界面设计(简洁仪表盘)

- 概览卡片:托管证书数、健康状态(绿/黄/红)、下次扫描时间、最近操作结果
- 证书列表:名称、域名、剩余天数(三色)、状态;点击展开详情
- 条目详情:SAN、NotBefore/NotAfter、指纹、签发源、部署目标与结果
- 操作:立即续期、立即部署
- 状态栏:最近扫描时间、任务进度、告警记录
