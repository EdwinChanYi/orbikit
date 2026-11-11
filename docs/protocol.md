# 协议

客户端和网关之间是一条长连接。TCP 和 WebSocket 用同一套帧，WebSocket 的每个二进制消息恰好是一帧。

## 帧

20 字节包头，五个 uint32，大端：

| 偏移 | 字段 | 说明 |
|---|---|---|
| 0 | headerLen | 固定 20 |
| 4 | clientVersion | 客户端版本。服务端下发的包使用配置里的 `client_version`，默认 `100000000` |
| 8 | cmdId | 命令号，见 `api/proto/client/cmd_id.proto` |
| 12 | seqId | 请求序号。服务端推送为 0 |
| 16 | bodyLen | 包体长度，默认上限 256 KiB，超出即断开 |

包体是 protobuf，定义在 `api/proto/client`。响应带回请求的 cmdId 和 seqId。

## 两种包体编码

由配置选择，TCP 默认 `xxtea`，WebSocket 默认 `plain`。

- `plain`：包体就是 protobuf。`AUTH` 也是。
- `xxtea`：`AUTH` 请求包体是 8 字节大端 `maskNum` 拼上 `xxtea(AuthReq, key)`，key 是 `^maskNum` 的十进制字符串。`AUTH` 响应用 `maskNum` 的十进制字符串做密钥。之后的包体用 `AuthResp.mask_key` 做密钥。

WebSocket 建议直接用 `plain`，传输加密交给 wss。

## 登录和进房

1. 连接后先发 `AUTH`（800），包体 `AuthReq`：`app_id`、`auth_token`（core 签发的登录令牌）、`device_id`、`type`。
2. 成功返回 `AuthResp`，随后可能推送 `CHATROOM_IN_STATUS_BCST`（1004），带该用户还在的房间。
3. 发 `CHATROOM_ENTER`（1001），包体 `EnterChatroomReq`：`room_id` 和进房令牌。
4. 发消息用 `CHATROOM_SEND_MESSAGE`（1050），包体 `SendMessageReq`。文本消息的 `message_type` 为 `MESSAGE_TYPE_TEXT`，`session.session_type` 为 `SESSION_TYPE_CHATROOM`，`session.session_id` 是房间号。

未完成 `AUTH` 就发其他命令、重复 `AUTH`、或 `AUTH` 超时（默认 30 秒），连接会被断开。业务错误只回错误码，不断开。

服务端每 5 秒下发一个只有包头的 `NOOPING`（6）。WebSocket 另外用 ping 帧检测空闲，默认 60 秒无响应断开。

## 服务端推送

| cmdId | 包体 | 何时 |
|---|---|---|
| 1070 | `MessageBcst` | 房间消息、全服消息 |
| 1060 | `ChatRoomMemberBeKickedBcst` | 被踢出房间 |
| 1004 | `ChatroomInStatusBcst` | 登录后，告知还在哪些房间 |
| 802 | `LoginKickedBcst` | 被其他端挤下线，随后断开 |
| 997 | 要求上传日志 | 目前没有配套的上传实现 |

## 命令一览

短连接是 `POST`，URL 与长连接命令共用处理函数。短连接仍是旧的 xxtea 包装，见网关 `httpapi` 包。浏览器 demo 走 WebSocket。

| cmdId | 短连接 URL |
|---|---|
| 999 | `/base/service/echo` |
| 801 | `/base/service/logout` |
| 1001 / 1002 / 1005 | `/base/service/chatroom/enter`、`/out`、`/batch.out` |
| 1010 / 1011 / 1012 | `/base/service/chatroom/info.get`、`/info.edit`、`/base/service/opext/get` |
| 1050 / 1051 | `/base/service/chatroom/msg/send`、`/base/service/message/confirm` |
| 1041 / 1042 / 1043 | `/base/service/chatroom/edit.member/kick`、`/block`、`/mute` |
| 1020–1026 | `/base/service/chatroom/position.up` 到 `.token.update` |

`2010` 和 `2011` 历史消息查询保留了命令号，当前返回不支持。

## 消息抄送签名

webhook 按消息类型选择回调地址：文本用 `text_cp_url`，自定义用 `custom_cp_url`，房间事件用 `msg_cp_url`。

`POST`，`Content-Type: application/json`，请求头：

| Header | 说明 |
|---|---|
| `App-Id` | 应用 ID |
| `Nonce` | 64 位随机串 |
| `Cur-Time` | Unix 秒 |
| `Check-Sum` | `小写十六进制(SHA1(AppSecret + Nonce + Cur-Time + 原始请求体))`，直接拼接，无分隔符 |

请求体是抄送消息的 JSON：字段 snake_case，零值省略，枚举和 int64 是数字，自定义内容的二进制字段为 base64。
