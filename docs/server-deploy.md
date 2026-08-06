# 服务器端部署指南(守护进程 / Docker)

easyssh 的 **CLI headless 形态本身就是为服务器设计的**:`easyssh serve` 以守护进程方式常驻,
按 `check_interval` 周期扫描,自动完成签发/续期/部署,与桌面 GUI 共用同一套核心逻辑与配置格式。
服务器端**不需要也不包含 GUI**(Wails 桌面端),功能与桌面端完全一致:

| 能力 | 桌面端(GUI) | 服务器端(CLI serve) |
|---|---|---|
| 自动签发/续期(ACME) | ✅ | ✅ |
| 自动部署(nginx/ssh/file/webhook) | ✅ | ✅ |
| 周期扫描 + 失败退避 | ✅(后台运行) | ✅(serve 常驻) |
| webhook/邮件告警 | ✅ | ✅ |
| 手动操作 | GUI 按钮 | `issue` / `renew` / `deploy` / `list` 命令 |
| 配置热更新 | ✅(界面保存) | ✅(改 yaml 后重启 serve 或发信号) |
| 开机自启 | ✅(Windows 托盘) | systemd / Docker `restart` |

> 服务器端与桌面端是**同一份代码、同一个二进制**。`docs/DESIGN.md` 中的 core 层
> (config → certmgr → scheduler → acme → storage → deploy → notify)对两者完全一致,
> 只是服务器端没有 GUI 前端与托盘。

---

## 一、两种部署方式

| 方式 | 适合场景 | 优点 |
|---|---|---|
| **Docker Compose**(推荐) | 已有 Docker 的服务器 | 隔离、可复现、升级简单 |
| **裸机 + systemd** | 精简服务器 / 不想装 Docker | 无容器开销,直接管理 |

仓库已提供两种模板:

- `Dockerfile` + `deploy/docker-compose.yml`
- `deploy/easyssh.service`(systemd 单元)

---

## 二、服务器端配置(与桌面端完全相同)

拷贝示例配置并编辑:

```bash
cp easyssh.yaml.example easyssh.yaml
vim easyssh.yaml
```

关键差异只有两点,都是**环境相关**而非功能差异:

### 1. 切换正式 CA(重要!)

示例配置默认连接 Let's Encrypt **staging**(测试环境,不占正式额度)。
跑通流程后,务必切换为正式环境,否则浏览器不信任签发的证书:

```yaml
ca:
  server: https://acme-v02.api.letsencrypt.org/directory   # 正式环境
  email: ops@example.com
  account_key: ./data/account.key   # 首次运行自动生成
```

> 同一台机器第一次用正式环境签发前,建议先清掉 staging 的 account key 与测试证书
> (`rm -rf data certs`),避免账号/域名限额混淆。

### 2. http-01 需要 80 端口(仅 http-01)

- **http-01**:Let's Encrypt 会访问 `http://<域名>/.well-known/acme-challenge/...` 回访验证,
  easyssh 在挑战期间临时监听 80 端口。需要:
  - 服务器 80 端口空闲(未被 nginx 占用),或让 nginx 反代该路径
  - 防火墙/安全组放行 80 入站
  - Docker 方式需映射 `-p 80:80`
- **dns-01**:通过 DNS 服务商 API 验证,**不需要 80 端口**,
  适用于服务器没有公网 80 端口、或需要通配符证书的场景:

  ```yaml
  certificates:
    - name: example
      domains:
        - example.com
        - "*.example.com"
      challenge: dns-01
      dns_provider: dnspod            # cloudflare / dnspod / alidns
      dns_provider_opts:
        api_token: "{{env:DNSPOD_TOKEN}}"
  ```

### 3. 密钥用环境变量注入,不写进配置文件

配置文件中的密钥一律用 `{{env:VAR}}` 引用,明文放在进程环境变量里:

- **Docker**:在 compose 的 `environment:` 或 `.env` 文件(已被 .gitignore 排除)中设置
- **systemd**:写入 `/opt/easyssh/easyssh.env`(chmod 600),由 `EnvironmentFile=` 加载

