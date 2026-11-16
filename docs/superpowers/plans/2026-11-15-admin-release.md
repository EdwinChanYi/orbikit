# 管理后台镜像发布 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让发布页按 tag 或完整 SHA 为本机六个平台服务构建镜像并替换容器，任务结果写入 `orbikit_admin`。

**Architecture:** admin 创建任务并调用 builder。builder 只启动一次性容器后返回。一次性容器使用 builder 镜像的 `-release` 模式，直接把结果写回任务表。页面轮询任务，不依赖被替换的进程送完这次 HTTP 响应。

**Tech Stack:** Go 1.27.4、gorm mysql、Docker Engine API、Git、Vue 3、Arco Design Vue、vitest。

## Global Constraints

- 服务只允许 `core`、`chatroom`、`gateway`、`webhook`、`admin`、`builder`。
- 引用只允许 tag `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$` 或 SHA `^[0-9a-fA-F]{40}$`。更短的纯十六进制拒绝。
- 仓库固定 `https://github.com/EdwinChanYi/orbikit.git`。只拉取 tag 或这一枚 SHA，不拉取分支。
- 凭证是 `ORBIKIT_GIT_USERNAME` 和 `ORBIKIT_GIT_TOKEN`。浏览器和仓库都不保存。
- 同一时间只有一个 `queued` 或 `running` 任务。超过 30 分钟由下一次查询改为失败，原因「超时」。
- 输出只保留末尾 256KB。镜像不推仓库。一次性容器不写进 compose。
- 现有容器查询白名单仍拒绝 `admin` 和 `builder`。总览仍是四个业务服务。
- 不修改 Nacos 配置正文。

---

### Task 1: 引用、任务表和执行器

**Files:**
- Create: `internal/release/ref.go`
- Create: `internal/release/store.go`
- Create: `internal/release/mysql.go`
- Create: `internal/release/runner.go`
- Create: `internal/release/git.go`
- Create: `internal/release/docker.go`
- Test: `internal/release/*_test.go`

**Interfaces:**
- Produces: `ParseRef`, `Allowed`, `ContainerName`, `Store`, `Run(ctx, Deps, id)`, `RunOnce(id uint64) error`

- [ ] 用内存任务表测试非法引用、单一进行中的任务、超时、只在 queued/running 时结束任务。
- [ ] 用假的 Git 和 Docker 测试发布、回滚、重建失败仍保留镜像 ID，以及不拉取 `refs/heads`。

### Task 2: admin 接口和总览

**Files:**
- Create: `internal/admin/releaseapi/http.go`
- Modify: `internal/admin/status/overview.go`
- Modify: `internal/admin/status/http.go`
- Modify: `cmd/admin/main.go`
- Test: `internal/admin/releaseapi/http_test.go`

**Interfaces:**
- Consumes: `release.Store`
- Produces: `POST /api/releases`、`POST /api/releases/rollback`、`POST /api/releases/{id}/recreate`、`GET /api/releases`、`GET /api/releases/{id}`

- [ ] 假 builder 覆盖规格里的 400、401、409、502 和成功记录填进 `last_release`。

### Task 3: builder 启动一次性容器

**Files:**
- Modify: `internal/builder/http.go`
- Create: `internal/builder/spawn.go`
- Modify: `cmd/builder/main.go`
- Modify: `Dockerfile`
- Modify: `deploy/docker-compose.yml`
- Test: `internal/builder/http_test.go`
- Test: `internal/admin/deploy/ports_test.go`

**Interfaces:**
- Consumes: `ReleaseEnv`
- Produces: `GET /v1/releases/ready`、`POST /v1/releases/{id}/start`、进程参数 `-release <id>`

- [ ] ready 不回显凭证。start 创建名为 `orbikit-release-<id>` 的自动删除容器后立即返回。
- [ ] compose 中 builder 仍不映射端口，且没有一次性容器服务。

### Task 4: 发布页

**Files:**
- Create: `web/admin/src/views/Release.vue`
- Modify: `web/admin/src/router.ts`
- Modify: `web/admin/src/views/Overview.vue`
- Test: `web/admin/src/release.test.ts`
- Modify: `web/admin/src/overview.test.ts`

- [ ] 六个服务、拒绝短 SHA 和斜杠引用、进行中时动作不可用、没有成功镜像时没有可用的回滚。
