# Admin Shell Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在单机 compose 上提供可登录的管理后台，并收掉 core、chatroom、网关 HTTP 的宿主机端口。

**Architecture:** `cmd/admin` 是一个 Go HTTP 进程，托管 Vue 3 页面。登录状态存在 MySQL 库 `orbikit_admin`。这一份计划不包含 builder、配置编辑、日志、构建、Nacos 和 Elasticsearch。那些各自有后续计划，并且依赖这里的页面壳和端口边界。

**Tech Stack:** Go 1.27.4、Vue 3、Vite、Arco Design Vue、MySQL 8、`golang.org/x/crypto/bcrypt`、标准库 `net/http` 与 `testing`。

## Global Constraints

- 浏览器只访问管理后台 `8091`，以及网关 TCP `8000`、WebSocket `8001`。
- compose 不把 `core:9001`、`chatroom:9002`、网关 HTTP `8080`、etcd、MySQL、Redis、Kafka、webhook 映射到宿主机。
- 登录账号是平台操作员，用户名固定为 `admin`。失败响应正文都是 `登录失败`。
- 密码来自环境变量 `ADMIN_PASSWORD`，不写进仓库。cookie 名 `orbikit_session`，HttpOnly，SameSite=Lax，不标记 Secure。
- 历史库名是 `orbikit_admin`，不使用业务库 `orbikit`。
- 本机 `configs/*.yaml` 和 `go run ./cmd/*` 保持可用。收口的是 compose 的端口映射。
- 测试用标准库 `testing`，不引入 testify。
- 不在这一份计划里实现配置发布、etcd 页面数据、日志、构建或 Nacos。

后续计划（本文件不实现）：

1. `2026-11-14-admin-status.md`：builder、总览和集群状态。
2. `2026-11-14-admin-config.md`：本地 YAML 的编辑、diff、历史、重启和回滚。
3. `2026-11-14-admin-logs.md`：容器日志跟随。
4. `2026-11-14-admin-release.md`：按 Git tag 或完整 SHA 构建并按镜像 ID 替换。
5. `2026-11-14-admin-optional.md`：Nacos 与 Elasticsearch 开关，默认关闭。

---

### Task 1: compose 端口收口

**Files:**
- Create: `internal/admin/deploy/ports_test.go`
- Modify: `deploy/docker-compose.yml`
- Modify: `README.md`

**Interfaces:**
- Consumes: 现有 compose 服务名 `core`、`chatroom`、`gateway`
- Produces: 测试 `TestComposeDoesNotPublishInternalPorts`。gateway 只保留宿主机端口 `8000` 和 `8001`。

- [ ] **Step 1: Write the failing test**

