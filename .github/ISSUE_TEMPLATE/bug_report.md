---
name: Bug 报告
about: 报告一个可复现的问题
title: "[Bug] "
labels: bug
assignees: ''
---

**⚠️ 安全提示**:如果问题涉及私钥、证书内容、真实主机信息或凭据泄露,
**不要**在这里提交,请走 [SECURITY.md](../../SECURITY.md) 的私密渠道。

## 问题描述

清晰描述你遇到的问题。

## 复现步骤

1. 配置(脱敏后贴出,密钥一律替换为 `<REDACTED>`)
2. 执行的命令
3. 观察到的现象

## 期望行为

应该发生什么?

## 实际行为

实际发生了什么?(附 `go-zs list` / `go-zs inspect <name>` 输出,如适用)

## 环境

- 操作系统:Windows / Linux / macOS,版本
- go-zs 版本:`go-zs --version` 输出
- 部署类型:nginx / ssh / file / webhook
- ACME 环境:staging / production

## 日志

粘贴相关日志(注意脱敏)。
