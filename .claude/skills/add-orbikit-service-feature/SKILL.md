---
name: add-orbikit-service-feature
description: 在 orbikit 已有服务（core、chatroom、gateway、webhook，以及已经按 extend-orbikit-service 加上的业务服务）中增加需求。当用户要加 gRPC、客户端命令、存储字段、Kafka 事件、审核或抄送行为时使用。不要用于从零新建一个独立服务进程（用 extend-orbikit-service）。任务没有点名时不要实现私聊、群聊或独立 HTTP 网关，也不要改掉 Redis 键、topic、字段号或错误码数值。
---

<!-- 同一份正文：.agents/skills/add-orbikit-service-feature/SKILL.md 与 .claude/skills/add-orbikit-service-feature/SKILL.md。改一处必须同时改另一处。 -->

# 在已有 orbikit 服务中增加需求

本 skill 描述如何在现有进程里加功能，并保持分布式行为与协议兼容。先读根目录 `AGENTS.md`、`docs/architecture.md`、`docs/protocol.md`，再改代码。新增的是整个进程时，改用 `extend-orbikit-service`。

需求里看不清落在哪个服务、要加哪些 RPC、失败时客户端看到什么，就先问这些具体问题。用户确认之前不改 proto，不改路由。仅仅打开本 skill 不算确认。

不要实现未点名的私聊、群聊或独立 HTTP 网关。协议里已有的预留（如 `SessionType`、`MESSAGE_HISTORY_QUERY`、core 的 `PcAppInfo` / `GdAppInfo` 表）保持原样，不要删除或改用途。调用其他进程用 `pkg/grpcx.DialDiscovered`：部署配置是 `etcd:///服务名`，本机配置是 `host:port`。不要新写一套服务发现。

## 1. 先定位服务和层

按需求落点选择**一个**主服务，必要时再改它的下游或网关适配，不要四个进程各写一份业务规则。

| 需求类型 | 主服务 | 从这里开始读 |
|---|---|---|
| 应用、登录令牌、封禁、抄送地址、QPS | `core` | `internal/core/server/*.go`，`internal/core/logic/`，`internal/core/store/` |
| 房间、成员、角色、麦位、机器人、房间消息、进房令牌、在线离线 | `chatroom` | `internal/chatroom/server/`，`internal/chatroom/logic/` |
| 客户端命令、长/短连接、审核、推送、会话踢线 | `gateway` | `internal/gateway/handler/`，`routes.go`，`push/`，`convert/` |
| 消息抄送、签名、限流、投递重试 | `webhook` | `internal/webhook/delivery/`，`server/server.go` |

一条完整的「客户端发消息」链路（加功能时按需截取，不要跳层）：

1. 业务方调 `Core.ApplyClientLoginToken`、`Chatroom.CreateChatroom` / `ApplyEnterChatroomToken`（见 `examples/quickstart`）。
2. 客户端长连接 `AUTH`（800）→ `handler.auth` → `Core.GetClientLoginTokenInfo` → Redis 记录连接 → `Chatroom.UserOnline`。
3. 客户端命令 → `handler.routes` → 可选 `moderation` → `Chatroom` / `Core` gRPC。
4. chatroom `server` → `logic` → MySQL/Redis，并用 `event.Producer` 写 Kafka。
5. 每台网关用**唯一**消费组读取，`push.Dispatcher` + `convert` 推给本机连接。
6. webhook 用共享消费组 `webhook` 读 `orbikit.room.broadcast`，签名 POST 到应用配置的 URL。

职责边界不可打破：gateway 不保存房间状态；core 不感知房间；chatroom 不直接碰客户端连接；webhook 不产生房间状态。

## 2. 按现有分层改，不要开新路径

### core

`cmd/core` 装配已经完成，一般只改内部包：

- proto：`api/proto/service/core_service.proto` 以及共用的 `service_comm.proto`
- RPC：`internal/core/server/`（`app.go`、`token.go`、`restrain.go`、`gd_app.go`、`pc_app.go`）
- 规则：`internal/core/logic/`（`AppService`、`TokenService`、`RestrainService`）
- 存储：`internal/core/store/`（`AppInfo`、`GdAppInfo`、`PcAppInfo`、`RestrainUser`；表名如 `app_infos`）
- Redis 键：只增不改，定义在 `internal/core/logic/keys.go`，Lua 必须落在 `{CORE_SERVICE}` 同一槽
- 应用配置变更后调用 `Notifier.Publish`（`pkg/appnotify`，频道 `orbikit:app_config`），让其他 core 与 webhook 失效/刷新缓存。不要再走点对点的 `MsgCopy` 地址清单（代码里虽仍注册 `AppInfoUpdated`，广播才是主路径）

