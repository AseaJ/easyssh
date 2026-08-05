# 贡献指南

感谢你愿意为 easyssh 贡献代码!请花几分钟读完这份指南,能让你的 PR 更快被合并。

## 项目速览

- **语言**:Go 1.25+,CLI 用 [cobra](https://github.com/spf13/cobra),GUI 用 [Wails v2](https://wails.io)
- **结构**:核心逻辑在 `internal/`(config / certmgr / scheduler / acme / storage / deploy / notify),CLI 入口在 `cmd/easyssh`,GUI 在 `cmd/easyssh-app`
- **架构说明**:见 [`docs/DESIGN.md`](docs/DESIGN.md)

## 环境要求

- Go 1.25+(CLI 开发只需这个)
- 开发 GUI 另需 Node.js 18+、Wails v2 及平台依赖(Windows: WebView2;Linux: webkit2gtk)

## 本地开发

```bash
# 构建 CLI
go build ./cmd/easyssh

# 运行测试
go test ./...

# 静态检查
go vet ./...
```

## 提交 PR 前检查

1. `go build ./...` 通过
2. `go vet ./...` 无告警
3. `go test ./...` 全绿
4. 新功能/修复带有对应测试(尤其是安全相关改动)
5. 若改动配置结构,同步更新 `easyssh.yaml.example` 与 README
6. `git commit` 信息遵循 [Conventional Commits](https://www.conventionalcommits.org/zh-hans/):
   `feat:` / `fix:` / `docs:` / `chore:` / `refactor:` / `test:` 前缀

## 安全相关

**本项目托管私钥与证书,安全问题极其敏感。** 请勿在 issue / PR / 讨论中公开任何
私钥内容、真实配置或真实主机信息。

- 发现安全漏洞:**不要**公开提交 issue,请通过 SECURITY.md 中的渠道私下报告
- 涉及私钥/证书/加密的代码改动,务必附测试,并在 PR 描述中说明威胁模型

## 流程

1. Fork 仓库并创建功能分支:`git checkout -b feat/my-feature`
2. 提交改动(小步、清晰)
3. 推送并开 PR,描述里写清"改了什么、为什么、如何验证"
4. 维护者 review 后会给出反馈,通常 1-2 轮内可合并
