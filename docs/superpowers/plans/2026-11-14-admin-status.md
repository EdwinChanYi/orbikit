# Admin Status Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 总览页显示四个平台服务的容器、gRPC 健康、etcd 实例、镜像 ID 和最近发布；集群页显示 etcd 健康、主节点、库大小、告警和实例地址。

**Architecture:** 新进程 `cmd/builder` 只在 compose 网络内监听 `:8092`，挂载 Docker 套接字，用 Docker Engine API 查询容器。`cmd/admin` 不挂载套接字。它通过 HTTP 调用 builder，直连固定的 gRPC 地址做健康检查，并用 etcd 前缀 `/orbikit/services` 列实例。etcd 查询失败时接口返回 `fault`，页面显示故障，不用空列表充当正常。最近发布这一列固定为「尚无发布」，发布写入留在 `2026-11-14-admin-release.md`。

**Tech Stack:** Go 1.27.4、标准库 `net/http`、`go.etcd.io/etcd/client/v3`、`google.golang.org/grpc/health/grpc_health_v1`、Vue 3、Arco Design Vue、vitest。

## Global Constraints

- 浏览器只访问管理后台 `8091`，以及网关 TCP `8000`、WebSocket `8001`。builder 的 `:8092` 不映射到宿主机。
- compose 不把 `core:9001`、`chatroom:9002`、网关 HTTP `8080`、etcd、MySQL、Redis、Kafka、webhook 映射到宿主机。
- Docker 套接字只挂在 `builder`。`admin` 不挂载它。`builder` 的 compose `user` 设为 `root`，这样才能读 `/var/run/docker.sock`。
- 容器查询白名单只有 `core`、`chatroom`、`gateway`、`webhook`。容器名只能由白名单拼出 `orbikit-<name>-1`。
- 网关不注册 etcd，也不做 gRPC 健康检查。它的 gRPC 列显示「不适用」，颜色是灰。
- core、chatroom、webhook 的健康检查地址分别是 `core:9001`、`chatroom:9002`、`webhook:9004`，服务名用空字符串。
- 状态色只有：正常 `#00b42a`，降级 `#ff7d00`，故障 `#f53f3f`，未知 `#86909c`。
- etcd 不可达、builder 不可达都是故障或未知，不把空实例显示成正常。
- `GET /api/overview` 和 `GET /api/cluster` 没有有效会话时返回 `401`，正文 `{"error":"登录失败"}`。
- 未设置 `ADMIN_PASSWORD` 时密码仍是 `123456`。这份计划不改登录哈希逻辑。
- 测试用标准库 `testing` 和 vitest，不引入 testify，不连接真实 Docker 或真实 etcd。
- 不在这份计划里实现 YAML 编辑、日志跟随、镜像构建、Nacos 或 Elasticsearch。

平台服务表：

| 服务 | 容器名 | gRPC | 是否注册 etcd |
|---|---|---|---|
| core | orbikit-core-1 | core:9001 | 是 |
| chatroom | orbikit-chatroom-1 | chatroom:9002 | 是 |
| webhook | orbikit-webhook-1 | webhook:9004 | 是 |
| gateway | orbikit-gateway-1 | 无 | 否 |

---

### Task 1: builder 的服务名白名单和容器查询

**Files:**
- Create: `internal/builder/inspect.go`
- Create: `internal/builder/inspect_test.go`
- Create: `internal/builder/http.go`
- Create: `internal/builder/http_test.go`
- Create: `cmd/builder/main.go`
- Create: `configs/builder.yaml`
- Create: `deploy/configs/builder.yaml`

**Interfaces:**
- Consumes: Docker Engine API `GET /containers/{name}/json`。
- Produces: `Allowed(name string) bool`；`ContainerName(project, name string) (string, error)`；`Inspector.Inspect(ctx, container string) (Info, error)`；`Info` 字段 `Status string`、`Running bool`、`ImageID string`；`NewHandler(ins Inspector, project string) http.Handler`，路径 `GET /v1/containers/{name}`。`cmd/builder` 监听配置 `http_addr`，默认 `:8092`。

- [ ] **Step 1: Write the failing test**

`internal/builder/inspect_test.go`：