RPC 风格跟 `ApplyClientLoginToken`：`startRPC` + `defer tr.finish`，先准备带 `internalErrorResponse()` 的 reply，成功再换成 `successResponse()` 或具体业务码；存储失败用 `internalError(err)` 变成 gRPC Internal。

`AppService` 有内存缓存（默认 60s）。查找失败或应用不存在也会被缓存。**不要把调用方可能已取消的 context 直接打到缓存加载里**，已有 `TestAppServiceIgnoresCallerCancellation`。改缓存时保留「错误不是 valid app」、`InvalidateApp`、以及 `AllAppQPS` 预热行为。

### chatroom

- proto：`api/proto/service/chatroom_service.proto`（历史查询另在 `chatroom_message_service.proto`，**尚未实现对应进程**，客户端 2010/2011 目前由网关直接返回不支持，不要假装已经能查历史）
- RPC：`internal/chatroom/server/` 按领域拆文件（`chatroom.go`、`member.go`、`message.go`、`mic.go`、`role.go`、`robot.go`、`misc.go`）
- 规则：`internal/chatroom/logic/`，依赖来自 `logic.Init(logic.Deps{DB, Redis, Core, Events, Settings})`。继续用这套全局 Init，不要在同包再引入第二套构造方式
- 模型：`internal/chatroom/model`，时间列必须是 `Timestamp`（RFC3339Nano + `CHAR(64)`），JSON 列用 `StringMap` / `StringList`。`AutoMigrate` 只加表/列/索引，不改已有表名、列名、索引名（如 `chatroom_info`、`UQE_chatroom_info_idx_app_room`）
- 键：`internal/chatroom/rediskey/keys.go`，注释写明「格式不得更改」。解析函数（`ParseMutedUser` 等）和测试一起改
- 锁：`internal/chatroom/lock`，先复用 `UserLock` / `ChatroomInfoLock` / `ChatroomProcessLock` / `ChatroomMicLock`
- 下行：`internal/chatroom/event.Producer` 的 `Broadcast` / `FullServer` / `Kicked` / `Misc`
- 过期事件：`watcher` + `scheduler` + `logic/after_deal.go`。禁言、踢出、断线离房依赖 Redis `notify-keyspace-events Ex`。多实例会重复收到过期事件，逻辑必须幂等

每个新 RPC 按 `SendChatroomMsg` 的骨架写：

```go
func (s *Server) Foo(goCtx context.Context, req *servicepb.FooRequest) (reply *servicepb.FooReply, err error) {
    ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId()) // WithoutCancel
    reply = &servicepb.FooReply{Ret: serviceErr()}
    defer func() { ctx.TraceReply(reply, err) }()
    defer recoverTo(ctx, "Foo", &reply.Ret)

    if ok, err = checkApp(ctx, "Foo", req.GetAppId(), &reply.Ret); !ok {
        return
    }
    // 需要打开房间时用 openRoom(...)
    code, err := logic.Foo(ctx, req)
    if failed(ctx, "Foo", code, err, &reply.Ret) {
        return
    }
    reply.Ret = ret(code)
    return
}
```

`failed` 在有 `error` 时保持默认 `CHATROOM_SERVICE_ERROR` 并把 error 交给 gRPC；只有业务码才写入 `Ret`。`recoverTo` 必须直接 `defer`。`paramErr` 的 `ErrorMsg` 是 `CHATROOM_SERVICE_ERROR` 的名字，这是旧客户端契约，不要「修正」。后台任务用 `logic` 里的 `bg()`（10s 超时），不要把 RPC ctx 传到返回之后。

### gateway

客户端能感知的功能几乎都要改网关，即使规则在 chatroom/core：

