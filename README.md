# nodekeep

nodekeep 是一个轻量级服务器监控面板和 Agent，Dashboard 提供 Web 管理、节点状态、服务监控、告警通知和 Agent 接入，Agent 负责采集主机状态并执行探测任务。

本项目基于早期哪吒监控面板实现，主要用于个人自用和小规模环境。当前维护重点是修复已知安全风险、收敛外部依赖、简化部署方式，并不追求与上游功能完全一致。

## 功能概览

- 节点监控：CPU、内存、交换分区、磁盘、网络、运行时间、在线状态。
- 服务监控：HTTP、HTTPS 证书、TCP、ICMP Ping。
- 告警通知：支持自定义通知请求和告警规则，支持恢复通知。
- 登录方式：支持本地管理员账号，也支持 GitHub / Gitee OAuth2。
- 单端口入口：Dashboard Web、WebSocket 和 Agent gRPC 共用一个端口，默认 `8008`。

## 快速安装

### 脚本安装

在 Dashboard 服务器执行：

```sh
curl -fsSL https://raw.githubusercontent.com/r0n9/nodekeep/master/script/install.sh -o nodekeep.sh
chmod +x nodekeep.sh
sudo ./nodekeep.sh install_dashboard
```

安装脚本会交互式配置本地管理员账号、站点端口、Agent 接入地址和 TLS 选项。运行数据默认保存在 `/opt/nodekeep`，后续可再次运行：

```sh
sudo ./nodekeep.sh
```

脚本菜单支持启动、停止、更新、查看日志和卸载。

### Docker 安装

创建数据目录和配置文件：

```sh
mkdir -p nodekeep/data
cd nodekeep
cat > data/config.yaml <<'EOF'
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
oauth2:
  type: "github"
  admin: ""
  clientid: ""
  clientsecret: ""
site:
  brand: "nodekeep"
  cookiename: "nodekeep-dashboard"
EOF
```

至少修改 `password`、`agent.install_host` 和 `agent.tls`。如果通过 HTTPS 反代访问，`agent.install_host` 填域名和外部端口，例如 `nodekeep.example.com:443`，`agent.tls` 设置为 `true`；如果直接访问 `http://IP:8008`，则填 `IP:8008` 并设置为 `false`。

启动容器：

```sh
docker run -d \
  --name nodekeep-dashboard \
  --restart unless-stopped \
  -p 8008:8008 \
  -v "$PWD/data:/dashboard/data" \
  ghcr.io/r0n9/nodekeep-dashboard:latest
```

也可以使用 Compose：

```yaml
services:
  dashboard:
    image: ghcr.io/r0n9/nodekeep-dashboard:latest
    restart: unless-stopped
    ports:
      - "8008:8008"
    volumes:
      - ./data:/dashboard/data
```

## Agent 安装

先登录 Dashboard，在“服务器”页面添加节点，然后点击管理列的 Agent 安装命令复制按钮。复制出的命令会自动携带服务器地址、节点密钥和 TLS 参数。

手动安装命令示例：

```sh
curl -fsSL https://raw.githubusercontent.com/r0n9/nodekeep/master/script/install-agent.sh | sudo bash -s -- -s nodekeep.example.com:443 -p YOUR_AGENT_SECRET
```

如果 Dashboard 直接以 HTTP/h2c 暴露，例如 `127.0.0.1:8008`，需要追加 `--insecure`：

```sh
curl -fsSL https://raw.githubusercontent.com/r0n9/nodekeep/master/script/install-agent.sh | sudo bash -s -- -s 127.0.0.1:8008 -p YOUR_AGENT_SECRET --insecure
```

Agent 安装脚本支持 Linux systemd、Linux OpenRC 和 macOS launchd。安装过程会打印当前系统架构、下载地址和安装路径；重复安装会备份旧二进制和服务文件，启动失败时自动回滚。卸载和停止命令会在安装成功后输出。

## 反向代理

nodekeep 使用同一个后端端口承载：

- 普通 Web 页面和 API。
- Dashboard 实时更新 WebSocket：`/ws`。
- Agent gRPC：`Content-Type: application/grpc`。

Nginx HTTPS 反代示例：

```nginx
server {
    listen 443 ssl http2;
    server_name nodekeep.example.com;

    ssl_certificate /etc/nginx/ssl/nodekeep.example.com.crt;
    ssl_certificate_key /etc/nginx/ssl/nodekeep.example.com.key;

    location /proto.ProbeService/ {
        grpc_pass grpc://127.0.0.1:8008;
        grpc_set_header Host $host;
        grpc_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        grpc_set_header X-Forwarded-Proto $scheme;
    }

    location /ws {
        proxy_pass http://127.0.0.1:8008;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 3600s;
    }

    location / {
        proxy_pass http://127.0.0.1:8008;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

使用 HTTPS 反代时，系统设置里的 `Agent 安装地址` 建议填写 `nodekeep.example.com:443`，`Agent 使用 TLS` 设置为开启。直接通过 `http://IP:8008` 访问时，Agent 安装命令需要 `--insecure`。

## 配置和数据

- 脚本安装目录：`/opt/nodekeep`
- Docker 数据目录：`/dashboard/data`
- 配置文件：`data/config.yaml`
- SQLite 数据库：`data/sqlite.db`

不要提交本地配置、数据库、OAuth2 密钥、通知 Webhook 或 Agent 密钥。

## License

当前仓库未声明许可证。
