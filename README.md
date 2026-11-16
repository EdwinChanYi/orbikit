# orbikit

**orbikit 是一套用 Go 编写、可以直接部署的分布式服务端。** 鉴权、房间、长连接网关和消息抄送已经按独立进程跑起来：etcd 做服务发现，服务之间用 gRPC，需要推给在线用户的事件走 Kafka。`make up` 之后就能签发登录令牌、进入房间、收发消息。

后续的私聊、群聊、独立 HTTP 网关，以及使用者自己的业务服务，都按这四套进程的方式加进同一套部署。网关上现有的 HTTP 短连接仍和长连接共用命令，那不是独立的 HTTP 网关。

给 AI 和贡献者的 Go 约定在 [AGENTS.md](AGENTS.md)。Claude Code 从 [CLAUDE.md](CLAUDE.md) 进入同一份约定。

- 新增一个可部署进程：`extend-orbikit-service`
- 在已有服务里加 RPC、命令或字段：`add-orbikit-service-feature`

Codex、Cursor 以及扫描 `.agents/skills/` 的工具读该目录。Claude Code 读 `.claude/skills/`。两处正文相同。

> 状态：v0.1 开发中。核心链路已可编译、可部署，接口仍可能调整。私聊、群聊、独立 HTTP 网关尚未实现。

## 已经提供的服务

| 服务 | 职责 | 默认端口 |
|---|---|---|
| `gateway` | 长连接接入、鉴权、命令路由、消息下发、HTTP 短连接 | TCP 8000 / WS 8001 (`/ws`) / HTTP 8080 |
| `core` | 应用管理、登录 token 签发与校验、用户封禁 | gRPC 9001 |
| `chatroom` | 房间、成员、角色、麦位、机器人、消息，向 Kafka 生产事件 | gRPC 9002 |
| `webhook` | 消费房间消息并签名回调业务服务器 | gRPC 9004 |

- **长连接**：TCP 与 WebSocket 共用 20 字节包头 + protobuf。TCP 可选 xxtea 包体加密，WebSocket 建议配合 wss。网关不保存房间状态，跨节点推送经 Kafka 广播。
- **服务发现**：`core`、`chatroom`、`webhook` 注册到 etcd，调用方用 `etcd:///服务名` 轮询存活实例。`gateway` 只拨号，不注册。本机 `configs/*.yaml` 不启用 etcd，地址保持 `host:port`。
- **多租户**：一套部署服务多个应用（appId / appSecret），应用间数据隔离。
- **房间**：创建/开关房间、进出房、在线列表、踢人/拉黑/禁言（到期自动解除）、自定义角色、麦位（上麦/下麦/邀请/锁麦/禁麦）、机器人、在线人数与麦位排行、在线时长统计。
- **抄送与审核**：房间消息和房间事件按应用配置签名后 POST 到业务服务器。内容审核可选 `none`、本地关键词（DFA）或业务方 HTTP 审核服务。
- **服务端 API**：业务服务器用 gRPC 调用 core / chatroom，完成建房、发 token、发系统消息、管理成员。业务服务器不接入长连接。

## 架构

```mermaid
flowchart LR
    client[客户端<br/>App / Web] -- TCP / WebSocket --> gateway
    biz[业务服务器] -- gRPC --> core
    biz -- gRPC --> chatroom
    gateway -- gRPC --> core
    gateway -- gRPC --> chatroom
    chatroom -- 房间事件/消息 --> kafka[(Kafka)]
    kafka -- 推送 --> gateway
    kafka --> webhook
    webhook -- 签名 HTTP 回调 --> biz
    core --- mysql[(MySQL)]
    chatroom --- mysql
    gateway --- redis[(Redis)]
    chatroom --- redis
    core --- redis
    core --- etcd[(etcd)]
    chatroom --- etcd
    gateway --- etcd
    webhook --- etcd
```

新的 gRPC 服务沿用同一条路径：`net.Listen` 之后登记到 etcd，调用方使用 `etcd:///服务名`。需要下发到在线连接时写 Kafka，由现有网关消费。不要另做一套长连接进程，也不要把业务状态放进 `gateway`。

## 快速开始

需要 Docker（含 compose v2）和 Go 1.27.4+。

```bash
git clone https://github.com/EdwinChanYi/orbikit.git
cd orbikit
make up            # 等价于 docker compose -f deploy/docker-compose.yml up -d --build
```