1. `api/proto/client/` 增加或扩展消息；`cmd_id.proto` 分配命令号。`AUTH`（800）只能走长连接，禁止进入路由表。
2. `internal/gateway/handler/routes.go` 用 `route(cmd, url, emptyResp, handler)` 注册。URL 必须以 `/base/service/` 开头，且不能与现有 27 条重复（`TestRoutes` 会数数量，加一条就改期望值）。只有确认允许空包体时才设 `AllowEmpty`（现仅 `LOGOUT`）。
3. 处理函数放在对应文件（`message.go`、`member.go`、`chatroom.go`、`mic.go`、`auth.go`、`base.go`）：`unmarshal` → 校验 → 可选审核 → `s.rpcCtx` 调下游 → `rpcRet` + `convert.ErrorCode`。把 `servicepb`/`clientpb` 的字段映射放到 `internal/gateway/convert`，不要在 handler 里复制一份。
4. 需要下行推送时改 `internal/gateway/push`：按 topic 解出固定的 protobuf 类型，经 `convert` 后 `sessions.Lookup` / `RangeApp` 只推本机连接。新增 topic 必须同步 `gateway.Config.Topics`、两份 YAML、chatroom 的 `event.Topics`、compose 的 `kafka-init`。
5. 审核只发生在网关发消息前（`moderation.Moderator`）。扩展审核实现接口并由 `moderation.New` 接入，配置在 `configs/gateway.yaml` 的 `moderation` 段。
6. HTTP 短连接共用 `handler.Service`（`httpapi`），不要为同一命令再写一套 JSON API。短连接仍是旧 xxtea 包装；`GET /healthz` 已有。

协议坑：20 字节大端包头、默认包体上限 256 KiB、TCP 默认 xxtea、WS 默认 plain、`client_version` 默认 `100000000`、业务错误不断开、未 AUTH / 重复 AUTH / 超限才断开。改协议必须同步 `docs/protocol.md` 和 `internal/gateway/protocol` 的测试。

### webhook

- 消费的是 `servicepb.ChatroomBcst`，topic 默认 `orbikit.room.broadcast`，组名默认 `webhook`（多实例分摊，至多一次）。
- 出站：`delivery.Copier` → 每应用队列（`workers_per_app` / `queue_size`）→ `Sender`。签名见 `delivery/sign.go`：`Check-Sum = 小写 hex(SHA1(AppSecret + Nonce + Cur-Time + 原始 body))`。重试仅网络错误、HTTP 429 和 5xx。队列满或重试耗尽丢弃并打日志，不要改成会卡住 Kafka 分区的「至少一次」（`kafkax.Consume` 即使 handler 出错也会 `MarkMessage`）。
- 应用配置来自 `Core.GetAppSecret` 缓存，并由 `appnotify` + `MsgCopy.AppInfoUpdated` 更新。改缓存时保持 `expire` / `not_found_expire` 语义。

## 3. 改协议与生成代码

- 只能**新增**字段、RPC、枚举值；已有字段号、枚举数值、错误码数值、命令号一律不动。废弃用 `reserved` 或文档标明，不要删除。
- 历史拼写错误的符号（`CHATROOM_MEMBER_KIKY`、`CHATROOM_APPLAY_TOKEN_ROLEIDS_NOT_EXISTS` 等）不要重命名。
- 客户端与服务端错误码都改时，必须更新 `internal/gateway/convert.ErrorCode`，并补测试，避免网关把下游码映射成错误的 `clientpb.ErrorCode`。
- 改完 proto 后执行 `make tools proto`，提交 `api/proto` 与 `api/gen` 的一起变化。不要手改 `api/gen`。
- 更新 `docs/protocol.md`（命令、推送、签名）或 `docs/architecture.md`（数据放哪、消息路径）中被影响到的段落。

## 4. 兼容性与分布式行为（必查）

加功能时逐项确认，而不是只跑通单测：

