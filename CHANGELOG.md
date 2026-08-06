# Changelog

本项目遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.0.0/) 与 [Semantic Versioning](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### 新增

- 服务器端部署模板(Docker / systemd),`serve` 守护进程与桌面端共用同一核心,功能一致:
  - `Dockerfile`(多阶段构建 + distroless nonroot 精简镜像)与 `.dockerignore`
  - `deploy/docker-compose.yml`(配置只读挂载、数据卷持久化、密钥环境变量注入)
  - `deploy/easyssh.service`(systemd 单元,含安全加固与 `CAP_NET_BIND_SERVICE`)
  - 部署指南 [`docs/server-deploy.md`](docs/server-deploy.md):http-01 的 80 端口要求、dns-01 免端口方案、与宿主机 nginx 共享证书、运维命令与 FAQ

### 安全

- SSH 部署改为强制 known_hosts 校验:未配置或指纹不匹配时拒绝连接,防中间人攻击
  - 新增配置项 `known_hosts`(hosts 定义与内联 ssh 部署均可)
  - GUI「测试连接」同步支持 known_hosts 透传
- PID 单实例锁检测进程存活:残留死 PID 自动覆盖,崩溃后不再无法启动

### 变更

- CLI 版本号改为构建期注入(`-ldflags "-X main.version=vX.Y.Z"`)
- 移除无用代码
- 项目更名为 **easyssh**,CLI 命令改为 `easyssh`,所有代码/文档同步更新
  - 配置文件名: `easyssh.yaml`(原 `go-zs.yaml`)
  - 可执行文件名: `easyssh.exe`(原 `go-zs.exe`)
  - module path: `github.com/asea/easyssh`(仓库创建后启用)

## [0.1.0-dev] - 2026-08-05

初始开发版本(内部使用)。
