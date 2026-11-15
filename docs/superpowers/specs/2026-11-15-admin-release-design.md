# 管理后台的镜像发布

状态：流程、服务范围、任务结构和测试已与使用者确认。待审阅本规格。确认前不写实现代码。

本规格只覆盖发布页的镜像构建和容器替换。配置页已经通过 Nacos 发布配置正文，这条链路不读取、不修改 Nacos。

## 目标

平台操作员在管理后台选择一个平台服务和 Git 引用，由本机 Docker 构建镜像并替换该服务的一个容器。替换 admin 或 builder 时，页面靠任务记录看到结果，不依赖被替换的进程把这次 HTTP 响应送完。

## 已确认的决定

- 可发布的服务是 `core`、`chatroom`、`gateway`、`webhook`、`admin`、`builder`。六个服务走同一条路径。
- 发布请求只创建任务并返回任务编号。构建和换容器由一个独立的一次性容器完成。页面轮询任务。
- 整个平台同一时间只有一个排队或进行中的任务。
- Git 仓库固定为 `https://github.com/EdwinChanYi/orbikit.git`。引用只能是 tag，或完整的 40 位 commit SHA。
- 镜像留在本机 Docker，不推送镜像仓库。只有一个副本，替换期间该服务中断几秒。
- 回滚使用该服务上一次成功发布留下的镜像 ID，不重新构建。
- 凭证放在 builder 的环境变量里。浏览器和仓库都不保存凭证。
- 总览仍只列出 `core`、`chatroom`、`gateway`、`webhook`。`admin` 和 `builder` 的发布记录只在发布页查看。

## 不做什么

- 不发布使用者自己的业务仓库，也不接受页面上传的 Git 地址。
- 不部署镜像仓库，不做多副本，不写 Kubernetes。
- 不实现日志跟随和 Elasticsearch。
- 不编辑本地 YAML，不改 Nacos 中的配置正文。
- 不把一次性容器写进 `deploy/docker-compose.yml`。
- 不把 `admin`、`builder` 加进现有容器查询白名单，总览不因此多出两行。

## 引用

SHA 必须匹配 `^[0-9a-fA-F]{40}$`。只含十六进制且长度不是 40 的字符串拒绝，错误是「引用无效」。

tag 必须匹配 `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`。含斜杠、空格或以 `-` 开头的字符串拒绝，错误同样是「引用无效」。因此 `feature/login` 和 `abc1234` 都被拒绝。

一次性容器只拉取 `refs/tags/<tag>` 或这一枚完整 SHA，不拉取分支。名为 `main` 的字符串可以通过 tag 格式检查；远程没有这个 tag 时，任务失败，容器保持原镜像。

## 组件

### admin

admin 持有任务表，并提供下面的会话接口。它不挂载 Docker 套接字。

创建任务前，admin 先把超过 30 分钟仍处于排队或进行中的任务改为失败，原因是「超时」。然后请求 builder 的 `GET /v1/releases/ready`。builder 不可达时返回 `502 {"error":"builder 不可达"}`，不创建任务。凭证未配置时返回 `400 {"error":"缺少 Git 凭证"}`，不创建任务。已有排队或进行中的任务时返回 `409 {"error":"已有发布在进行"}`，不创建任务。

通过检查后，admin 在一个事务里插入任务，再调用 builder 的 `POST /v1/releases/{id}/start`。启动调用失败时，把这条任务改为失败，原因是「builder 不可达」，并返回 `502 {"error":"builder 不可达","id":<编号>}`。启动调用成功时返回 `202 {"id":<编号>}`。

### builder

builder 增加两个接口，仍不映射宿主机端口：

- `GET /v1/releases/ready` 返回 `200 {"configured":true}` 或 `200 {"configured":false}`。响应不含凭证。两个环境变量 `ORBIKIT_GIT_USERNAME` 和 `ORBIKIT_GIT_TOKEN` 都非空时，`configured` 为 true。
- `POST /v1/releases/{id}/start` 校验 `id` 是正整数，然后启动一次性容器，立即返回 `204`。它不等待构建结束。

