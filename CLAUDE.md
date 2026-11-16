# orbikit

编程规范在仓库根目录 `AGENTS.md`。写 Go 之前先读它。不要在这里再写一套规范。

技能由 Claude Code 从 `.claude/skills/` 加载。Codex、Cursor 以及其他扫描 `.agents/skills/` 的 agent 读另一份，两处正文逐字相同，改一处必须同时改另一处。

- `/extend-orbikit-service`：新增一个可独立部署的服务进程
- `/add-orbikit-service-feature`：在已有服务里加 RPC、命令或字段