- **多租户**：所有新键、表、会话查找都带 `appId`，禁止只按 `uid` 当全局唯一。
- **网关无状态**：房间与成员状态仍只在 chatroom 的 MySQL/Redis。新功能不要在 gateway 内存里保存跨实例才正确的业务状态；进程内 `session.Manager` 只表示本机连接。
- **Kafka 消费组**：不要给网关配置写死的、会在多副本间共享的 `group_id`。webhook 必须继续共享同一组才能避免重复抄送。
- **至多一次**：Kafka 消费失败仍提交位移。新的推送/抄送必须可接受丢失，并打日志。不要擅自把 `Offsets.Initial` 从 newest 改成 oldest。
- **幂等**：chatroom 多实例会重复处理同一 Redis 过期事件。踢人、关房、禁言解除、离房必须可重入。
- **取消与关闭**：chatroom 多步写使用 `context.WithoutCancel`；请求结束后的清理用独立超时 ctx。不要把取消的请求 ctx 传入共享缓存加载。
- **挤线与重连**：改 AUTH / 离线路径时保留 `LockUser`、`KickOtherConnections`、`UserOnline`/`UserOffline` 的 `ConnUuid` 语义，以及「不同 login token 视为新登录并离开旧房间」。
- **键与缓存字段**：Redis 键字符串、以 JSON 缓存在 Redis 的结构体字段名（如 `model.ChatroomInfo`）不可随意改。
- **未实现功能**：历史消息、客户端日志上传必须继续返回不支持 / `codes.Unimplemented`，不要改成空成功。
- **配置双份**：改默认值或新增配置项时同步 `internal/<svc>/config.go` 的 `DefaultConfig`、`configs/*.yaml`、`deploy/configs/*.yaml`。compose 文件通常只是主机名不同（`localhost` vs `mysql`/`redis`/`kafka`/`core`）。
- **客户端版本**：服务端主动下发包带配置里的 `client_version`。没有明确的协议升级方案时不要改默认值 `100000000`。

## 5. 测试怎么补

用标准库 `testing`，表驱动，`t.Helper()`，不引 testify。按改动选择已有风格：

- 网关命令：学 `internal/gateway/handler/handler_test.go`（假 `ChatroomClient`、审核桩、`offlineStore`）。**改路由必须更新 `TestRoutes` 的 27**。
- core 逻辑：学 `internal/core/logic/logic_test.go`（键格式、令牌形态、缓存、取消、错误不当成功）。
- chatroom RPC 骨架：学 `internal/chatroom/server/server_test.go`（`recoverTo`、`failed`、参数校验发生在依赖初始化之前、Unimplemented）。
- 转换、签名、限流、包编解码：保持纯单测（`convert`、`delivery/sign_test.go`、`protocol/packet_test.go`、`codec_test.go`）。
- Redis 相关可用 `miniredis`（已是间接依赖），不要在单元测试里要求真实 MySQL/Kafka。

至少覆盖：成功路径、主要业务错误码、参数错误、以及你可能破坏的兼容点（映射、路由数、键格式、审核失败码）。

验证：

```bash
make proto
make test
make lint
```

涉及网关命令或房间消息时，再用：

```bash
# 依赖已在本地或 make up 起来之后
go run ./examples/quickstart -uid 10001 -room 1001
go run ./examples/gateway-client -transport ws -app <app_id> -uid 10001     -token <login_token> -room 1001 -room-token <room_token> -msg hello
```

需要观察推送或抄送时，分别确认：另一客户端能收到 1070/1060 等；webhook 对配置了 `text_cp_url` 等地址的应用发出带 `App-Id` / `Nonce` / `Cur-Time` / `Check-Sum` 的 POST。

## 6. 实施顺序（建议）

1. 写或补失败/成功测试（能先写的先写），确认当前红/绿灯含义。
2. 改 proto（如需要）并 `make proto`。
3. 改 logic/store/delivery，再改 server/handler/routes/convert/push。
4. 同步 YAML、文档、路由测试数量。
5. `make test` 与 `make lint`。涉及生成代码时确认 `api/gen` 无手改痕迹。

## 7. 检查清单

- [ ] 主改动落在正确的服务和分层，没有跨 `internal/` 包引用另一个进程
- [ ] 没有修改已有 protobuf 字段号、错误码数值、命令号、Redis 键、Kafka topic 名
- [ ] 网关路由、convert 映射、消费组语义、多租户隔离已检查
- [ ] chatroom 新 RPC 使用 `newCtx` / `recoverTo` / `checkApp` / `failed`
- [ ] 两份配置和（如有）文档已更新
- [ ] `make proto && make test && make lint` 通过
- [ ] 没有顺便实现私聊、群聊、独立 HTTP 网关或历史消息存储