builder 把 admin 库的 DSN、两个 Git 环境变量、compose 项目名和 Docker 套接字传给一次性容器。builder 自己不克隆仓库，也不构建业务镜像。

现有 `GET /v1/containers/{name}` 的白名单仍是 `core`、`chatroom`、`gateway`、`webhook`。

### 一次性容器

一次性容器使用 builder 镜像的发布模式，容器名是 `orbikit-release-<任务编号>`。它安装 Git，挂载 Docker 套接字，结束后由 Docker 删除。它不是 compose 服务。

它从 `orbikit_admin` 读取自己的任务。服务名必须再次落在六个名字里，否则把任务标为失败，原因是「服务名不在允许范围内」。服务名只作为参数传递，不拼进 shell 命令。

发布任务的步骤：

1. 把任务改为进行中。
2. 拉取指定 tag 或完整 SHA。
3. 在拉取下来的目录中执行仓库 Dockerfile，构建参数 `SERVICE` 是该服务名。环境变量 `GOPROXY` 有值时，把它传给构建；没有时使用 `https://proxy.golang.org,direct`。
4. 镜像标签为 `orbikit-<服务>:<引用的小写形式>`。Docker 标签只使用小写，任务里的 `git_ref` 保留操作员输入的原文。记录镜像 ID。
5. 查看当前容器 `<项目名>-<服务>-1`。用同一套网络、环境变量、挂载、端口绑定、重启策略、用户和命令创建新容器，只把镜像换成新的镜像 ID。新容器先使用临时名字。
6. 停掉并删除旧容器，再把新容器改成原来的名字。
7. 把任务改为成功。

任一步失败时，任务改为失败，并把命令输出写入任务。第 5 步或第 6 步失败时，已经得到的镜像 ID 保留在任务上。第 6 步开始前失败时，旧容器继续运行。第 6 步中途失败时，`error` 是「容器替换未完成」，输出里写明临时容器名。

一次性容器只把仍处于 `queued` 或 `running` 的任务改为成功或失败。任务已被标为超时后，它不再把状态改回成功。

回滚和「用该镜像重建」跳过拉取和构建，从任务上已经写好的镜像 ID 执行第 5 步和第 6 步。本机没有这个镜像时，任务失败，原因是「镜像不在本机」。

输出超过 256KB 时，只保留末尾 256KB。

## 数据

任务表 `releases` 在 `orbikit_admin`，由 admin 启动时迁移。一行是一次发布、回滚或重建。

| 列 | 含义 |
| --- | --- |
| id | 自增主键 |
| service | 六个服务名之一 |
| kind | `publish`、`rollback` 或 `recreate` |
| git_ref | 发布时的 tag 或 SHA。回滚为空。重建沿用来源任务的引用 |
| image_id | 构建或指定的镜像 ID。还没有镜像时为空 |
| status | `queued`、`running`、`succeeded`、`failed` |
| log | 末尾最多 256KB 的输出 |
| error | 给页面的失败原因。成功时为空 |
| operator | 固定为 `admin` |
| created_at | 创建时间 |
| finished_at | 进入成功或失败的时间。未结束时为空 |

占住名额的条件是：状态属于 `queued` 或 `running` 的行数最多为一。admin 在事务中检查这个条件。

「上一次成功镜像」指该服务 `status=succeeded` 且 `image_id` 非空的最新一行，三种 `kind` 都算。

## 接口

全部要求登录会话。未登录返回 `401 {"error":"登录失败"}`。

