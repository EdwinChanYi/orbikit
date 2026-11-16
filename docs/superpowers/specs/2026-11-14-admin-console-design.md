# orbikit 统一管理后台

状态：已与使用者确认，待审阅。确认前不写实现代码。

## 目标

提供一个 orbikit 自己的管理后台，给平台操作员使用。浏览器只打开这一个后台，以及网关给客户端用的端口。配置、etcd、服务状态、日志、构建和版本替换都在这个后台里完成。

初期部署是一台机器上的 docker compose，面向 8GB 内存。Nacos 和 Elasticsearch 默认关闭。Kubernetes 不在这一期实现，后台把部署操作收口到一个接口，这一期只提供 compose 实现。

## 已确认的决定

- 前端是 Vue 3，组件库用 Arco Design Vue。
- 页面是运维工作台：左侧为总览、配置、集群、日志、发布。控件保持紧凑。状态只用绿色正常、黄色降级、红色故障、灰色未知。品牌色不表示健康。日志用等宽字体。提供深色主题。
- 初期使用现有 `deploy/docker-compose.yml` 的单机模型。镜像留在本机 Docker，不部署镜像仓库。
- 构建和替换只覆盖平台服务：`core`、`chatroom`、`gateway`、`webhook`。以后加进同一仓库的平台服务也走这里。使用者自己的业务服务不能在这里构建。
- Nacos、Elasticsearch 各自有开关，默认关闭。打开其中任意一个时，按 16GB 内存准备机器。
- 登录账号是平台操作员。不使用业务 appId，也不把 Nacos 的登录交给浏览器。
- 本机开发继续使用 `make up`。管理后台是 compose 里的新服务，不替换现有开发流程。

## 不做什么

- 不安装 Kubernetes，不实现滚动更新的多副本调度。
- 不嵌入 Nacos 控制台、Grafana 或 Jenkins 的页面。
- 不提供 etcd 任意键值的编辑器。
- 不在管理后台里构建使用者登记的外部 Git 仓库。
- 不把应用密钥、抄送地址从 MySQL 迁走。那些仍由 core 管理。
- 不承诺监听端口、MySQL 地址、Kafka 地址可以热更新。这些变更通过发布并重启生效。

## 网络边界

浏览器可以访问：

- 管理后台，宿主机端口 `8091`。这一期是 HTTP，供本机或内网使用。cookie 不标记 Secure。放到公网前要在前面加 TLS 代理。
- 网关 TCP `8000` 和 WebSocket `8001`。这是客户端入口。

浏览器不能访问，compose 不再把这些端口映射到宿主机：

- `core` 的 `9001`、`chatroom` 的 `9002`、网关的 HTTP `8080`。
- etcd、MySQL、Redis、Kafka、webhook。它们现在就没有宿主机映射，保持这样。
- 打开 Nacos 或 Elasticsearch 时，它们的端口同样只在 compose 网络内。

管理进程不挂载 Docker 套接字。compose 里增加内部服务 `builder`，只有它挂载套接字，并且不映射宿主机端口。后台通过 compose 网络调用 builder。

## 组件

### admin

一个 Go 进程，提供 HTTP API，并托管构建好的 Vue 页面。它负责登录、配置历史、聚合状态，以及把构建、重启、读日志转给 builder。

配置历史存在 MySQL 的独立库 `orbikit_admin`，不写入业务库 `orbikit`。表保存每次发布的服务名、YAML 全文、镜像 ID、操作者和时间。

操作员密码来自环境变量 `ADMIN_PASSWORD`，首次启动写入密码哈希。会话使用 HttpOnly cookie。密码和 Git 凭证不进仓库。

### builder

内部 HTTP 服务。它执行本机 Docker 和 compose 命令，范围只允许平台服务名。它提供：构建镜像、按镜像 ID 重新创建容器、读取容器当前日志、查询容器是否在运行。

命令失败时返回退出码和输出，不重试有副作用的构建。admin 把这段输出展示在发布页。

### 部署接口

admin 内部用一组操作表示「容器状态、构建、替换、日志跟随」。这一期的实现调用 builder。以后的 Kubernetes 实现替换这一组操作，不改页面协议。这一期不写 Kubernetes 代码。

### 现有服务

`core`、`chatroom`、`gateway`、`webhook` 保持现有进程模型。管理后台不把业务逻辑放进这些进程。

## 配置

本地 YAML 始终保留，并继续负责引导信息：监听地址、MySQL、以及 Nacos 开关和地址。`pkg/config` 在 Nacos 关闭时行为与现在相同。

