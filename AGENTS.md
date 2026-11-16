# AGENTS.md

本文给在 [orbikit](https://github.com/EdwinChanYi/orbikit) 里写代码的 AI 助手和贡献者。规范必须贴合本仓库已有实现，不要套用通用的 Go 教程式建议。

orbikit 提供生产级的分布式服务端。业务方在上面接自己的产品，不从零搭登录、房间和长连接。已经提供的平台进程是：

- `core`：鉴权与应用（登录令牌、封禁、应用密钥）
- `chatroom`：房间服务
- `gateway`：长连接网关（TCP 与 WebSocket）。它还有一条与长连接共用命令的 HTTP 短连接，那不是独立的 HTTP 网关
- `webhook`：把房间广播签名抄送给业务服务器

规划中的平台服务是私聊、群聊、独立 HTTP 网关。它们和使用者自己的业务服务一样，按 `extend-orbikit-service` 加进程，不要塞进 `gateway`。在已有进程里加 RPC、命令或字段，按 `add-orbikit-service-feature`。任务没有点名某个服务时，不要提前实现私聊、群聊或 HTTP 网关。

这两份技能各有一份正文，放在两个目录里，内容必须逐字相同：

- `.agents/skills/<name>/SKILL.md`：Codex、Cursor，以及扫描 `.agents/skills` 的其他 agent
- `.claude/skills/<name>/SKILL.md`：Claude Code

`<name>` 是 `extend-orbikit-service` 或 `add-orbikit-service-feature`，并且和 frontmatter 的 `name` 相同。不要再把技能只放在 `.cursor/skills`。Claude Code 的编程规范入口是根目录 `CLAUDE.md`，正文仍以本文件为准。

更完整的运行时说明见 [docs/architecture.md](docs/architecture.md) 和 [docs/protocol.md](docs/protocol.md)。

仓库状态：v0.1 开发中，Go 1.27.4，模块路径 `github.com/EdwinChanYi/orbikit`，许可证 Apache-2.0。接口仍可能调整，但已部署的 Redis 键、Kafka topic、protobuf 字段号和错误码数值视为兼容面，不能为了「更干净」而改掉。

## 1. 仓库地图

四个进程，不是四个库：

| 进程 | 入口 | 实现 | gRPC 服务 | 默认端口 | 职责 |
|---|---|---|---|---|---|
| `core` | `cmd/core` | `internal/core` | `servicepb.Core` | gRPC `:9001` | 多租户应用（`appId` / `appSecret`）、登录令牌、用户封禁 |
| `chatroom` | `cmd/chatroom` | `internal/chatroom` | `servicepb.Chatroom` | gRPC `:9002` | 房间、成员、角色、麦位、机器人、房间消息；向 Kafka 生产事件 |
| `gateway` | `cmd/gateway` | `internal/gateway` | 无（它是 gRPC 客户端） | TCP `:8000`、WS `:8001`（`/ws`）、HTTP `:8080` | 长连接接入、鉴权、命令路由、Kafka 推送、HTTP 短连接 |
| `webhook` | `cmd/webhook` | `internal/webhook` | `servicepb.MsgCopy` | gRPC `:9004` | 消费房间广播，签名后 POST 到业务服务器 |

依赖是 MySQL、Redis、Kafka。业务服务器不接入长连接，只调 `Core` / `Chatroom` 的 gRPC，并用 webhook 收抄送。

目录约定：

```text
api/proto/client/     客户端 <-> 网关 的 protobuf（生成到 api/gen/clientpb）
api/proto/service/    服务间 gRPC（生成到 api/gen/servicepb）
api/gen/              由 buf 生成，禁止手改
cmd/<service>/        各进程 main
internal/<service>/   该进程的实现，不要被其他进程直接 import（gateway 引用 chatroom 包是不允许的）
pkg/                  跨进程公共组件，只放基础设施，不放房间/鉴权业务
configs/              本机运行示例（localhost）
deploy/               docker compose；deploy/configs/ 由 configs/ 派生，通常只改主机名
examples/             quickstart（扮演业务服务器）、gateway-client、web demo
docs/                 架构与协议，给人读
```

规范只维护这一份 `AGENTS.md` 和上面两份 skill。不要再写一份互相冲突的 `CONTRIBUTING` 或第二套编程规范。改 skill 时两个目录一起改。

## 2. 命名

### 包、目录、进程

- 进程名、`cmd/` 目录、`internal/` 目录、Makefile 的 `SERVICES` 使用同一组短名：`core`、`chatroom`、`gateway`、`webhook`。
- 包名用小写单词，与目录名一致：`package handler`、`package server`、`package logic`、`package store`。不要用 `pkg/` 前缀当包名。
- 新进程不要用 `server`、`service`、`app` 这种过于泛的目录名。
- `main` 包的文件头注释写成 `// Command core runs the core service: ...`，与现有四个入口一致。

### Go 标识符

- 导出符号用 MixedCaps：`DefaultConfig`、`NewTokenService`、`ApplyClientLoginToken`。
- 缩写保持全大写或按本仓库已有写法：`UID`、`ID`、`TTL`、`DSN`、`QPS`、`RPC`、`HTTP`、`WS`、`TCP`。配置结构体里已有 `GRPCAddr`、`CoreAddr`、`ChatroomAddr`。
- 本地变量可用 `appID`、`roomID`、`uid`、`rdb`、`st`、`gs`。不要引入与附近代码冲突的别名（例如在 `chatroom` 里把房间改叫 `roomSvc` 又同时出现 `ChatroomId`）。
- 请求/回复类型跟 proto 走：`CreateChatroomRequest`、`EnterChatroomReply`。handler 里客户端类型来自 `clientpb`，下游请求来自 `servicepb`，不要混用一套结构体。
- 同一概念在不同层的名字本来就不统一，新增代码跟随所在包，不要「统一重命名」：
  - Go 配置/日志字段常用 `AppID`、`UID`、`RoomID`。
  - proto / YAML 用 snake_case：`app_id`、`uid`、`chatroom_id`、`msg_trace_id`。
  - chatroom 的房间在 proto 里经常是 `chatroom_id` / `ChatroomId`，网关 handler 局部变量常用 `roomID`。
- 错误码、命令号、枚举名即便有历史拼写问题也不得改数值或符号名。已存在的例子：`CHATROOM_MEMBER_KIKY`、`CHATROOM_APPLAY_TOKEN_ROLEIDS_NOT_EXISTS`、`ROOM_ROUTER_NOT_AVAILABLE_UPSTREAM`。只在新符号上把拼写写对。

### 配置、日志、键

- YAML 与结构体 tag 一律 snake_case：`grpc_addr`、`shutdown_timeout`、`enter_token_ttl`、`cache_expire`。
- 密钥用环境变量占位，不要写进仓库：`${MYSQL_PASSWORD}`、`${REDIS_PASSWORD}`、`${MODERATION_TOKEN}`。`pkg/config.Load` 会先 `os.ExpandEnv` 再解析 YAML。
- 日志字段用 snake_case：`app_id`、`trace_id`、`msg_trace_id`、`conn_id`、`room_id`、`grpc_addr`。用 `pkg/log.L()`（zap），不要引入第二套日志库。
- Redis 键格式已经在线和历史部署中使用，**禁止修改**。新键跟所属服务现有前缀走，并集中写在该服务的 keys 文件里：
  - core：`internal/core/logic/keys.go`，格式 `{CORE_SERVICE}:INFO_2_LOGIN_TOKEN:APP_ID:%s:UID:%d` 等，`{CORE_SERVICE}` 哈希标签是给 Redis Cluster 上的 Lua 用的。
  - chatroom：`internal/chatroom/rediskey/keys.go`，主前缀 `CHATROOM_SERVICE:...`，也有短键如 `CRULock:`、`crAfterDealQueue`。
  - gateway：`internal/gateway/store/keys.go`，主前缀 `MARS_AGENT:...`（历史名，不要改成 `GATEWAY`）。
  - 机器 ID 租约：`orbikit:machine_id:<service>:<id>`，在 `pkg/idgen`。
- Kafka topic 现有四个，名称不要改：`orbikit.room.broadcast`、`orbikit.room.fullserver`、`orbikit.room.kicked`、`orbikit.room.event`。新 topic 继续用 `orbikit.<域>.<事件>`。
- Redis pub/sub 频道默认 `orbikit:app_config`（`pkg/appnotify.DefaultChannel`）。

### protobuf

- 文件放在 `api/proto/client` 或 `api/proto/service`。`buf.yaml` 把这两个目录各算一个 module，service 侧 `import "service_comm.proto"` 这种不带路径的写法不要改。
- `option go_package` 必须是：
  - 客户端：`github.com/EdwinChanYi/orbikit/api/gen/clientpb;clientpb`
  - 服务间：`github.com/EdwinChanYi/orbikit/api/gen/servicepb;servicepb`
- 包名分别是 `orbikit.client`、`orbikit.service`。
- 字段用 snake_case，从 1 起编号；**已有字段号不得改用途或删除**。废弃只能 `reserved` 或注明不再使用。
- 新错误码遵循 `service_comm.proto` 顶部的三段式：`来源(1 位) + 模块(3 位) + 具体(5 位)`。`2` 是平台业务流程异常。已占用模块：`000` core（`200100000` 起）、`001` chatroom（`200200000` 起）、`002` 仅有历史值 `ROOM_ROUTER_NOT_AVAILABLE_UPSTREAM = 200300000`。新业务模块接着分配，不要复用这些区段的空号当「别的服务」。
- 客户端命令号在 `api/proto/client/cmd_id.proto`。房间请求占用 1001–1999（`CHATROOM_BUSINESS_BEGIN = 1000` 不可当命令用）。服务端推送已用 `1004`、`1060`、`1070`、`802`、`997` 等。历史查询 `2010`/`2011` 已保留但网关返回不支持。新业务请开新的号段，不要挤进 1000–1999 除非它确实是房间命令。

## 3. 实际在用的设计模式

只使用下面这些已出现的模式。不要为了「更优雅」引入完整的清洁架构、全局事件总线或新的 DI 框架。

### 进程内分层，而不是跨进程的包引用

典型路径：

- **core**：`cmd/core` 装配 → `internal/core/server`（gRPC）→ `internal/core/logic` → `internal/core/store`（GORM）/ Redis。
- **chatroom**：`cmd/chatroom` 装配并调用 `logic.Init` → `internal/chatroom/server` → `internal/chatroom/logic` → `model` / Redis / `event.Producer`。
- **gateway**：`cmd/gateway` 装配 → `transport` 读帧 → `session` → `handler`（命令）/ `push`（Kafka 下行）。`convert` 负责 `servicepb` ↔ `clientpb`。`httpapi` 与长连接共用 `handler.Service`。
- **webhook**：`cmd/webhook` 装配 → Kafka `delivery.Dispatcher` / `Copier` / `Sender`；gRPC `MsgCopy.AppInfoUpdated` 只刷新缓存。

业务规则放在 `logic`（或 webhook 的 `delivery`），`server`/`handler` 只做校验、转换、调用和写回 `UnifiedResponse`。不要在 `cmd/` 里写业务，也不要把房间规则塞进 `pkg/`。

### 构造注入，chatroom 的 logic 是例外

- `core`、`gateway`、`webhook` 用构造函数注入依赖：`server.New(...)`、`handler.New(handler.Deps{...}, handler.Options{...})`、`delivery.NewCopier(...)`。
- `chatroom` 的 `logic` 包用包级变量，必须先 `logic.Init(logic.Deps{...})`。改 chatroom 时跟随这一约定，不要在同一个包里再做一套构造函数并行存在。
- **新建服务优先学 core / gateway 的构造注入**，不要再复制 `logic.Init` 这种隐式全局状态。
- 测试里用窄接口或嵌入客户端再覆盖方法，例如 `handler_test.go` 的 `fakeChatroom` 嵌入 `servicepb.ChatroomClient` 只实现 `SendChatroomMsg`；core 测试用 `fakeAppStore`。

### 小接口，只抽象真正的接缝

仓库几乎不为每个 struct 建接口，只在需要替换或测试的地方抽象：

- `core/server.Notifier`：`Publish(ctx, *servicepb.AppInfoUpdatedRequest)`
- `handler.IDGenerator`：`Next() uint64`、`MachineID() uint16`
- `moderation.Moderator`：`CheckText(...)`
- `session.Handler`：`HandlePacket`、`OnClose`

新增接口保持这种窄定义。实现 gRPC 服务时加上编译期断言，与现有代码一致：

```go
var _ servicepb.CoreServer = (*Server)(nil)
```

服务结构体必须嵌入对应的 `UnimplementedXxxServer`，这样后续在 proto 里加 RPC 时旧实现会以 Unimplemented 失败，而不是编译失败以外的意外行为。

### 配置：默认值 + YAML + 校验

每个服务在 `internal/<svc>/config.go` 提供：

1. `Config` 结构体，字段带 `yaml` tag 和注释；
2. `DefaultConfig()`，在读文件前填默认值；
3. 需要启动前置条件时实现 `Validate()`（`chatroom`、`gateway` 已有）。

`cmd/<svc>/main.go` 固定为：`flag -config`（默认 `configs/<svc>.yaml`）→ `DefaultConfig` → `config.Load` → 可选 `Validate` → `log.Init(cfg.Log, "<service>")`。敏感项只通过环境变量注入。本机配置放 `configs/`，compose 配置放 `deploy/configs/`，后者通常只把 `localhost` 换成 `mysql` / `redis` / `kafka` / `core` / `chatroom`。

### 路由表（仅网关命令）

客户端命令在 `internal/gateway/handler/routes.go` 用 `route(cmd, url, empty, handler)` 注册。长连接按 `CmdId` 分发，HTTP 短连接按同一张表的 `URL` 分发。`AUTH`（800）**不准**进这张表，只走长连接的 `Service.auth`。`LOGOUT` 是目前唯一 `AllowEmpty` 的命令。新增命令必须同时改 `cmd_id.proto`、这张表、处理函数，以及 `TestRoutes`（当前断言 27 条路由、URL 以 `/base/service/` 开头、URL 不重复）。

### 转换层

`internal/gateway/convert` 是 `servicepb` 与 `clientpb` 的唯一映射点（错误码、消息、成员、麦位、踢人广播等）。下游错误经 `rpcRet` → `convert.ErrorCode` 再回给客户端。新的双向字段映射加在这里，不要在 handler 或 push 里再写一套。

### 错误与状态码分层

业务结果放在回复的 `ret`（`servicepb.UnifiedResponse` 或 `clientpb.UnifiedResponse`）里，**不要**用 gRPC status 表示「房间不存在」「令牌无效」这类可预期错误。gRPC `status.Error(codes.Internal, ...)` 或 `codes.Unimplemented` 只用于存储失败、未实现接口等基础设施问题（见 `core/server.internalError`、`UploadClientLog`）。

chatroom 的 server 辅助函数约定：

- 先把 `reply.Ret` 设成 `CHATROOM_SERVICE_ERROR`，成功再改成 `SUCCESS` 或具体业务码；
- `failed(...)`：有 `error` 时保持默认码并让调用方把 `error` 向上返回；非成功的 `ErrorCode` 写入 `reply.Ret`；
- `recoverTo` 必须直接 `defer`，把 panic 变成 `PARAM_ERROR`（`ErrorMsg` 仍是 `CHATROOM_SERVICE_ERROR` 的名字，这是已有客户端契约，不要「修」）；
- `newCtx` 使用 `context.WithoutCancel(goCtx)`，因为多步更新不能在调用方取消时做到一半停下。

网关：解包失败、空包、未知命令等用 `clientpb.ErrorCode`；下游 RPC 失败用 `rpcRet` 打日志并映射。协议级失败（未 AUTH、重复 AUTH、包体超限）会断开连接；**业务错误只回错误码，不断开**。

### 事件驱动与水平扩展

- chatroom 用 `event.Producer` 同步写入 Kafka（`sarama.WaitForAll` + hash 分区）。网关每个实例必须有**唯一**的 `kafka.group_id`（空则 `ConsumerGroup()` 生成 `gateway-<hostname>-<rand>`），否则会漏推送。webhook 共用 `group_id: "webhook"`，一条消息只被一个实例回调。
- `kafkax.Consume` 在 handler 返回错误时仍然 `MarkMessage`：投递语义是至多一次，毒消息不能卡住分区。新的消费逻辑必须能接受丢失，并打错误日志。
- 应用配置变更：core 往 Redis 频道 `orbikit:app_config` 发布 `AppInfoUpdatedRequest`；所有 core 丢本地缓存，所有 webhook 更新抄送地址和限流。不要再给 webhook 维护一份实例地址清单。
- Redis Cluster 的 pub/sub 只在单节点内传播。文档和 `appnotify.Config` 已说明：集群部署时发布和订阅必须落在同一节点，或改用单机 Redis。不要假设频道会全集群广播。
- chatroom 多实例时，每个实例都会收到同一条 Redis 过期事件并可能各广播一次，处理必须幂等。

### 可插拔审核

网关 `moderation.Moderator`：`none` / 本地关键词 DFA / 业务方 HTTP。在 `SendChatroomMsg` 路径上、调用 chatroom 之前执行。新增审核方式实现该接口并由 `moderation.New` 构造，不要在 handler 里写死。

## 4. 代码复用

- 跨服务的基础设施只能进 `pkg/`：`config`、`log`、`grpcx`、`discovery`、`mysqlx`、`redisx`、`kafkax`、`idgen`、`appnotify`。新的公共能力先看这里有没有，不要在 `internal/<svc>` 再抄一份 Dial、日志、服务发现或 YAML 加载。
- 服务发现用 etcd，实现在 `pkg/discovery`。键是 `/orbikit/services/<服务名>/<实例>`，值是 `host:port`。租约默认 10 秒。`core`、`chatroom`、`webhook` 在 `net.Listen` 之后 `discovery.Bind`。`gateway` 只拨号，不注册。
- 调用别的服务用 `pkg/grpcx.DialDiscovered`。部署配置写 `etcd:///core` 这种目标；本机 `configs/*.yaml` 不填 `etcd.endpoints`，地址保持 `host:port`（例如 `localhost:9001`）。`etcd:///` 目标在 registry 为 nil 时 `DialDiscovered` 直接返回错误。`Dial` 仍可用于不需要发现的直连。两者都是 insecure 凭证加 `round_robin`。不要再引入另一套客户端或 Consul。
- 多副本把 `register.advertise_addr` 留空，让每个实例登记自己的地址。单副本 compose 可以写成服务 DNS 名，例如 `core:9001`。
- `pkg/grpcx.NewServer` 已经注册标准 health 服务并设置 keepalive，进程入口直接用它再 `RegisterXxxServer`。
- 新服务的监听测试必须能在没有 MySQL、Redis、Kafka、etcd 的 `go test -race ./...` 里结束。不要照抄现有 `cmd/chatroom` 在 `Listen` 之前 Ping 依赖、然后一直等信号的写法。测试调用的启动函数加载不写 `etcd.endpoints` 的本地配置，创建 gRPC server 并 `Register`，然后 `Listen`、`Bind`、`Serve`，并在这个函数自己的上下文取消后返回。`discovery.Open` 在未启用时返回 nil，`Bind` 为空操作。MySQL、Redis、Kafka 只在该服务的真实运行路径需要时再接，不要为了让监听测试变绿去连它们。
- 进程入口的信号处理、`ShutdownTimeout` 内 `GracefulStop`、可选 `debug_addr` 上的 `net/http/pprof`，都已有四个 `cmd/*/main.go` 模板，新服务照抄最近的一个（推荐 `cmd/core/main.go` 或 `cmd/webhook/main.go`），不要发明新的生命周期。
- ID 用 `pkg/idgen`（sonyflake + Redis 租约），不要自己做雪花或用 UUID 替换已有的 `msg_id` / `conn` id 体系。`idgen.Next` 在超过 sonyflake 寿命时会 panic，这是有意的，不要改成返回 error 却让调用方忽略。
- 时间、JSON 文本列、房间时间戳类型已经在 `internal/chatroom/model`（`Timestamp`、`StringMap`、`StringList`）。chatroom 的新表必须继续用 `Timestamp`（RFC3339Nano 存在 `CHAR(64)`），不要改成 `datetime` 以免 `AutoMigrate` 毁掉已有库。
- core 的表是普通 `datetime`（`internal/core/store/models.go`）。两套模式不要交叉。
- 网关到 chatroom/core 的字段转换复用 `internal/gateway/convert`；webhook 出站 JSON 复用 `internal/webhook/delivery/convert`。
- 不要把 `internal/chatroom` 的类型 import 进 `gateway` 或反过来。进程之间只通过 gRPC、Redis、Kafka 通信。

## 5. 注释怎么写

本仓库主体已经是英文 godoc（包注释、导出函数、配置字段、关键陷阱）。proto、YAML 示例和少数 RPC（如 `ApplyClientLoginToken`、`AppInfoUpdated`）带中文。**新增 Go 代码用英文写完整句子的 godoc**；对外 RPC 可以在英文说明后跟一句中文，但不要只写中文，也不要写「This function does X」这种重复名字的空话。

必须写注释的情况：

- 每个包的 package comment，说明职责，以及它**不**做什么（见 `docs/architecture.md` 的风格）。
- 所有导出类型、函数、重要导出字段。配置字段的注释要能直接帮助写 YAML。
- 「为什么」：兼容性、并发、关闭顺序、取消传播、幂等。例如 `newCtx` 为什么 `WithoutCancel`，`kafkax` 为什么出错仍提交位移，`rediskey` 为什么「格式不得更改」，`AppService` 为什么忽略调用方的 cancel（避免污染共享缓存）。

不要做的：

- 不要给显而易见的局部变量加行注释。
- 不要手改 `api/gen/**` 里的生成注释。CI 的 proto job 是 `buf generate && git diff --exit-code api/gen`，生成器版本必须与 `Makefile` / `.github/workflows/ci.yml` 一致（`protoc-gen-go@v1.34.2`、`protoc-gen-go-grpc@v1.4.0`、`buf@v1.34.0`）。
- 不要靠注释「预告」尚未落地的私聊/群聊/独立 HTTP 网关实现；协议里已有的预留（会话类型、`2010`/`2011`、`PcAppInfo` 表）只需在改到相关代码时保持兼容。

## 6. 测试规范

- 命令：`make test` 即 `go test -race ./...`。`make lint` 为 `go vet ./...` 再 `golangci-lint run`。CI（`.github/workflows/ci.yml`）还会 `go build ./...`、用 golangci-lint v2.15.0，以及 `docker build --build-arg SERVICE=...`。Go 版本以 `go.mod` 为准（当前 1.27.4）。
- 只使用标准库 `testing`。不要引入 testify / gomega。辅助函数调用 `t.Helper()`。
- 优先表驱动测试，命名 `TestXxx`，子用例 `t.Run`。参考 `internal/gateway/handler/handler_test.go`、`internal/core/logic/logic_test.go`、`internal/chatroom/server/server_test.go`。
- 单元测试默认不连真实 MySQL / Kafka。Redis 可用 `miniredis`（已是间接依赖）或对客户端打桩。网关 handler 测试里对 Redis 不可用的 `offlineStore` 是有意的：消息计数是 best-effort，失败不得影响主路径。
- gRPC 依赖用假客户端；存储用假 store。需要编译期保证时写 `var _ servicepb.ChatroomServer = (*Server)(nil)`。
- 行为契约要写成测试，而不是只写在注释里。已经覆盖、改时代码必须同步更新的例子：
  - 网关路由数量、URL 前缀、`AUTH` 不在路由表、`LOGOUT` 允许空包；
  - 历史消息命令返回不支持；
  - 审核拒绝 / 替换 / 后端失败的错误码；
  - Redis 键格式；
  - 登录令牌为 64 位小写十六进制；
  - 应用缓存在调用方 context 已取消时仍能加载，且错误不会被当成「应用存在」；
  - chatroom 的 `recoverTo` / `paramErr` 文案；
  - `UploadClientLog*` 返回 `codes.Unimplemented`。
- 纯逻辑（键解析、时间、转换、签名、限流）放在不需外部依赖的 `*_test.go`。涉及竞态的路径必须能在 `-race` 下通过（例如 `TestTTLCacheSingleLoad`）。
- 新增导出 API 或命令时，至少补：成功路径、主要业务错误码、参数校验（在依赖尚未初始化时也能跑，见 `TestParamChecksBeforeDependencies`）。
- linter 见 `.golangci.yml`：`errcheck`、`govet`、`ineffassign`、`staticcheck`、`unused`、`gofmt`、`goimports`、`misspell`。`api/gen` 已排除。不要关闭这些规则来迁就新代码。

## 7. 本仓库要特别小心的 Go 坑

### context 与取消

- chatroom 的 handler 使用 `context.WithoutCancel`，避免客户端断开导致房间状态写到一半。不要在 `logic` 的多步更新里改回尊重传入 cancel，除非你能证明每一步都可安全中止。
- 后台补救、断线后的清理、刷新会话，用独立的超时 context，例如 `logic` 里的 `bg()`（10s）、网关 `disconnectRPCBudget`（10s）、`refreshUserInfo`（5s）。不要把请求 ctx 传到请求返回之后仍运行的 goroutine。
- 共享内存缓存（`AppService`）不要直接用调用方可能已取消的 ctx 去打存储，否则一次取消会污染缓存或把查找失败当成「应用无效」。已有测试锁定了这个行为。
- 进程关闭：先停监听，再在 `ShutdownTimeout`（默认 10s）内 `GracefulStop` / 关掉 Kafka consumer / `sessions.CloseAll()`，超时则 `Stop()`。新的后台 goroutine 必须接入同一套 `ctx` / `WaitGroup`。

### 并发与锁

- 同一用户在网关上的 AUTH / 踢旧连接，走 `store.LockUser`（键 `MARS_AGENT:AUTHLOCK:...`）。跨网关挤下线靠 Redis 状态和 `MARS_AGENT:NEW_CONNECTION_EVENT` pub/sub，以及 `push.NewKickWatcher`。改登录路径时不要去掉这把锁。
- chatroom 有多把 Redis 锁（`rediskey.UserLock`、`ChatroomInfoLock`、`ChatroomProcessLock`、`ChatroomMicLock`），实现见 `internal/chatroom/lock`。拿锁失败对应 `CHATROOM_ROOM_USER_LOCKED` 等业务码。新的多步房间变更必须先看同文件已有锁，避免再加一把互相死锁的锁。
- `session.Manager` 是网关进程内连接表，推送只投递给**本机**连接。不要在 chatroom 里尝试直连客户端。
- 向 `log.L()` 或共享 map 写入时遵循现有代码：zap logger 是进程级原子指针，可并发用；自己加的缓存要像 `ttlCache` 一样考虑 stampede（`GetOrLoad` 单飞）。

### 错误处理

- 对外返回的业务错误用 `ErrorCode` + `ErrorMsg`。内部错误用 `fmt.Errorf("...: %w", err)` 包装，日志里带 `trace_id` / `msg_trace_id`。
- 不要用 `_ = fn()` 丢掉 `errcheck` 管得到的错误；现有代码只在关闭、尽最大努力的统计、或确实无法处理时显式忽略，并通常打 Warn。
- `errors.Is` / `errors.Join` 已在用（例如 Redis `Nil`、认证路径上的 `errors.Join`）。比较错误时不要用 `==`。
- chatroom 的 panic 被变成 `PARAM_ERROR`，不是 crash 进程。新的 chatroom RPC 必须 `defer recoverTo(...)`。其他服务没有这层保护，不要让它们 panic。

### 网络与协议

- 客户端帧是 20 字节大端包头 + protobuf 包体，见 `internal/gateway/protocol`。`HeaderLen` 必须为 20；包体默认上限 `DefaultMaxBodyLen`（256 KiB），超出断开。WebSocket 每个二进制消息必须恰好一帧，多余字节是 `ErrTrailingData`。
- TCP 默认 `xxtea`，WebSocket 默认 `plain`（加密交给 wss）。`AUTH` 在 xxtea 下的密钥交换与后续 `mask_key` 逻辑在 `protocol.Codec` / `handler.auth`，不要「简化」掉。
- 服务端下发的包头 `client_version` 来自配置，默认 `100000000`。旧客户端会丢弃版本不一致的包。
- 建连后必须在 `auth_timeout`（默认 30s）内完成 `AUTH`；重复 `AUTH` 或未认证发其他命令会断开。心跳：服务端每 5s 下发只有包头的 `NOOPING`（6）；WebSocket 另有 ping，默认 60s 空闲断开。
- 短连接是 `POST` 到与长连接共用的 URL，包体仍是旧的 xxtea 包装（`internal/gateway/httpapi`）。另有 `GET /healthz`。不要把短连接改成普通 JSON REST 却还共用命令号，除非明确做新的 HTTP 网关服务（那是后续工作，不是现在改 `httpapi`）。
- gRPC 客户端目前是 insecure。生产加固可以后续加 TLS，但不要只在某一个调用点改凭证而让 `grpcx.Dial` / `DialDiscovered` 分叉。
- 新的 gRPC 服务必须能被 `etcd:///服务名` 找到。空的 `etcd.endpoints` 表示关闭发现，只用于本机直连。不要把「进程起来了」当成已经注册：deploy 配置里要有 `etcd` 和 `register.name`。

### 序列化、存储、Redis/Kafka

- protobuf 用 `proto.Marshal` / `Unmarshal`（`google.golang.org/protobuf`），不要用 `gogo` 或 `encoding/json` 替代服务间消息。Kafka 的 value 就是这些 protobuf 字节：`ChatroomBcst`、`ChatRoomMemberBeKickedBcst`、`ChatRoomMiscEventMsg`。
- Redis 里房间/成员信息常以 JSON 缓存，结构体字段名不可随意改（见 `model.ChatroomInfo` 注释）。Lua 脚本里的键格式必须与 `keys.go` 完全一致，并且 Redis Cluster 下同一脚本触及的键必须在同一哈希槽（core 用 `{CORE_SERVICE}`；gateway 日活相关键用 `{MARS_AGENT:%s}`）。
- 禁言、踢出、断线离房依赖键过期通知。chatroom 启动时会尝试 `CONFIG SET notify-keyspace-events` 补上 `Ex`；托管 Redis 禁用 `CONFIG` 时只有告警，需要运维侧打开。`deploy/docker-compose.yml` 里 Redis 已带 `--notify-keyspace-events Ex`。不要改掉 `watcher` 对过期事件的依赖却不做替代方案。
- Redis 读超时在 `redisx` 里是 1s，Dial/Ping 3s。长时间的 `BLPOP` 或订阅不要复用这个默认客户端而不加考虑。
- MySQL DSN 必须 `charset=utf8mb4&parseTime=true`。core 与 chatroom 都在启动时 `AutoMigrate`。只允许添加表/列/索引；禁止重命名已有表名、列名、索引名（它们已经按线上库写死，例如 `UQE_chatroom_info_idx_app_room`）。
- `kafkax.NewConsumerGroup` 在没有提交位移时从 **newest** 开始消费，重启不会重放旧消息。不要在未讨论兼容性的情况下改成 `OffsetOldest`。
- webhook 出站签名：`Check-Sum = hex(sha1(AppSecret + Nonce + Cur-Time + 原始请求体))`，小写、无分隔符。改抄送格式必须同步文档和 `delivery/sign` 的测试。投递是至多一次：队列满或重试耗尽就丢弃并记日志。

### 多租户与身份

- 所有业务数据以 `appId` 隔离。Redis 键、MySQL 唯一索引、网关会话查找都必须带 `appID`，不要只按 `uid` 当全局唯一。
- 登录令牌由 core 签发，存在 Redis，值为 `"appID,uid"`。网关 `AUTH` 必须校验令牌里的 `appId` 与请求 `app_id` 一致。
- 进房令牌由 chatroom 签发，TTL 来自 `enter_token_ttl`（默认 300s）。进房还要求角色 ID 已存在（`CreateRole`）；这是运行时契约，不是可省略的演示细节。
- 同一用户新连接会踢掉旧连接（`LoginKickedBcst`）。用不同登录令牌再次 AUTH 视为新登录，会离开上一次登录仍在的房间。

## 8. 日常命令

```bash
make tools proto   # 安装 buf 与插件并生成 api/gen；改 proto 后必做
make test          # go test -race ./...
make lint          # go vet + golangci-lint
make build         # 编译 core chatroom gateway webhook 到 bin/
make up / make down
go run ./cmd/core -config configs/core.yaml   # 其他服务同理
go run ./examples/quickstart -uid 10001 -room 1001
```

国内构建可加 `GOPROXY=https://goproxy.cn,direct`。不要提交 `bin/`、`configs/*.local.yaml` 或密钥。

## 9. 明确不要做的事

- 不要手改 `api/gen/`。
- 不要实现或「顺便落地」私聊、群聊、独立 HTTP 网关，除非任务明确要求；协议和 `PcAppInfo` 表里的预留保持原样即可。
- 不要把历史消息查询或客户端日志上传改成静默成功；它们目前必须继续返回不支持 / `Unimplemented`。
- 不要修改已有 Redis 键字符串、Kafka topic 名、protobuf 字段号、错误码数值。
- 不要让多个网关实例共用同一个 Kafka `group_id`。
- 不要绕过 `pkg/discovery` 再写一套注册表，也不要把 Redis pub/sub 当成服务发现。实例列表在 etcd，应用配置变更才走 Redis 频道 `orbikit:app_config`。