```go
package builder

import "testing"

func TestAllowedNames(t *testing.T) {
	for _, name := range []string{"core", "chatroom", "gateway", "webhook"} {
		if !Allowed(name) {
			t.Fatalf("%s should be allowed", name)
		}
	}
	for _, name := range []string{"", "admin", "core;touch", "../core", "Core"} {
		if Allowed(name) {
			t.Fatalf("%q should be rejected", name)
		}
	}
}

func TestContainerNameUsesWhitelist(t *testing.T) {
	got, err := ContainerName("orbikit", "core")
	if err != nil || got != "orbikit-core-1" {
		t.Fatalf("name = %q, err = %v", got, err)
	}
	if _, err := ContainerName("orbikit", "admin"); err == nil {
		t.Fatal("admin was accepted")
	}
	if _, err := ContainerName("Orbikit", "core"); err == nil {
		t.Fatal("project with uppercase was accepted")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/builder -count=1`

Expected: FAIL，`Allowed` 未定义。

- [ ] **Step 3: Write minimal implementation**

`internal/builder/inspect.go`：

```go
package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
)

var serviceName = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

var allowed = map[string]bool{
	"core": true, "chatroom": true, "gateway": true, "webhook": true,
}

// Allowed reports whether name may be turned into a container query.
func Allowed(name string) bool {
	return allowed[name]
}

// ContainerName returns the compose container for one whitelist service.
func ContainerName(project, name string) (string, error) {
	if !serviceName.MatchString(project) || !Allowed(name) {
		return "", errors.New("服务名不在允许范围内")
	}
	return project + "-" + name + "-1", nil
}

// Info is one container's runtime state.
type Info struct {
	Status  string
	Running bool
	ImageID string
}

// Inspector reads one container by its exact Docker name.
type Inspector interface {
	Inspect(ctx context.Context, container string) (Info, error)
}

// Docker queries the Engine API through a Unix socket or a test base URL.
type Docker struct {
	Socket string
	Base   string
	Client *http.Client
}

// Inspect calls GET /containers/{container}/json.
func (d *Docker) Inspect(ctx context.Context, container string) (Info, error) {
	client := d.Client
	if client == nil {
		client = &http.Client{Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var dial net.Dialer
				return dial.DialContext(ctx, "unix", d.Socket)
			},
		}}
	}
	base := d.Base
	if base == "" {
		base = "http://docker"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/containers/"+container+"/json", nil)
	if err != nil {
		return Info{}, err
	}
	res, err := client.Do(req)
	if err != nil {
		return Info{}, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return Info{Status: "missing"}, nil
	}
	if res.StatusCode != http.StatusOK {
		return Info{}, fmt.Errorf("docker status %d", res.StatusCode)
	}
	var body struct {
		Image string `json:"Image"`
		State struct {
			Status  string `json:"Status"`
			Running bool   `json:"Running"`
		} `json:"State"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return Info{}, err
	}
	return Info{Status: body.State.Status, Running: body.State.Running, ImageID: body.Image}, nil
}
```

`internal/builder/http.go`：

```go
package builder

import (
	"encoding/json"
	"net/http"
)

// NewHandler serves GET /v1/containers/{name} for whitelist names only.
func NewHandler(ins Inspector, project string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/containers/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		container, err := ContainerName(project, name)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "服务名不在允许范围内"})
			return
		}
		info, err := ins.Inspect(r.Context(), container)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "容器查询失败"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"name": name, "status": info.Status, "running": info.Running, "image_id": info.ImageID,
		})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
```

`internal/builder/http_test.go` 使用一个返回固定 `Info` 的假 `Inspector`。请求 `/v1/containers/admin` 期望 `400` 和正文包含 `服务名不在允许范围内`。请求 `/v1/containers/core` 期望 `200`、`running` 为 true、`image_id` 为 `sha256:abc`。另用 `httptest.NewServer` 返回 Docker 的 `404`，确认 `Docker.Inspect` 得到 `Status == "missing"` 且 `err == nil`。

`configs/builder.yaml` 与 `deploy/configs/builder.yaml` 内容相同：

```yaml
http_addr: ":8092"
socket: /var/run/docker.sock
project: orbikit
shutdown_timeout: 10s
log:
  level: info
  file: ""
  max_size_mb: 1024
  max_age_days: 30
  console: true