| 方法 | 路径 | 行为 |
| --- | --- | --- |
| POST | `/api/releases` | 正文 `{"service":"core","ref":"v1.2.3"}`。创建 `kind=publish` 的任务 |
| POST | `/api/releases/rollback` | 正文 `{"service":"core"}`。没有上一次成功镜像时返回 `400 {"error":"没有可回滚的镜像"}`，不创建任务。否则创建 `kind=rollback`，并在启动前把上一次成功的 `image_id` 写入新任务 |
| POST | `/api/releases/{id}/recreate` | 来源任务必须是 `failed` 且 `image_id` 非空，否则 `400 {"error":"没有可重建的镜像"}`。创建 `kind=recreate`，复制来源的服务名、引用和镜像 ID |
| GET | `/api/releases?service=core` | 返回该服务的任务，按 `id` 降序，最多 20 条 |
| GET | `/api/releases/{id}` | 返回这一条。编号不存在时 `404 {"error":"发布记录不存在"}` |

列表和详情在返回前执行 30 分钟超时处理。

任务 JSON 的字段是 `id`、`service`、`kind`、`git_ref`、`image_id`、`status`、`log`、`error`、`operator`、`created_at`、`finished_at`。时间使用 RFC3339。

总览里四个业务服务的 `last_release`：没有成功任务时仍是 JSON `null`，页面显示「尚无发布」。有成功任务时是 `{"ref":"v1.2.3","finished_at":"2026-11-15T15:10:00+08:00"}`。回滚成功时 `ref` 为空字符串，页面显示 `sha256:` 之后的 12 位十六进制和结束时间。

## 页面

发布页用服务按钮列出六个服务。选中后显示该服务最近的任务：状态、引用、镜像 ID、结束时间和等宽输出。

引用输入框和「发布」按钮提交 `POST /api/releases`。选中服务存在上一次成功镜像时，「回滚」可用。失败任务带有镜像 ID 时，该行显示「用该镜像重建」。

选中服务或全局存在排队、进行中的任务时，三个动作都不可用。页面每 2 秒请求一次当前任务，直到状态变为成功或失败。请求被拒绝时，在页面上显示接口返回的 `error`。

## 错误

- 服务名不在六个名字中：`400 {"error":"服务名不在允许范围内"}`。
- 引用无效：`400 {"error":"引用无效"}`。
- 缺少凭证、已有进行中的任务、没有可回滚或可重建的镜像：使用上面写明的状态码和错误文本，不创建新的进行中任务。
- 拉取失败或构建失败：任务为 `failed`，旧容器继续运行，页面显示输出。
- 构建成功但重建失败：任务为 `failed`，`image_id` 保留。
- 本机缺少回滚镜像：任务为 `failed`，`error` 是「镜像不在本机」。
- builder 在创建任务前不可达：不创建任务。任务已创建而启动失败：任务为 `failed`，`error` 是「builder 不可达」。

## 测试

- admin 使用假的 builder 和内存任务表。覆盖非法服务名、非法引用、并发任务被拒绝、凭证缺失被拒绝、启动失败后记录变为失败、回滚在没有成功镜像时被拒绝、重建只接受带镜像 ID 的失败任务，以及成功记录填进总览的 `last_release`。
- 一次性容器使用假的 Git 和假的 Docker。覆盖按 tag 和完整 SHA 构建、不拉取分支、复制原容器的网络与挂载后只换镜像、重建失败时保留镜像 ID、镜像不在本机时回滚失败。测试不连接本机 Docker。
- 发布白名单接受六个服务名，拒绝 `core;touch`、`../core` 和空字符串。现有容器查询白名单仍拒绝 `admin` 和 `builder`。
- 前端测试六个服务按钮、拒绝短 SHA 和带斜杠的引用、进行中时三个动作不可用、没有成功镜像时不显示可用的回滚。
- compose 检查保持 builder 不映射宿主机端口、只有 builder 挂载 Docker 套接字，并且 compose 服务列表中没有一次性容器。
- 浏览器验收走登录和发布页：能看到六个服务，非法引用被拒绝，任务状态和输出能显示。完整镜像构建不作为这次的自动验收。
