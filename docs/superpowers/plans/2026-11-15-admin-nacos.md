# Nacos Config Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Nacos 默认关闭；打开后四个平台服务用 Group `orbikit` 的配置覆盖本地 YAML，管理页通过 Nacos Open API 发布、回滚、按 IP 灰度和查看监听者，并且只热更新日志级别、缓存时间和审核开关。

**Architecture:** `pkg/nacos` 用标准库 HTTP 访问 Nacos 2.5.5 的 Open API，测试用 `httptest`，不启动 Nacos，也不引入 Nacos SDK。本地 YAML 先加载，远程文档再覆盖到同一个结构体；远程失败时保留本地并写日志。监听只把允许的字段交给各服务，其余变更记为需要重启。`cmd/admin` 保存 Nacos 账号并代理 Open API，浏览器只访问 `:8091`。Nacos 关闭时配置页保持现在的空状态。

**Tech Stack:** Go 1.27.4、标准库 `net/http`、`gopkg.in/yaml.v3`、Vue 3、Arco Design Vue、vitest、Nacos 2.5.5 镜像 `nacos/nacos-server:v2.5.5`。

## Global Constraints

- Nacos 默认关闭。`make up` 不启动它。compose 里 Nacos 使用 profile `nacos`，不映射宿主机端口。
- 镜像固定 `nacos/nacos-server:v2.5.5`。不嵌入 Nacos 控制台。
- Group 固定 `orbikit`。DataId 固定 `<服务>.yaml`。服务只有 `core`、`chatroom`、`gateway`、`webhook`。namespace 用 public，请求里的 tenant 为空。
- 本地 YAML 仍负责引导。Nacos 的开关、地址、账号写在各服务自己的 YAML 里。密码用环境变量 `NACOS_PASSWORD`，compose 缺省值是 `nacos`。密码不进 Git 的非默认值。
- 连不上 Nacos，或远程正文不是合法 YAML 时，进程用已经加载的本地配置启动，日志包含 `using local config`。不因此退出。
- 打开后配置正文以 Nacos 为准。这一轮不往 `orbikit_admin` 写配置历史。
- 浏览器不看到 Nacos 地址、账号、密码或 `8848`。配置接口都走 admin，并要求 `orbikit_session`。
- 热更新只允许这些路径：`log.level`；core 的 `cache_expire`；webhook 的 `cache.expire`、`cache.not_found_expire`；gateway 的整棵 `moderation`。chatroom 只有 `log.level`。
- `grpc_addr`、`tcp.addr`、`ws.addr`、`http.addr`、`mysql`、`kafka`、`redis`、`etcd` 变更不热更新。监听循环打日志 `restart required`，并带上变化的路径。
- 日志热更新只调用已有的 `log.SetLevel`。不重建 logger，不改输出文件和控制台开关。
- Nacos 关闭时 `pkg/config.Load` 的行为不变。配置页仍显示「配置还没有功能」。顶栏在关闭时仍是「本地配置」。
- Elasticsearch、本地 YAML 编辑器、容器日志跟随、镜像构建都不在这份计划里。
- 测试用标准库 `testing` 和 vitest，不引入 testify，不连接真实 Nacos 或 Docker。
- 工作区里已有未提交的 etcd、Go 1.27 和 skills 改动。本计划的提交只包含本任务文件。不要 `git add` `go.mod`、`go.sum`、`pkg/discovery`、`pkg/grpcx`、`README.md`、`docs/architecture.md`、`.agents`、`.claude`、`AGENTS.md`。`cmd/*/main.go`、`configs/*.yaml`、`deploy/configs/*.yaml`、`deploy/docker-compose.yml` 若必须修改，先把无关 diff 挪开，只提交本任务的块，再把无关 diff 放回去。

---

### Task 1: Nacos HTTP 客户端

**Files:**
- Create: `pkg/nacos/client.go`
- Create: `pkg/nacos/client_test.go`