```go
package deploy

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestComposeDoesNotPublishInternalPorts(t *testing.T) {
	raw, err := os.ReadFile("../../../deploy/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]struct {
			Ports []string `yaml:"ports"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if ports := doc.Services["core"].Ports; len(ports) != 0 {
		t.Fatalf("core ports = %v", ports)
	}
	if ports := doc.Services["chatroom"].Ports; len(ports) != 0 {
		t.Fatalf("chatroom ports = %v", ports)
	}
	got := doc.Services["gateway"].Ports
	want := []string{"8000:8000", "8001:8001"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("gateway ports = %v, want %v", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/admin/deploy -count=1`

Expected: FAIL，因为 `core` 仍有 `9001:9001`，或测试文件所在目录还不存在导致包无法编译。先创建只含测试的目录后再运行。失败原因应是端口断言，不是找不到 compose 文件。

- [ ] **Step 3: Write minimal implementation**

从 `deploy/docker-compose.yml` 删除 `core.ports` 和 `chatroom.ports`。`gateway.ports` 只留：

```yaml
    ports:
      - "8000:8000"
      - "8001:8001"
```

在 README「快速开始」里，`make up` 之后的 quickstart 改为在 compose 网络中运行，因为宿主机已经连不上 `localhost:9001`：

```bash
docker compose -f deploy/docker-compose.yml run --rm tools-quickstart -uid 10001 -room 1001
```

在 compose 增加仅工具用的服务，不映射端口，使用与业务服务相同的构建，但 `SERVICE` 不能用现有 Dockerfile（它只编译 `cmd/`）。这个服务用官方 Go 镜像直接跑示例：

```yaml
  tools-quickstart:
    image: golang:1.27.4
    profiles: ["tools"]
    working_dir: /src
    volumes: ["..:/src"]
    entrypoint: ["go", "run", "./examples/quickstart", "-core", "core:9001", "-chatroom", "chatroom:9002"]
    environment:
      GOPROXY: https://proxy.golang.org,direct
```

`docker compose run` 会启用 profile。README 写明：本机 `go run ./cmd/core -config configs/core.yaml` 时，quickstart 仍使用默认的 `localhost:9001`。`examples/web` 对着 compose 使用时同样要加入该网络，并传 `-core core:9001`；浏览器访问的 `8090` 由这个示例进程自己监听，不在本任务映射网关 `8080`。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/admin/deploy -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/admin/deploy/ports_test.go deploy/docker-compose.yml README.md
git commit -m "fix: stop publishing internal service ports from compose"
```

### Task 2: 操作员与会话存储

**Files:**
- Create: `internal/admin/auth/store.go`
- Create: `internal/admin/auth/memory.go`
- Create: `internal/admin/auth/mysql.go`
- Test: `internal/admin/auth/store_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Consumes: 无
- Produces:
  - `type Store interface { EnsureOperator(ctx context.Context, passwordHash []byte) error; PasswordHash(ctx context.Context) ([]byte, error); CreateSession(ctx context.Context, tokenHash []byte, expires time.Time) error; SessionValid(ctx context.Context, tokenHash []byte, now time.Time) (bool, error); DeleteSession(ctx context.Context, tokenHash []byte) error }`
  - `func NewMemory() *Memory`
  - `func OpenMySQL(ctx context.Context, dsn string) (*MySQL, error)`
  - `var ErrNoOperator = errors.New("no operator")`

- [ ] **Step 1: Write the failing test**

```go
package auth

import (
	"context"
	"bytes"
	"testing"
	"time"
)

func TestMemoryOperatorAndSession(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	hash := []byte("hash")
	if err := s.EnsureOperator(ctx, hash); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureOperator(ctx, []byte("other")); err != nil {
		t.Fatal(err)
	}
	got, err := s.PasswordHash(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, hash) {
		t.Fatalf("hash changed to %s", got)
	}
	exp := time.Now().Add(time.Hour)
	token := []byte("tok")
	if err := s.CreateSession(ctx, token, exp); err != nil {
		t.Fatal(err)
	}
	ok, err := s.SessionValid(ctx, token, time.Now())
	if err != nil || !ok {
		t.Fatalf("valid = %v, %v", ok, err)
	}
	ok, err = s.SessionValid(ctx, token, exp.Add(time.Second))
	if err != nil || ok {
		t.Fatalf("expired valid = %v, %v", ok, err)
	}
	if err := s.DeleteSession(ctx, token); err != nil {
		t.Fatal(err)
	}
	ok, err = s.SessionValid(ctx, token, time.Now())
	if err != nil || ok {
		t.Fatalf("deleted valid = %v, %v", ok, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/admin/auth -count=1`

Expected: FAIL，`NewMemory` 未定义。

- [ ] **Step 3: Write minimal implementation**

`store.go` 定义接口和 `ErrNoOperator`。`memory.go` 用互斥锁保存一份密码哈希和 `map[string]time.Time`。`EnsureOperator` 在已有哈希时直接返回。`PasswordHash` 没有记录时返回 `ErrNoOperator`。

`mysql.go` 使用已有依赖 `gorm.io/driver/mysql` 和 `gorm.io/gorm`：

- DSN 指向 `orbikit_admin`。
- 若库不存在，用去掉数据库名的 DSN 执行 `CREATE DATABASE IF NOT EXISTS orbikit_admin`，再打开目标库。
- `AutoMigrate` 两张表：`operators(id, password_hash)` 和 `sessions(token_hash, expires_at)`。`token_hash` 是主键。
- `EnsureOperator` 在 `Count == 0` 时插入，已有行不更新。

执行 `go get golang.org/x/crypto@v0.26.0`，与 `go.mod` 里现有间接依赖版本对齐。bcrypt 在 Task 3 使用，这一步只把依赖变成直接依赖。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/admin/auth -count=1`

Expected: PASS。MySQL 实现不在这个测试里连接真实数据库。

- [ ] **Step 5: Commit**

```bash
git add internal/admin/auth go.mod go.sum
git commit -m "feat: store the admin operator and sessions"
```

### Task 3: 登录 HTTP API

**Files:**
- Create: `internal/admin/auth/http.go`
- Create: `internal/admin/auth/http_test.go`
- Create: `internal/admin/config.go`
- Create: `internal/admin/config_test.go`
- Create: `configs/admin.yaml`
- Create: `deploy/configs/admin.yaml`

**Interfaces:**
- Consumes: Task 2 的 `Store`
- Produces:
  - `func NewHandler(store Store, password []byte, now func() time.Time) http.Handler`
  - `const CookieName = "orbikit_session"`
  - `const SessionTTL = 12 * time.Hour`
  - `func DefaultConfig() Config`，字段 `HTTPAddr string`、`MySQLDSN string`、`Log log.Config`、`ShutdownTimeout time.Duration`

- [ ] **Step 1: Write the failing test**

```go
package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestLoginRejectsWrongPasswordWithSameBody(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemory()
	if err := store.EnsureOperator(context.Background(), hash); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(store, []byte("secret"), time.Now)
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"password":"nope"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != `{"error":"登录失败"}` {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if rec.Result().Cookies() != nil && len(rec.Result().Cookies()) > 0 {
		t.Fatal("failed login set a cookie")
	}
}

func TestLoginThenMe(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemory()
	if err := store.EnsureOperator(context.Background(), hash); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(store, []byte("secret"), func() time.Time { return time.Unix(0, 0) })
	login := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"password":"secret"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, login)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("login status = %d, body %s", rec.Code, rec.Body.String())
	}
	cookie := rec.Result().Cookies()
	if len(cookie) != 1 || cookie[0].Name != CookieName || !cookie[0].HttpOnly {
		t.Fatalf("cookie = %+v", cookie)
	}
	me := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	me.AddCookie(cookie[0])
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, me)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"username":"admin"}` {
		t.Fatalf("me = %d %s", rec.Code, rec.Body.String())
	}
}
```

`internal/admin/config_test.go`：

```go
package admin

import (
	"strings"
	"testing"

	"github.com/EdwinChanYi/orbikit/pkg/config"
)

func TestDefaultListenAddr(t *testing.T) {
	if DefaultConfig().HTTPAddr != ":8091" {
		t.Fatalf("addr = %s", DefaultConfig().HTTPAddr)
	}
}

func TestExampleConfig(t *testing.T) {
	t.Setenv("MYSQL_PASSWORD", "secret")
	var cfg Config
	if err := config.Load("../../configs/admin.yaml", &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":8091" {
		t.Fatalf("addr = %s", cfg.HTTPAddr)
	}
	if cfg.MySQLDSN == "" || strings.Contains(cfg.MySQLDSN, "${") {
		t.Fatalf("dsn = %s", cfg.MySQLDSN)
	}
}
```

YAML 里的 DSN 写为 `root:${MYSQL_PASSWORD}@tcp(localhost:3306)/orbikit_admin?charset=utf8mb4&parseTime=true&loc=Local`。

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/admin/auth ./internal/admin -count=1`

Expected: FAIL，`NewHandler` 未定义。

- [ ] **Step 3: Write minimal implementation**

`NewHandler` 注册：

- `POST /api/login`：JSON `{"password":"..."}`。用 `bcrypt.CompareHashAndPassword`。失败、空密码、JSON 不合法都返回 401 和 `{"error":"登录失败"}`。成功生成 32 字节随机 token，cookie 里放十六进制原文，库里保存 `sha256` 原文。响应 `204`，cookie `Path=/`、`HttpOnly`、`SameSite=Lax`、`Secure=false`、`Max-Age` 为 12 小时。
- `POST /api/logout`：删除会话并清空 cookie，返回 `204`。
- `GET /api/me`：cookie 对应会话有效时返回 `{"username":"admin"}`，否则 `401` 和 `{"error":"登录失败"}`。

`Config` 嵌入 `pkg/log.Config`。`configs/admin.yaml` 的 MySQL 主机是 `localhost`。`deploy/configs/admin.yaml` 的主机是 `mysql`。两份都不写 `ADMIN_PASSWORD`。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/admin/... -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/admin configs/admin.yaml deploy/configs/admin.yaml go.mod go.sum
git commit -m "feat: add the admin login API"
```

### Task 4: 进程入口

**Files:**
- Create: `cmd/admin/main.go`
- Create: `internal/admin/static/static.go`
- Create: `web/admin/dist/index.html`

**Interfaces:**
- Consumes: `admin.DefaultConfig`、`admin.Config`、`auth.OpenMySQL`、`auth.NewHandler`、`config.Load`、`log.Init`
- Produces: `go run ./cmd/admin -config configs/admin.yaml` 在设置 `ADMIN_PASSWORD` 和可达 MySQL 后监听 `:8091`

- [ ] **Step 1: Write the failing test**

在 `internal/admin/static/static_test.go`：

```go
package static

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIndexExists(t *testing.T) {
	h := Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if len(body) == 0 {
		t.Fatal("empty index")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/admin/static -count=1`

Expected: FAIL，`Handler` 未定义。

- [ ] **Step 3: Write minimal implementation**

`static.go` 用 `//go:embed all:dist` 嵌入 `dist`，`Handler()` 把 `/` 和未知路径回退到 `dist/index.html`，`/assets/` 按文件返回。先放一个占位 `web/admin/dist/index.html`，内容是 `<!doctype html><title>orbikit</title>`。Task 5 的 Vite 构建会覆盖 `dist`。

`cmd/admin/main.go` 按 `cmd/webhook/main.go` 的形状：

1. `flag.String("config", "configs/admin.yaml", ...)`
2. `config.Load`、`log.Init(cfg.Log, "admin")`
3. 读取 `ADMIN_PASSWORD`，空则返回错误 `ADMIN_PASSWORD is required`
4. `auth.OpenMySQL`，`bcrypt.GenerateFromPassword` 后 `EnsureOperator`
5. `http.Server` 监听 `cfg.HTTPAddr`。`/api/` 交给 `auth.NewHandler`，其余交给 `static.Handler`
6. `SIGINT`/`SIGTERM` 后在 `ShutdownTimeout` 内 `Shutdown`

占位页面还没有登录表单。Task 5 替换它。这一步的验收是进程能编译，并且 `/` 返回占位 HTML。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/admin/static ./cmd/admin -count=1`

Expected: PASS。`cmd/admin` 没有测试时，`go test` 只编译它。

- [ ] **Step 5: Commit**

```bash
git add cmd/admin internal/admin/static web/admin/dist/index.html
git commit -m "feat: start the admin HTTP process"
```

### Task 5: Vue 登录壳

**Files:**
- Create: `web/admin/package.json`
- Create: `web/admin/vite.config.ts`
- Create: `web/admin/index.html`
- Create: `web/admin/src/main.ts`
- Create: `web/admin/src/App.vue`
- Create: `web/admin/src/router.ts`
- Create: `web/admin/src/views/Login.vue`
- Create: `web/admin/src/views/Section.vue`
- Create: `web/admin/src/login.test.ts`
- Modify: `Dockerfile`
- Modify: `Makefile`
- Modify: `deploy/docker-compose.yml`
- Modify: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: `POST /api/login`、`GET /api/me`、cookie `orbikit_session`
- Produces: `web/admin` 的 `npm test` 与 `npm run build`。构建结果写到 `web/admin/dist`，由 Task 4 的 embed 提供。

- [ ] **Step 1: Write the failing test**

`web/admin/package.json` 包含 `vue`、`vue-router`、`@arco-design/web-vue`、`vite`、`vitest`、`@vue/test-utils`、`jsdom`。脚本：`"test": "vitest run"`、`"build": "vite build"`。

`login.test.ts` 用 `@vue/test-utils` 挂载 `Login.vue`。把 `fetch` 换成返回 401 的函数，点击登录按钮后，页面文本包含 `登录失败`。

- [ ] **Step 2: Run test to verify it fails**

Run: `npm test`，工作目录 `web/admin`

Expected: FAIL，`Login.vue` 不存在。

- [ ] **Step 3: Write minimal implementation**

`Login.vue` 只有密码框和按钮。提交时 `fetch("/api/login", { method: "POST", credentials: "include", headers: {"Content-Type":"application/json"}, body: JSON.stringify({password}) })`。状态不是 204 时显示 `登录失败`。204 时 `router.push("/")`。

`router.ts` 路由：`/login`，以及 `/`、`/config`、`/cluster`、`/logs`、`/release`。除 `/login` 外，进入前请求 `/api/me`，401 则转到 `/login`。

`App.vue` 在已登录路由使用 Arco 的 `Layout`：左侧菜单五项，标题分别为总览、配置、集群、日志、发布。内容区渲染 `router-view`。`Section.vue` 只显示路由名，不请求业务数据。

`vite.config.ts` 的 `build.outDir` 是 `dist`，开发服务器把 `/api` 代理到 `http://127.0.0.1:8091`。

`Dockerfile` 在 `go build` 之前：

```dockerfile
ARG SERVICE
RUN if [ "$SERVICE" = "admin" ]; then \
      apk add --no-cache nodejs npm && \
      npm ci --prefix web/admin && \
      npm run build --prefix web/admin; \
    fi
RUN test -n "$SERVICE" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/${SERVICE}
```

`Makefile` 的 `SERVICES` 改为 `core chatroom gateway webhook admin`。

compose 增加 `admin`：

```yaml
  admin:
    <<: *service
    build:
      context: ..
      args: { SERVICE: admin }
    command: ["-config", "/app/configs/admin.yaml"]
    ports: ["8091:8091"]
    environment:
      MYSQL_PASSWORD: orbikit
      ADMIN_PASSWORD: ${ADMIN_PASSWORD:?set ADMIN_PASSWORD}
    depends_on:
      mysql: { condition: service_healthy }
```

CI 的 test job 在 `go build` 之前增加 Node 22，并在 `web/admin` 执行 `npm ci && npm test && npm run build`。docker job 的矩阵加上 `admin`。

- [ ] **Step 4: Run test to verify it passes**

Run: `npm test`，工作目录 `web/admin`

然后：`npm run build && go test ./internal/admin/... -count=1`

Expected: PASS。`go test ./internal/admin/static` 仍返回 `index.html`。

- [ ] **Step 5: Commit**

```bash
git add web/admin Dockerfile Makefile deploy/docker-compose.yml .github/workflows/ci.yml
git commit -m "feat: add the admin login shell"
```

不要把 `web/admin/node_modules` 加进去。`dist` 可以由构建生成；若 `static` 的 embed 在干净检出时需要 `dist/index.html`，保留 Task 4 的占位文件，直到 `npm run build` 覆盖它。`.gitignore` 不要忽略 `web/admin/dist/index.html` 这个占位，可以忽略 `web/admin/dist/assets`。
