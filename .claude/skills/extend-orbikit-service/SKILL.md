---
name: extend-orbikit-service
description: 在 orbikit 分布式平台中新增一个可独立部署的服务进程。用于使用者自己的业务服务，也用于规划中的私聊、群聊、独立 HTTP 网关。按现有 core/chatroom/webhook 的进程模型接入 etcd。不要用于只在已有服务里加一个 RPC、客户端命令或字段；那种任务用 add-orbikit-service-feature。任务没有点名私聊、群聊或 HTTP 网关时，不要提前实现它们。
---

<!-- 同一份正文：.agents/skills/extend-orbikit-service/SKILL.md 与 .claude/skills/extend-orbikit-service/SKILL.md。改一处必须同时改另一处。 -->

# 在 orbikit 中新增业务服务

本 skill 教你如何把一个**新的进程**接入 [EdwinChanYi/orbikit](https://github.com/EdwinChanYi/orbikit)。先读仓库根目录 `AGENTS.md`、`docs/architecture.md`、`docs/protocol.md`，再动手。本仓库已有的四个进程是模板，不要另起一套目录或启动方式。

当前已提供：`core`（鉴权/应用）、`chatroom`（房间）、`gateway`（长连接网关）、`webhook`（消息抄送）。私聊、群聊、独立 HTTP 网关是规划中的平台服务，和使用者自己的业务服务用同一套进程模型。只有用户点名要做其中某一个时才落地那个服务，不要一次把三个都写出来，也不要改现有 `gateway` 的 HTTP 短连接来冒充 HTTP 网关。

接口没确认之前不要写 proto、`api/gen`、`cmd/`、`internal/`、两份 YAML、compose 服务项或测试。先给出服务列表和每个 RPC 的名字、请求、回复、错误情况。有不清楚的点就问具体问题。用户明确同意这份列表之后再写文件。仅仅打开本 skill 不算同意。

服务名必须匹配 `^[a-z][a-z0-9]*$`。拒绝 Go 关键字，以及忽略大小写后与已有 `cmd` 目录相同的名字（现有是 `core`、`chatroom`、`gateway`、`webhook`）。拒绝时不写文件，也不覆盖已有服务。

## 1. 先判断：是否真的需要新进程

符合下面任一条件才新建进程，否则改用 `add-orbikit-service-feature` 扩展已有服务：

- 需要独立扩缩容、独立失败域，或要让业务方单独部署；
- 主要依赖与现有服务不同（例如只要 Kafka 不要 MySQL，或反过来）；
- 职责在架构上已经划出去：`core` 不感知房间，`chatroom` 不碰客户端连接，`gateway` 不保存房间状态，`webhook` 不产生房间状态。新服务也要能一句话说清「做什么 / 不做什么」。

不要把业务逻辑塞进 `gateway` 或新建一个什么都做的 `server` 进程。业务服务器（调用方）仍然通过 gRPC 访问平台，不要要求它改走网关长连接。

## 2. 对照现有进程选模板

| 你的服务更像 | 抄哪套 | 典型依赖 |
|---|---|---|
| 有 MySQL 状态、被其他服务或业务方用 gRPC 调用（鉴权、配置、账号） | `cmd/core` + `internal/core` | MySQL、Redis、`pkg/appnotify`（若要广播配置） |
| 有复杂业务状态，结果要推给在线用户 | `cmd/chatroom` + `internal/chatroom` | MySQL、Redis、Kafka 生产者、作为 `Core` 客户端 |
| 只消费事件并回调外部 HTTP / 无用户连接 | `cmd/webhook` + `internal/webhook` | Kafka 消费者、可选 Redis、作为 `Core` 客户端 |
| 面向终端用户的接入层 | 优先扩展现有 `gateway`，不要再做一个并行长连接网关 | TCP/WS 协议已在 `internal/gateway/protocol` 定死 |

新建服务的依赖注入学 **core / gateway / webhook 的构造函数**，不要复制 chatroom `logic.Init` 那种包级全局状态。

模块路径始终是 `github.com/EdwinChanYi/orbikit`。进程短名用小写单词，并在 `cmd/<name>/`、`internal/<name>/`、Makefile 的 `SERVICES`、Docker `SERVICE` 参数、compose 服务名之间保持一致。不要用已占用名：`core`、`chatroom`、`gateway`、`webhook`。

## 3. 推荐目录布局

以新服务 `example` 为例（把名字换成真实短名）：

```text
api/proto/service/example_service.proto
cmd/example/main.go
internal/example/config.go
internal/example/config_test.go
internal/example/server/server.go
internal/example/server/server_test.go
internal/example/logic/          # 或 store/、delivery/，按复杂度拆，不要把业务写进 cmd
configs/example.yaml
deploy/configs/example.yaml
```

需要 Redis 键时集中放 `internal/example/rediskey/` 或 `logic/keys.go`，与现有服务一致。需要 Kafka 生产时学 `internal/chatroom/event/producer.go`；需要消费时学 `pkg/kafkax.Consume` 和 `internal/webhook` 或 `internal/gateway/push`。

不要从其他服务的 `internal/` 包 import 类型。进程之间只通过 gRPC、Redis、Kafka 通信。公共能力只用 `pkg/config`、`pkg/log`、`pkg/grpcx`、`pkg/discovery`、`pkg/mysqlx`、`pkg/redisx`、`pkg/kafkax`、`pkg/idgen`、`pkg/appnotify`。

## 4. 定义并生成 gRPC 接口

1. 在 `api/proto/service/` 新增 `*_service.proto`：

```proto
syntax = "proto3";

package orbikit.service;

option go_package = "github.com/EdwinChanYi/orbikit/api/gen/servicepb;servicepb";

import "service_comm.proto";

// 每个 RPC 的回复都带 UnifiedResponse。
service Example {
    rpc Echo (orbikit.service.EchoRequest) returns (orbikit.service.EchoReply) {}
}
```

2. 请求里预留 `string msg_trace_id`（现有服务普遍这么做），便于和网关 `Request.TraceID`、chatroom `reqctx.MsgID` 对齐。
3. 业务错误码加到 `api/proto/service/service_comm.proto` 的 `ErrorCode`。遵循文件顶部的三段式编号。已占用：`200100000` 起为 core，`200200000` 起为 chatroom，`200300000` 是历史 `ROOM_ROUTER_NOT_AVAILABLE_UPSTREAM`。给新模块分配新的中三段，不要挪用空号。成功是 `SUCCESS = 0`；内部故障可复用 `SERVER_INTERNAL_ERROR`、`PARAM_ERROR`。
4. 不要手写 `api/gen/`。在仓库根执行：

```bash
make tools proto
# 即 PATH="$GOBIN:$PATH" buf generate
```

CI 会跑 `buf generate && git diff --exit-code api/gen`，插件版本必须与 `Makefile` / `.github/workflows/ci.yml` 一致：`buf@v1.34.0`、`protoc-gen-go@v1.34.2`、`protoc-gen-go-grpc@v1.4.0`。`buf.gen.yaml` 的 `out` 是 `api/gen`，`opt` 是 `module=github.com/EdwinChanYi/orbikit/api/gen`。

5. 若该服务还要被客户端经网关调用，再改 `api/proto/client/`（`cmd_id.proto`、请求/响应消息），给命令分配**未占用**的号段。房间命令 1001–1999、推送号 `1004`/`1060`/`1070`/`802`/`997`、历史查询 `2010`/`2011` 不要占用。然后按 `add-orbikit-service-feature` 把命令挂到 `internal/gateway/handler/routes.go`。

## 5. 实现服务端

`internal/<name>/server` 实现生成的接口：

```go
type Server struct {
    servicepb.UnimplementedExampleServer
    // 依赖放在字段上，由 New 注入
}

var _ servicepb.ExampleServer = (*Server)(nil)

func New(...) *Server { return &Server{...} }
```

必须嵌入 `UnimplementedXxxServer`，并保留编译期断言。

业务错误写进 `reply.Ret *servicepb.UnifiedResponse`（学 `internal/core/server` 的 `successResponse` / `codeResponse`，或 `internal/chatroom/server` 的 `ret` / `failed`）。只有存储崩溃、依赖不可用才返回 gRPC `status.Error(codes.Internal, ...)`。不要用 gRPC status 表示「记录不存在」这类可预期结果。

若需要校验应用是否存在：用 `pkg/grpcx.DialDiscovered` 得到 `servicepb.CoreClient`。部署目标写 `etcd:///core`，本机配置写 `localhost:9001`。调用 `IsAppValid` 或 `GetClientLoginTokenInfo`，不要直连 core 的 MySQL。令牌签发/校验也只走 `Core` 的 RPC（`ApplyClientLoginToken`、`GetClientLoginTokenInfo`、`DeleteClientLoginToken`）。`etcd:///` 目标在没有 etcd registry 时会直接失败，本机测试不要用这种目标。

若结果需要到达在线用户：

- 不要从新服务直连 `gateway` 进程内的 `session.Manager`。
- 学 chatroom：用 `pkg/kafkax.NewSyncProducer` 把 protobuf 写入 topic。已有 topic 含义固定：
  - `orbikit.room.broadcast`：`servicepb.ChatroomBcst`，网关按 `IncludedUid` 推给本机在房成员（`CmdId_CHATROOM_RECEIVE_MESSAGE` = 1070）；
  - `orbikit.room.fullserver`：同类型，推给本机该 `appId` 的全部在线用户；
  - `orbikit.room.kicked`：`servicepb.ChatRoomMemberBeKickedBcst` → `CHATROOM_HAS_BEEN_KICKED`（1060）；
  - `orbikit.room.event`：`servicepb.ChatRoomMiscEventMsg`（封禁断线、要求上传日志）。
- 新的推送语义请用**新 topic**（`orbikit.<域>.<事件>`），同时改网关 `internal/gateway/config.go` 的 `push.Topics`、`configs/gateway.yaml`、`deploy/configs/gateway.yaml`、`push.Dispatcher.Handle`，以及 `deploy/docker-compose.yml` 里 `kafka-init` 的建 topic 列表。网关消费组必须仍然**每实例唯一**。
- 客户端包体的映射加在 `internal/gateway/convert`，由 `push` 调用，不要在 dispatcher 里手填 `clientpb` 字段。

若要抄送业务服务器：优先复用 webhook 消费广播的机制（应用上的 `text_cp_url` / `custom_cp_url` / `msg_cp_url`）。只有抄送语义完全不同才在新服务里自建投递，并复用 `internal/webhook/delivery/sign` 的签名算法，以免业务方对每个服务实现一种验签。

Redis 键：新前缀，集中定义，写入测试锁定格式。Lua 多键操作必须带同一哈希标签（学 `{CORE_SERVICE}` 或 `{MARS_AGENT:%s}`）。不要改已有 `CHATROOM_SERVICE:`、`MARS_AGENT:`、`{CORE_SERVICE}` 键。需要过期回调时，确认 Redis `notify-keyspace-events` 包含 `Ex`，并学 `internal/chatroom/watcher`。

MySQL：用 `pkg/mysqlx.New`，DSN 含 `charset=utf8mb4&parseTime=true`。启动时 `AutoMigrate`。表名/列名/索引名一经落地不得为了美观重命名。多租户列必须有 `app_id`。

## 6. 配置、入口、装配

`internal/<name>/config.go` 提供 `Config`、`DefaultConfig()`，必要时 `Validate()`。YAML tag 用 snake_case。默认监听地址不要和已占用端口冲突：

- gRPC：`:9001` core、`:9002` chatroom、`:9004` webhook（`:9003` 空闲但不要假定一定可用）
- 网关：`:8000` TCP、`:8001` WS、`:8080` HTTP 短连接
- 调试：配置里 `debug_addr` 为空则不启动；注释示例是 `127.0.0.1:6061`–`6064`

`cmd/<name>/main.go` 按现有入口抄（推荐 `cmd/core/main.go` 或 `cmd/webhook/main.go`）：

1. `flag.String("config", "configs/<name>.yaml", ...)`
2. `cfg := <pkg>.DefaultConfig(); config.Load; Validate; log.Init(cfg.Log, "<name>"); defer log.Sync()`
3. `signal.NotifyContext` 处理 `SIGINT`/`SIGTERM`
4. `discovery.Open(cfg.Etcd)`。未配置 `etcd.endpoints` 时它返回 nil registry，本机直连仍可用。下游用 `grpcx.DialDiscovered`，不要新写客户端。
5. 需要 MySQL、Redis、Kafka 时再连接。监听测试调用的那条路径不要连接它们。
6. `gs := grpcx.NewServer()`，按这次 proto 的 Register 函数注册，不要假设进程名、文件名和 Register 函数同名。然后 `net.Listen`，再 `discovery.Bind`。`Bind` 在 registry 为 nil 时是空操作。
7. `Serve`，并在进程上下文取消后于 `ShutdownTimeout`（默认 10s）内 `GracefulStop`，超时 `Stop()`。Kafka / Redis / DB 连接成对关闭。

`grpcx.NewServer` 已注册 health。部署环境的下游地址写成 `etcd:///core`。多副本不要把 `register.advertise_addr` 写成同一个 DNS 名，留空让每个实例登记自己的地址。单副本 compose 可以写 `example:9010` 这种服务名。

监听端口从 `:9010` 起分配，跳过 `:9001`、`:9002`、`:9004`、`:8000`、`:8001`、`:8080`。同一次新增的多个服务端口互不相同。本地配置、deploy 配置和 compose 端口映射用同一个端口。

`configs/<name>.yaml` 不写 `etcd.endpoints`。`deploy/configs/<name>.yaml` 写 `etcd.endpoints: ["etcd:2379"]` 和 `register.name: <name>`。

配置文件要两份，内容几乎相同，仅主机名不同：

- `configs/<name>.yaml`：本机，`localhost`
- `deploy/configs/<name>.yaml`：compose，主机名 `mysql`、`redis`、`kafka`、`etcd`、`core`、`chatroom`

密钥只用 `${MYSQL_PASSWORD}`、`${REDIS_PASSWORD}` 等。文件头用中文说明职责，与现有 YAML 一致。

## 7. 接入构建、编排、CI

改这些地方，漏一处本地或 CI 就会过不去：

1. 根目录 `Makefile` 的 `SERVICES := core chatroom gateway webhook` 追加上新短名。`make build` 会执行 `go build -o bin/$@ ./cmd/$@`。
2. `Dockerfile` 已通用：`docker build --build-arg SERVICE=<name> -t orbikit-<name> .`。不要为每个服务复制一份 Dockerfile。`CGO_ENABLED=0`，入口是 `/app/app`，配置通过 `-config /app/configs/<name>.yaml` 传入。
3. `deploy/docker-compose.yml`：
   - 复用顶部 `x-service` 锚点（`build.context: ..`，只读挂载 `./configs`，注入 `MYSQL_PASSWORD` / `REDIS_PASSWORD`）；
   - 设置 `build.args.SERVICE`、`command: ["-config", "/app/configs/<name>.yaml"]`、需要暴露的端口；
   - `depends_on` 按实际依赖声明 health，注册进 etcd 的服务要等 `etcd` healthy（另外按需 `mysql` / `redis` / `kafka-init` / `core`）；
   - 若有新 Kafka topic，加入 `kafka-init` 的 `for t in ...` 列表（现为 `orbikit.room.broadcast|fullserver|kicked|event`，3 分区）。
4. `.github/workflows/ci.yml` 的 `docker` job 矩阵 `service: [core, chatroom, gateway, webhook]` 追加上新短名。
5. 根 `README.md` 的服务表、架构图、目录说明补一行；详细职责写进 `docs/architecture.md`。不要把启动说明只留在 skill 里。

服务发现是 etcd，不是 DNS，也不是 Redis pub/sub。新的 gRPC 服务必须 `discovery.Bind`，调用方用 `DialDiscovered`。应用配置变更仍走 Redis 频道 `orbikit:app_config`（core 发布，webhook 等订阅）。新服务若缓存了应用配置，用 `pkg/appnotify` 订阅，不要再维护一份实例地址清单。Redis Cluster 下 pub/sub 只在单节点传播。

CI 的 docker 矩阵可以加上新短名，让镜像能编过。验收门是 `go test -race ./...` 里的监听测试，不是把新服务推进 compose 就算完成。监听测试加载本地配置，看到监听地址后取消启动函数自己的上下文。不要另起一个只等信号、并且先 Ping MySQL 的 main 来充当这个测试。

## 8. 测试与验证

最少包含：

- `config_test.go`：默认值、`Validate` 拒绝空 brokers / 空 DSN 等；
- `server_test.go`：编译期接口断言、参数错误在依赖未初始化时也能返回 `PARAM_ERROR` 或你定义的业务码、成功路径用假依赖；
- Redis 键格式测试（若有）；
- 若走网关：更新 `internal/gateway/handler/handler_test.go` 的 `TestRoutes`（当前期望 27 条），并补命令的成功/失败用例。

验证命令（在仓库根）：

```bash
make proto
make test          # go test -race ./...
make lint          # go vet + golangci-lint
make build         # 或：go build -o bin/<name> ./cmd/<name>
```

本机联调：先起依赖（或 `make up`），再 `go run ./cmd/<name> -config configs/<name>.yaml`。业务方调用学 `examples/quickstart/main.go`：`grpcx.Dial`、`servicepb.NewXxxClient`、检查 `ret.GetErrorCode() == servicepb.ErrorCode_SUCCESS`。若经过网关，用 `examples/gateway-client` 或 `examples/web` 做一条真实长连接。

compose 验证：把新服务加进 `deploy/docker-compose.yml` 后执行 `make up`，确认 health 依赖顺序正确，且不会让多个 **gateway** 实例共用一个 `kafka.group_id`。

## 9. 检查清单

- [ ] 职责能写成「做什么 / 不做什么」，没有侵入 gateway 保存业务状态
- [ ] proto、错误码、生成代码已提交，且 `make proto` 无 diff
- [ ] `cmd/`、`internal/`、两份 YAML、Makefile、compose、CI 矩阵已同步
- [ ] 只用 `pkg/*` 和 gRPC/Redis/Kafka/etcd 与其他进程交互；deploy 配置有 `register.name`，本机配置不写 `etcd.endpoints`
- [ ] 监听测试在没有 MySQL、Redis、Kafka、etcd 时能看到端口并随上下文取消结束
- [ ] 需要推送时走 Kafka + 现有或新增的 gateway dispatcher，且网关消费组唯一
- [ ] 没有改动已有 Redis 键、topic 名、字段号、错误码数值
- [ ] `go test -race ./...` 与 `make lint` 通过
- [ ] 没有把密钥写进 YAML，没有实现未要求的私聊/群聊/独立 HTTP 网关