**Interfaces:**
- Produces:
  - `const Group = "orbikit"`
  - `func DataID(service string) (string, error)`：允许的服务返回 `<service>.yaml`，其他返回错误，正文包含 `服务名不在允许范围内`。
  - `type Client struct`，字段由 `New(baseURL, username, password string) *Client` 填充。`baseURL` 不含尾部斜杠。
  - `func (c *Client) Get(ctx context.Context, dataID string) (string, error)`
  - `func (c *Client) Publish(ctx context.Context, dataID, content string) error`
  - `type Revision struct { ID, Content, Modified string }`
  - `func (c *Client) History(ctx context.Context, dataID string) ([]Revision, error)`
  - `func (c *Client) Beta(ctx context.Context, dataID string) (content string, ips []string, err error)`
  - `func (c *Client) PublishBeta(ctx context.Context, dataID, content string, ips []string) error`
  - `func (c *Client) DeleteBeta(ctx context.Context, dataID string) error`
  - `type Listener struct { IP string }`
  - `func (c *Client) Listeners(ctx context.Context, dataID string) ([]Listener, error)`

- [ ] **Step 1: Write the failing test**

`pkg/nacos/client_test.go` 用 `httptest.Server`。登录路径 `POST /nacos/v1/auth/login`，表单字段 `username=nacos`、`password=nacos`，响应 `{"accessToken":"tok","tokenTtl":18000}`。之后的请求必须带查询参数 `accessToken=tok`。

```go
func TestDataIDRejectsUnknownService(t *testing.T) {
	if _, err := DataID("admin"); err == nil || !strings.Contains(err.Error(), "服务名不在允许范围内") {
		t.Fatalf("err = %v", err)
	}
	id, err := DataID("core")
	if err != nil || id != "core.yaml" {
		t.Fatalf("id = %q, err = %v", id, err)
	}
}
```

`Get`：`GET /nacos/v1/cs/configs?dataId=core.yaml&group=orbikit` 返回正文 `log:\n  level: debug\n`，状态 200。`Get` 的返回值等于该正文。状态 404 时返回错误，正文包含 `not found`。状态 500 时返回错误，正文包含 `nacos status 500`。

`Publish`：`POST /nacos/v1/cs/configs`，表单含 `dataId=core.yaml`、`group=orbikit`、`type=yaml`、`content=log:`。响应正文 `true`。

`History`：`GET /nacos/v1/cs/history?search=accurate&dataId=core.yaml&group=orbikit&pageNo=1&pageSize=20`，响应：

```json
{"totalCount":1,"pageItems":[{"id":"9","content":"log:\n  level: info\n","lastModifiedTime":"2026-11-15 00:00:00"}]}
```

返回的 `Revision.ID` 是 `9`，`Content` 是上述 YAML，`Modified` 是该时间字符串。

`PublishBeta`：同一个 publish 路径，表单另含 `beta=true` 和 `betaIps=10.0.0.8,10.0.0.9`。`Beta` 请求 `GET /nacos/v1/cs/configs?beta=true&dataId=core.yaml&group=orbikit`，响应 `{"content":"gray","betaIps":"10.0.0.8,10.0.0.9"}`，解析出内容和这两个 IP。`DeleteBeta` 发送 `DELETE /nacos/v1/cs/configs?beta=true&dataId=core.yaml&group=orbikit`。

