# nodekeep

nodekeep 是一个轻量级服务器监控面板和 Agent。Dashboard 提供 Web 管理界面、告警、监控任务和 Agent 接入；Agent 负责上报主机状态并执行探测任务。

当前版本使用单端口入口：Dashboard Web 和 Agent gRPC 共用同一个端口。默认本地访问端口为 `8008`，Docker 容器内监听 `80`。

## 功能

- 主机状态监控：CPU、内存、交换分区、磁盘、网络、在线状态。
- 服务探测：HTTP、HTTPS 证书、TCP、ICMP Ping。
- 告警通知：支持自定义请求方式和告警规则。
- 任务下发：支持计划任务和命令执行。
- 登录方式：支持本地管理员账号，也支持 GitHub / Gitee OAuth2。
- Agent 安装命令：后台服务器列表可直接复制安装命令。

## 快速安装

在 Dashboard 服务器执行：

```sh
curl -fsSL https://raw.githubusercontent.com/r0n9/nodekeep/master/script/install.sh -o nodekeep.sh
chmod +x nodekeep.sh
./nodekeep.sh
```

安装脚本会提示配置：

- 本地管理员账号和密码。
- Dashboard 站点端口。
- Agent 安装地址，例如 `nodekeep.example.com:443`。
- Agent 是否使用 TLS 连接。
- 可选 OAuth2 配置。

运行数据默认保存在 `/opt/nodekeep`。

## Docker

构建镜像：

```sh
docker build -t nodekeep-dashboard -f Dockerfile .
```

示例 Compose 模板在 [script/docker-compose.yaml](script/docker-compose.yaml)。容器内路径：

- 配置文件：`/dashboard/data/config.yaml`
- SQLite 数据库：`/dashboard/data/sqlite.db`
- 静态资源：`/dashboard/resource`

## Agent 安装

先在 Dashboard 的“服务器”页面添加服务器，然后点击管理列的复制按钮。复制出的命令会下载并执行：

```sh
https://raw.githubusercontent.com/r0n9/nodekeep/master/script/install-agent.sh
```

Agent 安装脚本支持：

- Linux `systemd`
- Linux `OpenRC`
- macOS `launchd`
- `amd64`、`386`、`arm`、`arm64`、`mips`、`mips64`、`s390x`、`riscv64` 等 Linux 架构
- macOS `amd64`、`arm64`

重复安装会先备份旧二进制和服务文件，启动失败时自动回滚。

## 单端口与反向代理

Dashboard Web 和 Agent gRPC 共用同一个入口。直接 HTTP 部署时，Agent 命令会使用 `--insecure`。HTTPS 反代部署时，Agent 默认使用 TLS。

Nginx 反代示例：

```nginx
server {
    listen 443 ssl http2;
    server_name nodekeep.example.com;

    location /proto.ProbeService/ {
        grpc_pass grpc://127.0.0.1:8008;
    }

    location / {
        proxy_pass http://127.0.0.1:8008;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }
}
```

在“设置”页面配置 `Agent 安装地址` 和 `Agent 使用 TLS`。服务器列表复制命令会优先使用这两个配置；未配置时才根据当前浏览器地址推断。

## 配置

配置文件示例见 [script/config.yaml](script/config.yaml)。常用字段：

```yaml
debug: false
httpport: 8008
auth:
  local:
    enabled: true
    username: "admin"
    password: "change-me"
agent:
  install_host: "nodekeep.example.com:443"
  tls: true
site:
  brand: "nodekeep"
```

不要提交本地 `data/config.yaml`、SQLite 数据库或任何密钥。

## 开发

常用命令：

```sh
go test ./...
go build ./cmd/dashboard
go build ./cmd/agent
sh script/proto.sh
```

本地运行 Dashboard 前，需要准备：

```text
data/config.yaml
data/sqlite.db
```

项目结构：

- `cmd/dashboard`：Dashboard 入口。
- `cmd/agent`：Agent 入口。
- `model`：数据模型。
- `service`：DAO、RPC 和核心服务。
- `proto`：gRPC protobuf 定义和生成代码。
- `resource`：Dashboard 模板和静态资源。
- `script`：安装脚本、配置模板和服务模板。

## License

当前仓库未声明许可证。
