# 导出/导入包格式(.zsbundle)

本文档定义 `go-zs export` / `go-zs import`(及 GUI 同功能)的包格式 v1。

## 1. 目标

- 证书列表导出/导入,让不同客户端**直接复用同一套配置**(证书条目、hosts、调度、通知)。
- 关联配置可一起导出:密钥(可选,口令加密)、证书产物(可选)、SSH 私钥(可选)。
- 分级:默认只导出配置(可安全分发);敏感内容必须经口令加密。

## 2. 格式总览

单文件 zip 容器,扩展名 `.zsbundle`。

```
example.zsbundle
├── manifest.json          # 明文清单:版本/范围/加密参数/内容索引/警告
├── config.yaml            # 明文:配置(密钥保持 {{env:VAR}} 引用,不展开)
├── secrets.enc            # 可选:AES-256-GCM 密文(env 密钥值 + ACME account.key)
├── ssh-keys.enc           # 可选:AES-256-GCM 密文(SSH 私钥内容)
└── certs/
    ├── <cert-name>/
    │   ├── fullchain.pem
    │   ├── privkey.pem
    │   └── meta.json      # 仅描述性字段,运行时状态清零
    └── ...
```

## 3. manifest.json

```json
{
  "format_version": 1,
  "kind": "go-zs-bundle",
  "exported_at": "2026-08-04T12:00:00+08:00",
  "app_version": "0.1.0-dev",
  "scope": { "config": true, "secrets": true, "certs": true, "ssh_keys": true },
  "kdf": { "algo": "scrypt", "n": 131072, "r": 8, "p": 1, "key_len": 32 },
  "certs": [ { "name": "example", "domains": ["example.com"] } ],
  "ssh_keys": [ { "path": "./id_ed25519" } ],
  "env_secrets": ["GOZS_EXAMPLE_API_TOKEN"],
  "warning": "…"
}
```

- `scope.config` 恒为 true(config 是底座)。
- `scope.secrets` 或 `scope.ssh_keys` 为 true 时,`kdf` 必填,且必须有对应 `.enc` 条目。
- 导入方**不解密即可预览**:检查版本、范围、条目清单、冲突,再决定是否输口令。

## 4. 加密方案

- 算法:AES-256-GCM(标准库 `crypto/aes` + `crypto/cipher`)。
- KDF:口令 → scrypt(N=2^17, r=8, p=1, keyLen=32)派生密钥。
  - salt 16B、nonce 12B,均每次导出随机;**每个 .enc 文件独立 salt+nonce**。
- `.enc` 二进制布局:

```
"GZSB" | version(1B=1) | salt(16B) | nonce(12B) | AES-GCM 密文+tag(16B)
```

- 解密:校验 magic/version → 按 manifest.kdf 参数派生密钥 → GCM Open。
  - tag 校验失败 = 口令错误或文件损坏(不可区分,统一报「口令错误或文件已损坏」)。
- 口令强度:至少 10 字符且非纯数字(导出时强制)。

## 5. 导出范围

| scope 勾选 | 口令 | 产物 |
|---|---|---|
| config | 不需要 | manifest + config.yaml |
| config, secrets | 必须 | + secrets.enc |
| config, secrets, certs, ssh-keys | 必须 | + ssh-keys.enc + certs/ |

- 导出 `secrets` 时,遍历 config.yaml 中出现的 `{{env:VAR}}` 引用:
  - 环境变量缺失 → 导出失败并指名缺失项(避免导出缺密钥的包)。
  - 同时打包 `account_key` 指向的 PEM 文件。
- 导出 `certs` 时,读各条目 `storage.dir` 下的 cert/fullchain/chain/privkey/meta。
  - meta.json 只保留描述性字段(Name/Domains/NotBefore/NotAfter/Fingerprint),**清零运行时状态**(DeployedFingerprint/FailCount/LastError 等)。
- 导出 `ssh-keys` 时,收集 hosts 与 deploy.ssh 的 `key` 路径(去重)读取私钥内容。

## 6. 导入流程

1. 读 zip → 解析并校验 manifest(format_version/kind/kdf 参数)。
2. 预览:清单摘要 + 冲突检测(条目名 / hosts 名)。
3. 含加密段时输入口令 → 解密 secrets/ssh-keys。
4. 解析包内 config.yaml → 合并进目标配置:
   - **全新导入**(目标配置不存在):以包内全局段(CA/Schedule/Notify)为基底。
   - **追加合并**:hosts/证书条目按名追加。
5. 冲突策略(CLI `--conflict`,GUI 选择):
   - `append`(默认):冲突项改名追加(`example` → `example-2`),deploy 的 `host_ref` 联动改名。
   - `skip`:跳过冲突项(目标机已有同名条目)。
   - `overwrite`:覆盖同名词条(仅条目级;hosts 永不覆盖)。
6. 路径重映射:相对路径(`storage.dir`、`key`、`account_key`)基于目标配置目录重新解析。
7. 落盘:
   - env 密钥 → 系统环境变量并持久化(Windows:注册表;复用 `app.PersistEnv`)。
   - account.key → 目标路径(0600)。
   - certs/ → 各条目 storage.dir。
   - ssh-keys → 目标路径(0600)。
   - config.yaml → 原子写回。
8. 导入后提示:缺失的 env 变量;未携带 SSH 私钥的部署目标(需人工配密钥)。

## 7. 不迁移的内容

- 运行时状态:meta.json 的 `deployed_fingerprint`、`fail_count`、`next_retry_at`。
  - 首次扫描按幂等机制(指纹驱动)自动补部署。
- 进程内状态:调度器 lastRun 等。

## 8. 安全边界

- 口令即唯一防线:泄露 `.zsbundle` + 口令 = 泄露全部密钥/私钥。
- 建议口令 ≥ 16 字符;含敏感段的包经加密传输/当面拷贝传递。
- 导出的 zip 内私钥为明文存储(整体靠可选导出 + 安全传输);v2 可考虑加密整个 certs 段。
- 导入方对 zip 做防护:总大小 ≤ 256MB、单条目 ≤ 64MB、条目数 ≤ 4096(防 zip bomb)。

## 9. 版本演进

- `format_version` 递增策略:
  - 向后兼容(新增字段/新 KDF algo)→ v1 内兼容,导入方忽略未知字段。
  - 破坏性变更(布局/语义变化)→ 升 `format_version`,旧导入方明确拒绝并提示。
- 预留:KDF `algo` 支持 `argon2id`(manifest 带参数);certs 段整体加密。