```

`cmd/builder/main.go` 按 `cmd/admin/main.go` 的形状加载配置、初始化日志、监听 `http_addr`，处理函数是 `builder.NewHandler(&builder.Docker{Socket: cfg.Socket}, cfg.Project)`。不读取 `ADMIN_PASSWORD`，不映射额外端口。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/builder ./cmd/builder -count=1`

Expected: PASS。`cmd/builder` 可以没有测试文件。

- [ ] **Step 5: Commit**

```bash
git add internal/builder cmd/builder configs/builder.yaml deploy/configs/builder.yaml
git commit -m "feat: add the internal container inspector"
```

---

### Task 2: compose 把套接字只交给 builder

**Files:**
- Modify: `deploy/docker-compose.yml`
- Modify: `internal/admin/deploy/ports_test.go`
- Modify: `Makefile`
- Modify: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: Task 1 的 `cmd/builder` 和 `deploy/configs/builder.yaml`。
- Produces: compose 服务 `builder`。它没有 `ports`，`user` 是 `root`，volumes 含 `/var/run/docker.sock:/var/run/docker.sock`。`admin` 增加 `builder_url: http://builder:8092` 所用的依赖，且 admin 服务自身的 volumes 不含 `docker.sock`。`Makefile` 的 `SERVICES` 包含 `builder`。CI 的 docker matrix 包含 `builder`。

- [ ] **Step 1: Write the failing test**

在 `internal/admin/deploy/ports_test.go` 增加：