Nacos 关闭时：

- 配置页编辑 compose 挂进容器的那份 YAML，路径是 `deploy/configs/<服务>.yaml`。
- 发布前展示与当前文件的 diff。
- 发布把全文写入历史，再写入文件，然后由 builder 重新创建该服务容器。
- 回滚从历史中取出一份 YAML，按同一次发布流程写回并重启。

Nacos 打开时：

- 服务启动时用本地引导配置连接 Nacos，再用 Group `orbikit`、DataId `<服务>.yaml` 的内容覆盖同一结构体。
- 连不上 Nacos 时，进程用本地 YAML 启动，并在日志里写明使用了本地配置。
- 配置页改为调用 Nacos Open API。历史、回滚、按 IP 灰度和监听者使用 Nacos 的能力。此时 `orbikit_admin` 不再作为配置正文的来源。
- 管理后台保存 Nacos 的服务端账号，浏览器不接触。
- 日志级别、缓存时间和审核开关由服务监听变更后生效。监听端口、MySQL、Kafka 的变更仍要重新创建容器。

## 状态

总览列出每个平台服务的容器是否在运行、gRPC health、etcd 中的注册实例、当前镜像 ID、最近一次发布。

集群页展示 etcd 的健康、主节点、库大小和告警，以及 `/orbikit/services/` 下的实例和地址。etcd 不可达时，这两页显示故障，不把空列表显示成正常。

## 日志

Elasticsearch 关闭时，日志页按服务读取该容器的当前输出，支持跟随和暂停。不提供关键词检索，也不保存历史。

Elasticsearch 打开时，compose 增加 Elasticsearch 和 Fluent Bit。Fluent Bit 只采集平台服务的标准输出。日志页按服务、时间和关键词查询。索引保留 7 天，到期删除。Elasticsearch 不映射宿主机端口。

## 发布

发布页选择平台服务和 Git 引用。引用只能是 tag 或完整 commit SHA。builder 在本机构建镜像，记录镜像 ID，再重新创建该容器。构建日志显示在同一页。

回滚使用历史里的上一个镜像 ID 重新创建容器，不重新构建。只有一个副本，替换期间该服务中断几秒。

Git 地址固定为 orbikit 仓库。拉取凭证放在 builder 的环境变量里。没有凭证时，发布页拒绝构建并说明原因。

## 页面

五个区域共用左侧导航和顶栏。顶栏显示当前是「本地配置」还是「Nacos」，以及日志是「容器输出」还是「Elasticsearch」。

- 总览：顶部一条 etcd 和配置来源的状态，下面是服务表。
- 配置：左侧服务列表，右侧 YAML。未保存时离开页面需要确认。发布前看 diff。
- 集群：etcd 状态和实例表。
- 日志：一行筛选，下面是等宽输出。
- 发布：选择服务和版本，查看构建输出，执行替换或回滚到上一镜像。

## 错误处理

- builder 不可达：发布、重启和容器日志显示故障，配置历史仍可查看。
- 写入 YAML 成功但重启失败：历史保留这次内容，页面显示容器未更新，允许再次重启或回滚。
- etcd 查询失败：状态显示未知或故障，不用旧数据冒充当前健康。
- 登录失败不区分「用户不存在」和「密码错误」。

## 测试

- admin 的配置历史、diff 和回滚用临时目录和假的 builder，不连接真实 Docker。
- builder 对服务名做白名单测试，拒绝 `core` 以外的任意字符串进入命令。
- Nacos 关闭时，`pkg/config` 的现有测试保持通过。Nacos 打开时的覆盖用假的 HTTP 服务测试，不启动 Nacos。
- 前端用组件测试覆盖未保存离开和服务名白名单。浏览器里走通登录、查看状态、发布前 diff。
- compose 文件检查：`core`、`chatroom` 没有宿主机端口；Nacos 和 Elasticsearch 默认不出现在启动的服务里。

## 实现顺序

设计按一块产品确认。实现时按下面的顺序交付，每一步都能在一台 8GB 的机器上运行：

1. admin 登录、页面壳、compose 服务和端口收口。
2. builder，以及总览、集群页所需的状态。
3. 本地 YAML 的编辑、历史、diff、发布重启和回滚。
4. 容器日志跟随。
5. 本机构建和按镜像 ID 替换。
6. Nacos 开关和 Elasticsearch 开关。默认仍然关闭。