国内网络可以在构建时指定 Go 代理：`docker compose -f deploy/docker-compose.yml build --build-arg GOPROXY=https://goproxy.cn,direct`。

服务启动后，用 quickstart 扮演业务服务器：创建应用、签发登录 token、建房并申请进房 token。`make up` 之后 `core` 和 `chatroom` 不再向宿主机发布 gRPC 端口，宿主机已经连不上 `localhost:9001`，因此在 compose 网络中运行：

```bash
docker compose -f deploy/docker-compose.yml run --rm tools-quickstart -uid 10001 -room 1001
```

本机 `go run ./cmd/core -config configs/core.yaml` 时，quickstart 仍使用默认的 `localhost:9001`：

```bash
go run ./examples/quickstart -uid 10001 -room 1001
```

它会打印一条可直接执行的命令，用 WebSocket 连上网关、进入房间并发送一条消息：

```bash
go run ./examples/gateway-client -transport ws -app <app_id> -uid 10001 \
    -token <login_token> -room 1001 -room-token <room_token> -msg hello
```

再开一个终端，用另一个 uid 重复上面两步，就能看到两个客户端互相收到房间消息。

也可以直接用浏览器。本机 `go run ./cmd/core -config configs/core.yaml` 时，再开一个终端：

```bash
go run ./examples/web
```

`examples/web` 对着 compose 使用时同样要加入该网络，并传 `-core core:9001`（chatroom 使用 `-chatroom chatroom:9002`）。浏览器访问的 `8090` 由这个示例进程自己监听，不在本任务映射网关 `8080`。

打开 <http://localhost:8090>，两个浏览器窗口用不同昵称进入同一个房间号即可对话。页面走的是网关的 WebSocket 协议，`examples/web` 只负责创建应用、签发令牌。

协议和架构说明在 [docs/protocol.md](docs/protocol.md)、[docs/architecture.md](docs/architecture.md)。

## 消息抄送 webhook

在应用上配置回调地址（`msg_cp_url` 房间事件、`text_cp_url` 文本消息、`custom_cp_url` 自定义消息）后，webhook 服务会把对应消息以 `POST application/json` 发送过去，请求头：

| Header | 说明 |
|---|---|
| `App-Id` | 应用 ID |
| `Nonce` | 64 位随机串 |
| `Cur-Time` | Unix 时间戳（秒） |
| `Check-Sum` | `hex(sha1(AppSecret + Nonce + Cur-Time + 请求体))`，小写十六进制，直接拼接无分隔符 |

业务服务器用同样的方式计算并比对 `Check-Sum` 即可校验来源。网络错误、HTTP 429 和 5xx 会按配置指数退避重试；投递语义为至多一次。

## 目录结构

```text
api/proto/        protobuf 定义（client：客户端协议；service：服务间 gRPC）
api/gen/          buf 生成的 Go 代码（make proto）
cmd/<service>/    各服务入口
internal/<svc>/   各服务实现
pkg/              公共组件：配置、日志、Redis、Kafka、MySQL、gRPC、etcd 服务发现、ID 生成
configs/          本机运行的示例配置（localhost，不启用 etcd）
deploy/           docker compose 及其配置（启用 etcd）
examples/         gateway-client 命令行客户端、quickstart、浏览器 demo
docs/             架构与协议说明
.agents/skills/   Codex、Cursor 等使用的技能
.claude/skills/   Claude Code 使用的同一份技能
AGENTS.md         Go 编程规范
CLAUDE.md         Claude Code 进入上述规范的入口
```

## 本地开发

```bash
make build         # 编译全部服务到 bin/
make test          # go test -race ./...
make lint          # go vet + golangci-lint
make tools proto   # 安装 buf 与插件并重新生成 api/gen
```

本机直接运行服务时使用 `configs/*.yaml`，例如 `go run ./cmd/core -config configs/core.yaml`。密码等敏感项通过 `${MYSQL_PASSWORD}`、`${REDIS_PASSWORD}` 等环境变量注入。

## 路线图

- [x] 鉴权、房间、长连接网关、消息抄送
- [x] etcd 服务发现
- [x] 协议与架构文档、Web 端 demo
- [ ] 私聊服务
- [ ] 群聊服务
- [ ] 独立 HTTP 网关
- [ ] 历史消息存储与查询
- [ ] 对象存储抽象（恢复客户端日志上传）
- [ ] 压测报告

## 许可证

[Apache License 2.0](LICENSE)