`Listeners`：`GET /nacos/v1/cs/configs/listener?dataId=core.yaml&group=orbikit`，响应 `[{"ip":"10.0.0.3"}]`，返回一个 `Listener{IP:"10.0.0.3"}`。

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/nacos -count=1`

Expected: FAIL，`DataID` 未定义。

- [ ] **Step 3: Write minimal implementation**

`client.go` 用 `http.Client{Timeout: 10 * time.Second}`。`login` 在 token 为空时调用。所有业务请求在 query 加 `accessToken`。`group` 固定 `Group`。`History` 只解码 `pageItems` 的 `id`、`content`、`lastModifiedTime`。`Beta` 把 `betaIps` 按逗号拆开，去掉空白。`PublishBeta` 拒绝空 IP 列表，也拒绝 `net.ParseIP` 失败的条目，错误包含 `无效的灰度 IP`。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/nacos -count=1`

Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add pkg/nacos/client.go pkg/nacos/client_test.go
git commit -m "feat: call the Nacos config API"
```

作者环境变量：`GIT_AUTHOR_NAME=edwin`、`GIT_AUTHOR_EMAIL=568264623@qq.com`，提交者使用同一对值。不要 `--no-verify`，不要 amend。

---

### Task 2: 启动覆盖和长轮询

**Files:**
- Create: `pkg/nacos/overlay.go`
- Create: `pkg/nacos/overlay_test.go`

**Interfaces:**
- Consumes: `Client.Get`。
- Produces:
  - `func Overlay(ctx context.Context, c *Client, dataID string, dst any) (usedRemote bool, err error)`
  - `func (c *Client) Watch(ctx context.Context, dataID, contentMD5 string, onChange func(content string))`

`Overlay` 的 `dst` 已经装有本地配置。`c == nil` 时返回 `false, nil`，不改 `dst`。GET 失败或正文无法按 YAML 解进 `dst` 时返回 `false` 和错误，错误包含 `using local config`，并且 `dst` 保持调用前的值。GET 成功时把远程 YAML 解到 `dst` 上，返回 `true, nil`。远程文档没写的字段保留本地值。

`Watch` 循环 `POST /nacos/v1/cs/configs/listener`。请求头 `Long-Pulling-Timeout: 30000`。正文表单 `Listening-Configs` 的值是 `dataID + "\x02" + Group + "\x02" + contentMD5 + "\x01"`。响应体为空时继续下一轮。响应体非空时再 `Get` 一次，把新正文交给 `onChange`，并用新正文的 MD5 继续监听。`ctx` 取消后返回。MD5 使用 `crypto/md5` 的十六进制小写。

- [ ] **Step 1: Write the failing test**

用一个只含 `Log struct{ Level string \`yaml:"level"\` }` 和 `Addr string \`yaml:"grpc_addr"\`` 的目标。先填入 `info` 和 `:9001`。服务端返回 `log:\n  level: debug\n`。调用后 `Level == "debug"`，`Addr` 仍是 `:9001`，`usedRemote` 为 true。

第二个测试让 GET 返回 500。`Overlay` 的错误包含 `using local config`，`Level` 仍是 `info`。`c == nil` 时不发请求。

`Watch` 测试：第一轮 listener 响应 `core.yaml`，随后 GET 返回 `log:\n  level: warn\n`。`onChange` 收到该正文后取消 `ctx`。断言只收到一次，且第二次 listener 请求的 `Listening-Configs` 含新正文的 MD5。

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/nacos -count=1 -run 'Overlay|Watch'`

Expected: FAIL，`Overlay` 未定义。

- [ ] **Step 3: Write minimal implementation**

`Overlay` 在修改 `dst` 之前把远程正文解到一个 `yaml.Node`，失败则直接返回。成功后再 `yaml.Unmarshal` 到 `dst`。`Watch` 在每次循环开始时检查 `ctx.Done()`。HTTP 超时或暂时失败时睡 1 秒再试，`ctx` 已取消则不再睡。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/nacos -count=1`

Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add pkg/nacos/overlay.go pkg/nacos/overlay_test.go
git commit -m "feat: overlay local config from Nacos and watch for changes"
```

---

### Task 3: 热更新路径和需要重启的路径

**Files:**
- Create: `pkg/nacos/hot.go`
- Create: `pkg/nacos/hot_test.go`

**Interfaces:**
- Produces:
  - `func HotPaths(service string) []string`
  - `type FieldChange struct { Path, Value string }`
  - `func Classify(service string, before, after []byte) (hot []FieldChange, restart []string, err error)`

`HotPaths`：

| 服务 | 路径 |
|---|---|
| core | `log.level`、`cache_expire` |
| chatroom | `log.level` |
| webhook | `log.level`、`cache.expire`、`cache.not_found_expire` |
| gateway | `log.level`、`moderation` |
| 其他 | 空切片 |

`Classify` 把两份 YAML 解成 `yaml.Node` 映射。比较叶子路径。`moderation` 按整棵子树比较：子树有任何不同就产生一条 `FieldChange{Path:"moderation", Value:<after 的 moderation 子树 YAML>}`。其余热路径产生对应的 `FieldChange`，`Value` 是 after 里该叶子的字符串。不在热路径里的变化追加到 `restart`，路径使用点号，例如 `mysql.dsn`、`grpc_addr`、`kafka.brokers`。两边相同的路径不出现。`before` 或 `after` 不是 YAML 映射时返回错误。

- [ ] **Step 1: Write the failing test**

```go
func TestClassifySplitsHotAndRestart(t *testing.T) {
	before := []byte("log:\n  level: info\ncache_expire: 60s\ngrpc_addr: \":9001\"\nmysql:\n  dsn: old\n")
	after := []byte("log:\n  level: debug\ncache_expire: 15s\ngrpc_addr: \":9001\"\nmysql:\n  dsn: new\n")
	hot, restart, err := Classify("core", before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(hot) != 2 || hot[0].Path != "log.level" || hot[0].Value != "debug" || hot[1].Path != "cache_expire" || hot[1].Value != "15s" {
		t.Fatalf("hot = %+v", hot)
	}
	if len(restart) != 1 || restart[0] != "mysql.dsn" {
		t.Fatalf("restart = %+v", restart)
	}
}
```

再测 gateway：只有 `moderation.type` 从 `none` 变成 `keyword` 时，`hot` 只有一条 `moderation`，`restart` 为空。chatroom 的 `enter_token_ttl` 变化进入 `restart`，`log.level` 进入 `hot`。

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/nacos -count=1 -run Classify`

Expected: FAIL，`Classify` 未定义。

- [ ] **Step 3: Write minimal implementation**

用递归比较 mapping node。标量用 `node.Value`。序列和 `moderation` 子树用重新编码后的 YAML 文本比较。路径排序后再返回，保证测试稳定。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/nacos -count=1`

Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add pkg/nacos/hot.go pkg/nacos/hot_test.go
git commit -m "feat: classify hot config changes from restarts"
```

---

### Task 4: core 和 webhook 的缓存时间可以换

**Files:**
- Modify: `internal/core/logic/app.go`
- Modify: `internal/core/logic/logic_test.go`
- Modify: `internal/webhook/delivery/appconfig.go`
- Modify: `internal/webhook/delivery/appconfig_test.go`

**Interfaces:**
- Produces:
  - `func (s *AppService) SetCacheExpire(ttl time.Duration)`
  - `func (c *AppCache) SetTTL(ttl, negativeTTL time.Duration)`

`SetCacheExpire` 把 `AppService.ttl` 换成新值。之后的缓存写入使用新 ttl。已存在的条目保持原来的到期时间。`ttl <= 0` 时不修改。

`SetTTL` 同样只影响之后的写入。`ttl <= 0` 时保留原来的 `ttl`；`negativeTTL <= 0` 时保留原来的 `negativeTTL`。两个都 `<= 0` 时函数直接返回。

- [ ] **Step 1: Write the failing test**

在 `logic_test.go` 增加测试：用 1 小时创建 `AppService`，调用 `SetCacheExpire(time.Millisecond)`，写入一条，把缓存时钟向前拨 2 毫秒，下一次读取必须重新加载。不要改已有的缓存测试。

在 `appconfig_test.go` 增加测试：`SetTTL(time.Millisecond, time.Hour)` 后新放入的命中条目在 2 毫秒后重新加载；负缓存仍按原来的 `negativeTTL`，因为这次传入的负值是正的 1 小时，所以 2 毫秒后负缓存还在。再调用 `SetTTL(0, time.Millisecond)`，新的未命中在 2 毫秒后重新查询。

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/logic ./internal/webhook/delivery -count=1 -run 'SetCacheExpire|SetTTL'`

Expected: FAIL，方法未定义。

- [ ] **Step 3: Write minimal implementation**

`AppService.ttl` 的读取改成带锁的当前值，或者把 `ttl` 换成 `atomic.Int64` 纳秒。`getOrLoad` 里现在把 `s.ttl` 传进闭包，改为每次加载时读取当前 ttl。`AppCache.SetTTL` 在 `c.mu` 下更新两个字段。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/core/logic ./internal/webhook/delivery -count=1`

Expected: PASS。

- [ ] **Step 5: Commit**

只提交这两个包里本任务改动的文件。

```bash
git add internal/core/logic/app.go internal/core/logic/logic_test.go internal/webhook/delivery/appconfig.go internal/webhook/delivery/appconfig_test.go
git commit -m "feat: let app caches take a new TTL"
```

---

### Task 5: gateway 的审核器可以换

**Files:**
- Modify: `internal/gateway/handler/handler.go`
- Modify: `internal/gateway/handler/handler_test.go`

**Interfaces:**
- Consumes: `moderation.New`、`moderation.Moderator`。
- Produces: `func (s *Service) SetModerator(m moderation.Moderator)`

现有字段 `Moderator` 继续保存初始值。新增一个原子指针。`SetModerator(nil)` 不改变当前审核器。处理消息的代码改为读取 `currentModerator()`：原子指针非空时用它，否则用字段 `Moderator`。

- [ ] **Step 1: Write the failing test**

构造一个总是允许的 `Moderator` 和一个总是拒绝的 `Moderator`。`Service` 先使用允许的实现，`SetModerator` 换成拒绝的实现后，下一次审核调用必须走拒绝实现。测试不要依赖真实连接。

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gateway/handler -count=1 -run SetModerator`

Expected: FAIL，`SetModerator` 未定义。

- [ ] **Step 3: Write minimal implementation**

在 `handler.go` 增加 `moderator atomic.Pointer[moderation.Moderator]` 不合适，因为接口不能取类型指针。使用 `atomic.Value`，存 `moderation.Moderator`。`currentModerator` 在 `Load` 为 nil 时返回字段。把现有读取 `s.Moderator` 的业务路径改为 `s.currentModerator()`。赋值 `s.Moderator = ...` 的构造路径保留。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/gateway/handler -count=1`

Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/handler/handler.go internal/gateway/handler/handler_test.go
git commit -m "feat: swap the gateway moderator at runtime"
```

---

### Task 6: 四个服务接入覆盖和监听

**Files:**
- Create: `pkg/nacos/bootstrap.go`
- Create: `pkg/nacos/bootstrap_test.go`
- Modify: `internal/core/config.go`
- Modify: `internal/chatroom/config.go`
- Modify: `internal/gateway/config.go`
- Modify: `internal/webhook/config.go`
- Modify: `configs/core.yaml`
- Modify: `configs/chatroom.yaml`
- Modify: `configs/gateway.yaml`
- Modify: `configs/webhook.yaml`
- Modify: `deploy/configs/core.yaml`
- Modify: `deploy/configs/chatroom.yaml`
- Modify: `deploy/configs/gateway.yaml`
- Modify: `deploy/configs/webhook.yaml`
- Modify: `cmd/core/main.go`
- Modify: `cmd/chatroom/main.go`
- Modify: `cmd/gateway/main.go`
- Modify: `cmd/webhook/main.go`

**Interfaces:**
- Consumes: `Overlay`、`Watch`、`Classify`、`log.SetLevel`、`AppService.SetCacheExpire`、`AppCache.SetTTL`、`Service.SetModerator`、`moderation.New`。
- Produces:
  - 四个服务的 `Config` 增加：

```go
type NacosConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Addr     string `yaml:"addr"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// 字段名
Nacos NacosConfig `yaml:"nacos"`
```

  - `func Start(ctx context.Context, service string, cfg NacosConfig, local []byte, apply func(content string))`

`Start` 在 `Enabled == false` 或 `Addr == ""` 时立即返回。否则 `New(cfg.Addr, cfg.Username, cfg.Password)`，调用 `Overlay`。`usedRemote == false` 时用 `log.L().Warn` 记录错误。无论远程是否成功，只要 `Enabled` 为 true，就在 goroutine 里 `Watch`。`onChange` 调用 `apply`。

每个 `cmd/*/main.go` 在 `config.Load` 之后、使用配置建立监听之前调用 `nacos.Overlay` 到已经加载的配置对象。保存本地文件原文。服务开始监听后再 `go nacos.Start(...)`。`apply` 做这些事：

1. `Classify(service, localOrLast, []byte(content))`。
2. 对 `log.level` 调用 `log.SetLevel`。失败时记日志，不退出。
3. core 解析 `cache_expire` 为 `time.Duration`，调用 `SetCacheExpire`。
4. webhook 解析两个缓存时长，调用 `SetTTL`。
5. gateway 把 `moderation` 的 YAML 解进 `moderation.Config`，`moderation.New` 成功后 `SetModerator`。失败只记日志。
6. `restart` 非空时 `log.L().Info("restart required", zap.Strings("paths", restart))`。
7. 成功应用热字段后，把 last 内容更新为这次的 content，供下一次 `Classify`。

本地 `configs/*.yaml` 和 `deploy/configs/*.yaml` 都增加：

```yaml
nacos:
  enabled: false
  addr: http://nacos:8848
  username: nacos
  password: ${NACOS_PASSWORD}
```

本地 `configs/*.yaml` 的 `addr` 仍写 `http://nacos:8848`，因为默认 `enabled: false`，本机 `go run` 不会连接。不要把 `enabled` 改成 true。

- [ ] **Step 1: Write the failing test**

`bootstrap_test.go` 验证 `Start` 在 `Enabled: false` 时不发 HTTP。另一个测试用 `httptest` 返回新的 `log.level`，`apply` 被调用一次。不要在这个测试里启动 core。

四个 `config_test.go` 已有加载测试。各加一条断言：示例 YAML 不含 nacos 时 `Nacos.Enabled` 是 false。不要要求现有示例把 nacos 写进去。

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/nacos ./internal/core ./internal/chatroom ./internal/gateway ./internal/webhook -count=1 -run 'Nacos|Start'`

Expected: FAIL，`NacosConfig` 未定义。

- [ ] **Step 3: Write minimal implementation**

按接口写入。修改 `cmd/*/main.go` 时不要改动 etcd 注册、端口或已有客户端初始化顺序，只在配置加载后插入 Overlay，在 `Serve` 前启动 `Start` 的 goroutine。`Start` 自己再启动 `Watch` 的 goroutine，调用方的 `go Start` 会多一层；`Start` 改为同步跑到 `Watch` 返回，由调用方 `go nacos.Start(...)`。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/nacos ./internal/core ./internal/chatroom ./internal/gateway ./internal/webhook -count=1`

Expected: PASS。`go test ./pkg/config -count=1` 也 PASS。

- [ ] **Step 5: Commit**

提交前检查 `git diff -- cmd/core/main.go cmd/chatroom/main.go cmd/gateway/main.go cmd/webhook/main.go configs deploy/configs`。只暂存 nacos 块。无关的 etcd 块留在工作区。

```bash
git commit -m "feat: overlay platform config from Nacos when enabled"
```

---

### Task 7: admin 代理 Nacos，关闭时配置接口不可用

**Files:**
- Modify: `internal/admin/config.go`
- Modify: `internal/admin/config_test.go`
- Create: `internal/admin/nacosapi/http.go`
- Create: `internal/admin/nacosapi/http_test.go`
- Modify: `cmd/admin/main.go`
- Modify: `configs/admin.yaml`
- Modify: `deploy/configs/admin.yaml`

**Interfaces:**
- Consumes: `pkg/nacos.Client`、`auth.RequireSession`。
- Produces: `func NewHandler(c *nacos.Client) http.Handler`。`c == nil` 表示 Nacos 关闭。

`admin.Config` 增加与服务相同的 `Nacos nacos.NacosConfig` 会引入密码字段重复。直接嵌入同一结构，把 `NacosConfig` 留在 `pkg/nacos`，admin 使用 `nacos.NacosConfig`。

路由，全部先经过 `RequireSession`：

| 方法 | 路径 | 关闭时 | 打开时 |
|---|---|---|---|
| GET | `/api/config/source` | `200 {"source":"local","enabled":false}` | `200 {"source":"nacos","enabled":true}` |
| GET | `/api/config/{service}` | `404 {"error":"Nacos 未打开"}` | `200 {"content":"..."}` |
| PUT | `/api/config/{service}` | 同上 | 正文 `{"content":"..."}`，调用 `Publish`，成功 `204` |
| GET | `/api/config/{service}/history` | 同上 | `200 {"revisions":[{"id","content","modified"}]}` |
| POST | `/api/config/{service}/rollback` | 同上 | 正文 `{"id":"9"}`。在 `History` 里找该 id，再 `Publish` 那份 content。找不到返回 `404 {"error":"发布记录不存在"}` |
| GET | `/api/config/{service}/beta` | 同上 | `200 {"content":"...","ips":["10.0.0.8"]}` |
| PUT | `/api/config/{service}/beta` | 同上 | 正文 `{"content":"...","ips":["10.0.0.8"]}`，成功 `204` |
| DELETE | `/api/config/{service}/beta` | 同上 | 成功 `204` |
| GET | `/api/config/{service}/listeners` | 同上 | `200 {"listeners":[{"ip":"10.0.0.3"}]}` |

无会话时这些路径都是 `401 {"error":"登录失败"}`。服务名非法时 `400`，正文含 `服务名不在允许范围内`。上游错误时 `502 {"error":"Nacos 请求失败"}`，响应里不含 Nacos 地址和密码。

`cmd/admin` 只在 `cfg.Nacos.Enabled && cfg.Nacos.Addr != ""` 时构造 `nacos.New`。否则传入 nil。`configs/admin.yaml` 和 `deploy/configs/admin.yaml` 增加 `nacos.enabled: false`、`addr: http://nacos:8848`、`username: nacos`、`password: ${NACOS_PASSWORD}`。

这些路径注册在 `/api/` 之前。

- [ ] **Step 1: Write the failing test**

`http_test.go` 使用 `auth.NewMemory` 和一张有效会话。nil client 时 `GET /api/config/source` 是 local。`GET /api/config/core` 是 404，正文 `Nacos 未打开`。用 `httptest` 充当 Nacos 时，`PUT` 后的 `GET` 返回刚发布的正文；`rollback` 发布 history 里的旧正文；非法服务名是 400；没有 cookie 是 401。

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/admin/nacosapi -count=1`

Expected: FAIL，包不存在。

- [ ] **Step 3: Write minimal implementation**

handler 内部持有 `*nacos.Client`。不要把 `accessToken`、用户名或密码写进日志或 JSON。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/admin/... ./cmd/admin -count=1`

Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/admin/config.go internal/admin/config_test.go internal/admin/nacosapi cmd/admin/main.go configs/admin.yaml deploy/configs/admin.yaml
git commit -m "feat: proxy Nacos config through the admin session"
```

不要提交 `cmd/admin/main_test.go`，除非它本来就只测本任务。现有未跟踪的 `cmd/admin/main_test.go` 是密码缺省测试，留在工作区。

---

### Task 8: 配置页和顶栏来源

**Files:**
- Create: `web/admin/src/views/Config.vue`
- Create: `web/admin/src/config.test.ts`
- Modify: `web/admin/src/router.ts`
- Modify: `web/admin/src/App.vue`

**Interfaces:**
- Consumes: Task 7 的 JSON。
- Produces: `/config` 渲染 `Config.vue`。顶栏第一枚标签在 `source == "nacos"` 时是「Nacos」，否则是「本地配置」。第二枚仍是「容器输出」。

`Config.vue` 先 `GET /api/config/source`，`credentials: "include"`。`enabled == false` 时渲染现有空状态，文字包含 `配置还没有功能`。`enabled == true` 时：

- 左侧四个服务按钮：`core`、`chatroom`、`gateway`、`webhook`。
- 右侧 `a-textarea` 显示 `content`。
- 按钮「发布」`PUT` 当前正文。
- 历史列表显示 `modified`。按钮「回滚」`POST {"id"}`。
- IP 输入框和按钮「灰度」，`PUT /beta`。按钮「停止灰度」`DELETE /beta`。
- 监听者列表显示 IP。没有监听者时显示「没有监听者」。
- 请求失败时显示「Nacos 请求失败」。

页面使用 `a-card`、`a-button`、`a-textarea`。不使用新的状态色。YAML 文本使用等宽字体。

- [ ] **Step 1: Write the failing test**

`config.test.ts`：

1. source 为 local 时，文字包含 `配置还没有功能`，不包含 `灰度`。
2. source 为 nacos 时，点击 `core` 后显示服务端返回的 `log:`，点击「发布」发出 `PUT /api/config/core`，`credentials` 是 `include`。
3. `App.vue` 的测试放在同一文件：source local 时文字包含 `本地配置`；把 fetch 改成 nacos 后文字包含 `Nacos`，且不再把「本地配置」当作来源标签。`容器输出` 和 `退出` 仍在。

路由测试改成 `/config` 的组件是 `Config`，不再是 `Section`。`/logs` 和 `/release` 仍是 `Section`。

- [ ] **Step 2: Run test to verify it fails**

Run: `npm test -- src/config.test.ts`，工作目录 `web/admin`。

Expected: FAIL，`Config.vue` 不存在。

- [ ] **Step 3: Write minimal implementation**

`App.vue` 在已登录布局里请求一次 `/api/config/source`。失败时标签保持「本地配置」。不要在关闭 Nacos 时改掉空状态文案。

- [ ] **Step 4: Run test to verify it passes**

Run: `npm test`，工作目录 `web/admin`。

Expected: 全部 PASS。

- [ ] **Step 5: Commit**

```bash
git add web/admin/src/views/Config.vue web/admin/src/config.test.ts web/admin/src/router.ts web/admin/src/App.vue
git commit -m "feat: edit Nacos config from the admin page"
```

若 `App.vue` 仍含未提交的 Arco 布局改动，这些改动随本任务一起提交，因为顶栏标签就在这个文件里。不要把 `internal/admin/static/dist` 算进这一步；镜像构建会重新生成。

---

### Task 9: compose 里的 Nacos 默认不启动

**Files:**
- Modify: `deploy/docker-compose.yml`
- Modify: `internal/admin/deploy/ports_test.go`

**Interfaces:**
- Consumes: 现有 compose。`yaml.v3` 不展开 `<<` 合并键，测试只读 `nacos` 服务自己的字段。

`nacos` 服务：

```yaml
nacos:
  image: nacos/nacos-server:v2.5.5
  profiles: ["nacos"]
  environment:
    MODE: standalone
    NACOS_AUTH_ENABLE: "true"
    NACOS_AUTH_TOKEN: SecretKey012345678901234567890123456789012345678901234567890123456789
    NACOS_AUTH_IDENTITY_KEY: serverIdentity
    NACOS_AUTH_IDENTITY_VALUE: security
```

不写 `ports`。`admin`、`core`、`chatroom`、`gateway`、`webhook` 不 `depends_on` nacos。它们的环境增加 `NACOS_PASSWORD: ${NACOS_PASSWORD:-nacos}`。不要给 Nacos 增加宿主机端口。

- [ ] **Step 1: Write the failing test**

在 `ports_test.go` 增加 `TestNacosStaysOffTheHost`。`nacos` 服务存在，`ports` 长度为 0，`profiles` 等于 `["nacos"]`，`image` 等于 `nacos/nacos-server:v2.5.5`。`core` 的 `depends_on` 解码后的原始映射里没有名为 nacos 的键。用 `yaml.Node` 看 `core` 的 `depends_on` 内容，避免被合并键干扰；如果 `depends_on` 不在 `core` 自己的节点上，就只断言文档全文的 `profiles` 段和 `nacos` 服务节点没有 `ports`。

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/admin/deploy -count=1 -run NacosStaysOffTheHost`

Expected: FAIL，服务不存在。

- [ ] **Step 3: Write minimal implementation**

只加 `nacos` 服务和五个服务的 `NACOS_PASSWORD`。不要改 builder 的卷，不要改 gateway 的端口。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/admin/deploy -count=1`

Expected: PASS。

- [ ] **Step 5: Commit**

提交前用 `git diff -- deploy/docker-compose.yml` 确认没有把无关 etcd 改动带进来。

```bash
git add deploy/docker-compose.yml internal/admin/deploy/ports_test.go
git commit -m "feat: keep Nacos off unless its compose profile is selected"
```

---

## Self-review

- 规格里的 Nacos 开关、Group、DataId、失败回退、Open API 历史、回滚、按 IP 灰度、监听者、浏览器不接触账号、端口不发布，分别落在 Task 1、2、7、8、9。
- 热更新的日志级别、core 缓存、webhook 缓存、gateway 审核落在 Task 3、4、5、6。chatroom 没有缓存和审核字段，所以只有日志级别。
- 监听端口、MySQL、Kafka 进入 `restart`，不调用对应客户端。
- 本地 YAML 编辑器、日志跟随、镜像发布、Elasticsearch 没有任务。
- Nacos 关闭时配置页文案和 `pkg/config.Load` 保持不动。
