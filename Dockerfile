# syntax=docker/dockerfile:1

# ---- 构建阶段:交叉编译 Linux CLI(与 CI 同一命令) ----
FROM golang:1.25 AS builder
WORKDIR /src

# 先拷贝依赖清单,利用 Docker 层缓存
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# 静态编译(与 CI 交叉编译一致),版本号可注入
ARG VERSION=0.1.0-dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/easyssh ./cmd/easyssh

# ---- 运行阶段:精简镜像,非 root 运行 ----
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app

COPY --from=builder /out/easyssh /usr/local/bin/easyssh

# 数据目录(config 之外):account.key、certs/、pid 文件
# 建议以只读方式挂载配置目录,数据目录挂卷持久化
VOLUME ["/app/data"]

EXPOSE 80

ENTRYPOINT ["/usr/local/bin/easyssh"]
CMD ["serve", "-c", "/app/config/easyssh.yaml", "--pidfile", "/app/data/easyssh.pid"]