---

## 三、Docker Compose 部署

### 目录结构

```
/opt/easyssh/
├── easyssh.yaml          # 配置(只读挂载)
├── .env                  # 密钥(可选;已被 git 忽略)
├── data/                 # 数据卷:account.key / certs/ / pid
└── docker-compose.yml    # 来自仓库 deploy/
```

### 步骤

```bash
mkdir -p /opt/easyssh && cd /opt/easyssh

# 1. 拷贝配置模板与编排文件
cp <repo>/easyssh.yaml.example ./easyssh.yaml
cp <repo>/deploy/docker-compose.yml .

# 2. 编辑配置(见上文:切正式 CA、域名、挑战方式、部署目标)
vim easyssh.yaml
```

> **⚠️ 容器内路径必须写绝对路径**(最容易踩的坑):
> 容器内配置文件位于 `/app/config`(只读挂载),而相对路径都基于配置目录解析,
> 因此以下路径若保持示例里的 `./` 写法,会尝试写入只读的 `/app/config`,启动即失败
> (`mkdir /app/config/...: read-only file system`)。请改为指向可写数据卷 `/app/data` 的绝对路径:
>
> ```yaml
> ca:
>   account_key: /app/data/account.key        # 默认 ./data/account.key → 必须改
> certificates:
>   - name: example
>     storage:
>       dir: /app/data/certs/example          # 默认 ./certs/example → 必须改
> hosts:
>   - name: prod
>     key: /app/data/ssh_key                  # 若有 SSH 部署,私钥放进 ./data/
>     known_hosts: /app/data/known_hosts      # ssh-keyscan 生成后放入 ./data/
> ```

```bash
# 3. 可选:密钥写入 .env(勿提交 git)
#    echo 'DNSPOD_TOKEN=xxx' > .env

# 4. 构建并启动
docker compose up -d --build

# 5. 查看日志
docker compose logs -f easyssh

# 6. 手动签发一次验证(staging 或正式)
docker compose exec easyssh easyssh issue <name> -c /app/config/easyssh.yaml
docker compose exec easyssh easyssh list -c /app/config/easyssh.yaml
```

**首次启动若报 data 目录权限错误**(distroless 以 nonroot 用户 65532 运行):

```bash
chown -R 65532:65532 /opt/easyssh/data
```

**修改配置后重新加载**:

```bash
docker compose up -d --force-recreate
```

### 与宿主机 nginx 共享证书

把证书产物目录挂进 nginx 容器(或宿主机 nginx 直接读该目录):

```yaml
volumes:
  - ./data/certs:/etc/nginx/ssl:ro
```

并在 easyssh.yaml 中:

```yaml
deploy:
  - type: file              # 或 nginx(reload 走宿主机命令,容器内无法 reload 宿主 nginx)
    dir: ./data/certs/example
```

> 注意:容器内的 `nginx` 部署类型执行 `nginx -t` / `nginx -s reload` 是针对**容器内**进程;
> 若你的 nginx 跑在宿主机,请用 `file` 类型落盘 + 宿主机 cron/systemd 重载,
> 或用 `ssh` 类型推送到 nginx 所在主机。

---

## 四、裸机 + systemd 部署

### 步骤

```bash
# 1. 创建用户与目录
sudo useradd --system --home /opt/easyssh --shell /usr/sbin/nologin easyssh
sudo mkdir -p /opt/easyssh/data && cd /opt/easyssh

# 2. 放置二进制(任选:CI 产物 / 本机 go build)
#    GOOS=linux GOARCH=amd64 go build -o easyssh ./cmd/easyssh
sudo install -m 0755 easyssh /opt/easyssh/easyssh

# 3. 配置(注意 account_key / storage.dir / known_hosts 的相对路径
#    以 WorkingDirectory=/opt/easyssh 为基准解析)
sudo cp <repo>/easyssh.yaml.example ./easyssh.yaml
sudo chown root:easyssh easyssh.yaml && sudo chmod 640 easyssh.yaml

# 4. 密钥环境文件(0600,勿提交 git)
sudo touch easyssh.env && sudo chmod 600 easyssh.env
#    sudo vi easyssh.env   # 写入 DNSPOD_TOKEN=xxx 等

# 5. 安装 systemd 单元并启动
sudo cp <repo>/deploy/easyssh.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now easyssh

# 6. 验证
systemctl status easyssh
sudo journalctl -u easyssh -f
```