```go
func TestBuilderAloneMountsDockerSocket(t *testing.T) {
	raw, err := os.ReadFile("../../../deploy/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]struct {
			Ports   []string `yaml:"ports"`
			User    string   `yaml:"user"`
			Volumes []string `yaml:"volumes"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	builder := doc.Services["builder"]
	if len(builder.Ports) != 0 {
		t.Fatalf("builder ports = %v", builder.Ports)
	}
	if builder.User != "root" {
		t.Fatalf("builder user = %q", builder.User)
	}
	if len(builder.Volumes) != 1 || builder.Volumes[0] != "/var/run/docker.sock:/var/run/docker.sock" {
		t.Fatalf("builder volumes = %v", builder.Volumes)
	}
	for _, volume := range doc.Services["admin"].Volumes {
		if volume == "/var/run/docker.sock:/var/run/docker.sock" {
			t.Fatal("admin mounts the docker socket")
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/admin/deploy -count=1 -run TestBuilderAloneMountsDockerSocket`

Expected: FAIL，`builder` 服务不存在。

- [ ] **Step 3: Write minimal implementation**

在 `deploy/docker-compose.yml` 的 `admin` 服务前增加：

```yaml
  builder:
    <<: *service
    user: root
    build:
      context: ..
      args: { SERVICE: builder }
    command: ["-config", "/app/configs/builder.yaml"]
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
```

`admin.depends_on` 增加 `builder: { condition: service_started }`。不要给 builder 写 `ports`。

`Makefile` 第一行改为：

```makefile
SERVICES := core chatroom gateway webhook admin builder
```

`.github/workflows/ci.yml` 的 docker matrix 改为：

```yaml
service: [core, chatroom, gateway, webhook, admin, builder]
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/admin/deploy -count=1`

Expected: PASS。原有端口测试仍通过。

- [ ] **Step 5: Commit**

```bash
git add deploy/docker-compose.yml internal/admin/deploy/ports_test.go Makefile .github/workflows/ci.yml
git commit -m "feat: run the container inspector inside compose"
```

---

### Task 3: 集群快照

**Files:**
- Create: `internal/admin/status/cluster.go`
- Create: `internal/admin/status/cluster_test.go`
- Create: `internal/admin/status/etcd.go`

**Interfaces:**
- Consumes: etcd 前缀 `/orbikit/services/<service>/<id>`，值是 `host:port`。
- Produces: `Source` 接口，方法 `Cluster(ctx) (Cluster, error)`。`Cluster` 字段：`Status string`（`ok`、`degraded`、`fault`）、`Leader string`、`DBSize int64`、`Alarms []string`、`Instances []Instance`、`Error string`。`Instance` 字段：`Service string`、`ID string`、`Addr string`。查询失败时 `Status` 是 `fault`，`Instances` 是 nil。

- [ ] **Step 1: Write the failing test**

`internal/admin/status/cluster_test.go` 定义假 `Source`。另外直接测试纯函数 `Interpret(leader string, dbSize int64, alarms []string, instances []Instance, queryErr error) Cluster`：

- `queryErr != nil` 时 `Status == "fault"`，`Instances == nil`，`Error` 非空。
- 无错误、无告警时 `Status == "ok"`，实例原样返回。
- 无错误但 `alarms` 含 `NOSPACE` 时 `Status == "degraded"`，实例仍返回。

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/admin/status -count=1 -run TestInterpret`

Expected: FAIL，`Interpret` 未定义。

- [ ] **Step 3: Write minimal implementation**

`internal/admin/status/cluster.go`：

```go
package status

import (
	"context"
	"errors"
	"strings"
)

// Instance is one registered process.
type Instance struct {
	Service string `json:"service"`
	ID      string `json:"id"`
	Addr    string `json:"addr"`
}

// Cluster is the etcd snapshot shown on the cluster page.
type Cluster struct {
	Status    string     `json:"status"`
	Leader    string     `json:"leader,omitempty"`
	DBSize    int64      `json:"db_size,omitempty"`
	Alarms    []string   `json:"alarms,omitempty"`
	Instances []Instance `json:"instances"`
	Error     string     `json:"error,omitempty"`
}

// Source loads one cluster snapshot.
type Source interface {
	Cluster(ctx context.Context) (Cluster, error)
}

// Interpret maps an etcd read to the page status.
func Interpret(leader string, dbSize int64, alarms []string, instances []Instance, queryErr error) Cluster {
	if queryErr != nil {
		return Cluster{Status: "fault", Error: queryErr.Error()}
	}
	out := Cluster{Status: "ok", Leader: leader, DBSize: dbSize, Alarms: alarms, Instances: instances}
	if len(alarms) > 0 {
		out.Status = "degraded"
	}
	return out
}

// ParseInstanceKey reads /orbikit/services/<service>/<id>.
func ParseInstanceKey(prefix, key string) (Instance, bool) {
	rest, ok := strings.CutPrefix(key, strings.TrimRight(prefix, "/")+"/")
	if !ok {
		return Instance{}, false
	}
	service, id, ok := strings.Cut(rest, "/")
	if !ok || service == "" || id == "" || strings.Contains(id, "/") {
		return Instance{}, false
	}
	return Instance{Service: service, ID: id}, true
}

// ErrEtcd is returned when the cluster cannot be read.
var ErrEtcd = errors.New("etcd unreachable")
```

`internal/admin/status/etcd.go` 使用 `clientv3.Client`。`Cluster` 依次调用 `Status`、`MemberList`、`AlarmList`、`Get(prefix, WithPrefix())`。任一步失败就 `return Interpret("", 0, nil, nil, ErrEtcd)`。`Status.Leader` 与成员 ID 匹配得到主节点名。告警类型转成字符串，忽略 `NONE`。键用 `ParseInstanceKey`，值是地址。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/admin/status -count=1`

Expected: PASS。测试覆盖 `fault` 时 `Instances` 为 nil、`NOSPACE` 为 `degraded`、键 `/orbikit/services/core/abc` 解析成 service `core` 和 id `abc`。

- [ ] **Step 5: Commit**

```bash
git add internal/admin/status/cluster.go internal/admin/status/cluster_test.go internal/admin/status/etcd.go
git commit -m "feat: read the etcd cluster snapshot"
```

---

### Task 4: 总览聚合

**Files:**
- Create: `internal/admin/status/overview.go`
- Create: `internal/admin/status/overview_test.go`
- Modify: `internal/admin/config.go`
- Modify: `configs/admin.yaml`
- Modify: `deploy/configs/admin.yaml`

**Interfaces:**
- Consumes: `Cluster`、`Instance`，以及 builder 的 JSON `name/status/running/image_id`。
- Produces: `Overview` 与 `ServiceRow`。`GET` builder 的路径是 `{builder_url}/v1/containers/{name}`。行字段：`name`、`container`（`running`、`exited`、`missing`、`unknown`）、`grpc`（`serving`、`not_serving`、`skipped`、`unknown`）、`instances`、`image_id`、`last_release`。`last_release` 永远是 JSON `null`。

- [ ] **Step 1: Write the failing test**

假 builder 返回 core 运行且镜像 `sha256:abc`，对 gateway 返回 HTTP 500。假健康检查对 `core:9001` 返回 `serving`。假集群返回 core 的一个实例，且 `Status == "ok"`。调用 `Collect`。

期望：core 的 `container` 是 `running`，`grpc` 是 `serving`，`image_id` 是 `sha256:abc`，实例地址保留，`last_release` 是空字符串。gateway 的 `container` 是 `unknown`，`grpc` 是 `skipped`。把集群改成 `fault` 后再收集，所有 `register == true` 的行 `instances` 为 nil。

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/admin/status -count=1 -run TestCollect`

Expected: FAIL，`Collect` 未定义。

- [ ] **Step 3: Write minimal implementation**

`internal/admin/status/overview.go` 中的固定表：

```go
var Services = []struct {
	Name     string
	GRPC     string
	Register bool
}{
	{Name: "core", GRPC: "core:9001", Register: true},
	{Name: "chatroom", GRPC: "chatroom:9002", Register: true},
	{Name: "webhook", GRPC: "webhook:9004", Register: true},
	{Name: "gateway", Register: false},
}
```

`Health` 接口是 `Check(ctx, addr string) (string, error)`，成功值只接受 `serving`。builder 非 200 或网络错误把该行 `container` 设为 `unknown`。`running == true` 映射为 `running`；`status == "missing"` 映射为 `missing`；其他非运行状态映射为 `exited`。gRPC 地址为空时写 `skipped`，不拨号。集群 `Status == "fault"` 时，`Register` 为 true 的行把 `instances` 置为 nil。

`configs/admin.yaml` 增加：

```yaml
builder_url: http://127.0.0.1:8092
etcd:
  endpoints: ["127.0.0.1:2379"]
  prefix: /orbikit/services
```

`deploy/configs/admin.yaml` 使用 `http://builder:8092` 和 `etcd:2379`。`internal/admin/config.go` 增加 `BuilderURL string` 和 `Etcd discovery.Config`。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/admin/... -count=1`

Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/admin/status/overview.go internal/admin/status/overview_test.go internal/admin/config.go configs/admin.yaml deploy/configs/admin.yaml
git commit -m "feat: aggregate platform service status"
```

---

### Task 5: 把状态接口接到登录会话

**Files:**
- Modify: `internal/admin/auth/http.go`
- Modify: `internal/admin/auth/http_test.go`
- Create: `internal/admin/status/http.go`
- Create: `internal/admin/status/http_test.go`
- Modify: `cmd/admin/main.go`

**Interfaces:**
- Consumes: `auth.Store`、`Collect`、`Source`。
- Produces: `auth.RequireSession(store Store, now func() time.Time, next http.Handler) http.Handler`。`status.NewHandler(view View) http.Handler` 提供 `GET /api/overview` 和 `GET /api/cluster`。`cmd/admin` 在 `/api/` 之前注册这两个路径。

- [ ] **Step 1: Write the failing test**

无 cookie 请求 `GET /api/overview` 得到 `401` 和 `{"error":"登录失败"}`。用 `auth.NewMemory` 写入一个未过期会话后，同一个请求得到 `200`，JSON 含 `"name":"core"` 和 `"last_release":null`。集群假数据为 `fault` 时，`GET /api/cluster` 的 JSON 含 `"status":"fault"`，且 `instances` 键的值是 `null`。

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/admin/auth ./internal/admin/status -count=1 -run 'TestRequireSession|TestStatusRoutes'`

Expected: FAIL，路由不存在。

- [ ] **Step 3: Write minimal implementation**

`RequireSession` 复用 `sessionHash` 和 `Store.SessionValid`。无效时调用现有的 `writeUnauthorized`。`status.NewHandler` 只编码 `Collect` 和 `Source.Cluster` 的结果。`cmd/admin/main.go` 的 `routes` 改为接收 `builderURL` 和 etcd `Source`，并先注册：

```go
mux.Handle("/api/overview", auth.RequireSession(store, nil, statusHandler))
mux.Handle("/api/cluster", auth.RequireSession(store, nil, statusHandler))
mux.Handle("/api/", auth.NewHandler(store, password, nil))
```

builder 客户端不可达时，`Collect` 仍返回四行，容器状态为 `unknown`，不让整个接口 500。etcd `Cluster` 返回的 `fault` 原样写入 JSON。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/admin/... ./cmd/admin -count=1`

Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/admin/auth/http.go internal/admin/auth/http_test.go internal/admin/status/http.go internal/admin/status/http_test.go cmd/admin/main.go
git commit -m "feat: serve overview and cluster to the operator"
```

---

### Task 6: 总览页和集群页

**Files:**
- Create: `web/admin/src/views/Overview.vue`
- Create: `web/admin/src/views/Cluster.vue`
- Create: `web/admin/src/overview.test.ts`
- Create: `web/admin/src/cluster.test.ts`
- Modify: `web/admin/src/router.ts`

**Interfaces:**
- Consumes: `GET /api/overview` 与 `GET /api/cluster`，cookie 使用 `credentials: "include"`。
- Produces: `/` 渲染总览表，`/cluster` 渲染集群页。配置、日志、发布仍渲染原来的 `Section`。

- [ ] **Step 1: Write the failing test**

`overview.test.ts` 挂载 `Overview`。fetch 返回四行，其中 core 为 `running`/`serving`，etcd 页面字段 `etcd` 为 `fault`，gateway 的 `grpc` 为 `skipped`。期望文字包含「故障」「运行中」「不适用」「尚无发布」，并且「尚无发布」的元素 class 包含 `status-unknown`。「运行中」的 class 是 `status-ok`。故障响应里不出现把实例数显示成正常的「0 个实例」。

`cluster.test.ts` 先返回 `status: "fault"`，期望文字包含「故障」，不含「正常」。再返回 `status: "ok"`、leader `etcd`、一个地址 `core:9001`，期望两段文字都出现。

- [ ] **Step 2: Run test to verify it fails**

Run: `npm test -- src/overview.test.ts src/cluster.test.ts`

工作目录：`web/admin`。

Expected: FAIL，组件文件不存在。

- [ ] **Step 3: Write minimal implementation**

两个页面在加载失败或 `status === "fault"` 时显示红色文字「故障」。总览列是：服务、容器、gRPC、实例、镜像、最近发布。`last_release === null` 显示「尚无发布」。实例在 `instances === null` 时显示「故障」，数组为空且该服务 `grpc === "skipped"` 时显示「不适用」。颜色 class：

```css
.status-ok { color: #00b42a; }
.status-degraded { color: #ff7d00; }
.status-fault { color: #f53f3f; }
.status-unknown { color: #86909c; }
```

`router.ts` 把 `/` 的组件换成 `Overview`，把 `/cluster` 的组件换成 `Cluster`。路由守卫保持不变。

- [ ] **Step 4: Run test to verify it passes**

Run: `npm test`

工作目录：`web/admin`。

Expected: PASS，包含原来的登录测试。

- [ ] **Step 5: Commit**

```bash
git add web/admin/src/views/Overview.vue web/admin/src/views/Cluster.vue web/admin/src/overview.test.ts web/admin/src/cluster.test.ts web/admin/src/router.ts
git commit -m "feat: show service and etcd status"
```

---

### Task 7: 浏览器复核

**Files:**
- Modify: `internal/admin/static/dist/`（`npm run build` 生成）

**Interfaces:**
- Consumes: Task 1 到 Task 6。
- Produces: 重新构建的 admin 镜像和正在运行的 builder。

- [ ] **Step 1: Build the page and recreate the two containers**

工作目录 `web/admin` 执行 `npm run build`。仓库根目录执行：

```bash
docker compose -f deploy/docker-compose.yml up -d --build admin builder
```

- [ ] **Step 2: Check the pages in the browser**

打开 `http://localhost:8091/`，用 `123456` 登录。总览表有 core、chatroom、gateway、webhook 四行。当前容器在运行时，容器列是绿色「运行中」。gateway 的 gRPC 列是灰色「不适用」。最近发布列是灰色「尚无发布」。打开集群页，能看到主节点、库大小和实例地址。用开发者工具把 `/api/cluster` 的响应改不成目标；这一步不需要真的停 etcd。停 etcd 的检查用：

```bash
docker compose -f deploy/docker-compose.yml stop etcd
```

刷新总览和集群，两页都出现红色「故障」，不出现绿色「正常」的空实例表。检查后执行 `docker compose -f deploy/docker-compose.yml start etcd`。

从宿主机确认 `127.0.0.1:8092` 连不上。

- [ ] **Step 3: Commit the built page**

```bash
git add internal/admin/static/dist
git commit -m "feat: publish the status pages"
```