### 二进制安装方式对比

```bash
# 方式 A:GitHub Release(仓库发布后)
#   从 Releases 页下载 easyssh-linux-amd64 等压缩包解压

# 方式 B:本地交叉编译(与 CI 相同)
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o easyssh ./cmd/easyssh
```

---

## 五、常用运维命令(服务器端)

```bash
# 校验配置(含 {{env:VAR}} 展开,缺变量会报错)
easyssh validate-config -c easyssh.yaml

# 查看全部证书与剩余天数
easyssh list -c easyssh.yaml

# 查看单个条目详情
easyssh inspect <name> -c easyssh.yaml

# 立即签发/续期指定条目
easyssh issue <name> -c easyssh.yaml

# 强制重新签发并部署
easyssh renew <name> -c easyssh.yaml

# 单次全量扫描(不常驻)
# 常驻服务用 serve;手动触发可用:systemctl restart easyssh(启动即扫描一次)
```

### 热更新配置

`serve` 每次启动与周期扫描都会重新读取配置。修改 `easyssh.yaml` 后:

- **systemd**:`sudo systemctl restart easyssh`(重启即执行一次扫描,可顺带验证新配置)
- **Docker**:`docker compose up -d --force-recreate`

---

## 六、告警配置(服务器端建议开启)

服务器上无人值守,建议开启通知,失败时第一时间收到:

```yaml
notify:
  smtp:
    host: smtp.qq.com
    port: 465
    user: you@qq.com
    pass: "{{env:SMTP_AUTH_CODE}}"
    to: ops@example.com
  events:
    expiring: true
    success: true
```

> 失败告警始终推送(不受 events 开关控制)。

---

## 七、常见问题

**Q:docker 里提示 `listen tcp :80: bind: permission denied`?**
distroless nonroot 用户无特权端口绑定。镜像已声明 `EXPOSE 80` 但容器默认无法 bind <1024 端口;
将 compose 的端口映射改为 `- "8080:8080"` 并在配置/命令中指定
`--http-port 8080`(http-01 挑战监听端口),然后由宿主机 nginx 反代 `/.well-known/acme-challenge/` 到该端口;
或用 dns-01 挑战完全避开 80 端口。

**Q:容器启动即报 `mkdir /app/config/...: read-only file system`?**
示例配置的相对路径(`account_key: ./data/...`、`storage.dir: ./certs/...`)在容器内
基于只读挂载的 `/app/config` 解析。按上文「容器内路径必须写绝对路径」改为
`/app/data/...` 即可(数据卷可写)。

**Q:如何把证书部署到宿主机 nginx(容器内 reload 不到宿主 nginx)?**
用 `ssh` 类型推送到 nginx 所在主机,或 `file` 类型落盘到共享卷后由宿主机侧重载。

**Q:Windows 桌面端导出的 `.zsbundle` 能导入服务器端吗?**
可以。`easyssh import` 与 GUI 导入同一格式,配置/密钥/证书跨平台迁移:
`easyssh import -f full.zsbundle -c easyssh.yaml --conflict append --password 'xxx'`。

**Q:服务器端需要 X/Wayland 或桌面环境吗?**
不需要。CLI 是纯 headless,不依赖任何 GUI 库;`cmd/easyssh-app`(Wails)不会被编译进服务器端镜像。

**Q:多个服务器共用一套配置怎么管理?**
用 `.zsbundle` 导出/导入迁移配置;每台服务器独立运行 `serve`,
各自维护本机 meta.json 部署状态(幂等机制会自动补部署)。
