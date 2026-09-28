# 使用指南

[项目首页](../../README.md) · [English](../en/README.md)

Turnyard 通过 `Session` 指定仓库和主代理，通过 `Environment` 指定沙箱、运行时、模型、工具及检查，再将每个 `Work` 任务追加到会话。三个输入都是 JSON，分别由 [session.json](../../internal/contracts/schemas/session.json)、[environment.json](../../internal/contracts/schemas/environment.json) 和 [work.json](../../internal/contracts/schemas/work.json) 校验。

## 构建与环境

```sh
go build -o bin/turnyard ./cmd/turnyard
container system start --enable-kernel-install
container build -f Containerfile -t turnyard-agent:dev .
export OLLAMA_API_KEY='你的 API key'
bin/turnyard doctor
```

也可在 `Environment.sandbox.backend` 中选择 `docker`。Docker 需要先构建或导入同名镜像。对需要限制代理出站访问的任务，设置 `Environment.sandbox.network` 为 `model-only`；该模式目前仅支持 Docker，详情见[能力边界](delegation.md)。Podman 已在嵌套 Linux 的 rootful 环境跑通真实单任务链路，原生 rootless 与 macOS 虚拟机仍待验证。验收测试和 MCP 示例均使用 Go；代理镜像保留 Python 作为可选的通用开发工具。

在 Linux 上建议以专用非 root 账户运行 Turnyard，并让该账户可访问所选容器运行时。Docker/Podman 代理容器使用 Turnyard 账户的 UID/GID，以读写会话私有目录；不要让不受信任的用户访问容器运行时的管理接口。

Linux Docker 可在可信的 Environment 中设置 `"sandbox":{"backend":"docker","image":"turnyard-agent:dev","isolation":"gvisor","network":"model-only"}`。执行机须先按[gVisor 官方指南](https://gvisor.dev/docs/user_guide/quick_start/docker/)向 Docker 注册 `runsc`；Turnyard 会在创建会话时检查，缺失时拒绝创建。代理、检查和原生会话查询使用 `runsc`，模型网关仍是宿主侧受信任服务容器。已在 Docker-in-Docker 的 Linux/arm64 环境中跑通 gVisor 沙箱探针与 OpenCode + 云端模型任务；早期嵌套任务使用 `--ignore-cgroups`；后续标准 cgroup 探针和完整任务已通过，见[验证记录](validation.md)。

## 输入

`Session.repositories` 逐个给出本机仓库根目录或远端仓库 URL，以及固定的 40 位 commit SHA；`primaryAgent` 选择该会话的代理。`idempotencyKey` 标识一次会话创建：超时或响应丢失后，用相同文件重试会返回同一会话；同一键对应不同输入会报冲突。要创建独立的新会话，应使用新键。会话一旦创建，任务会继续使用同一个原生运行时会话，不跨运行时迁移。远端配置见 [Git 工作流](git.md)。

`Environment.agents` 将代理 ID 绑定到运行时和 `modelBinding`。目前验证过的组合：OpenCode + Ollama Cloud/DeepSeek、Kimi Code + Ollama Cloud、Claude Code + DeepSeek、Codex CLI + DeepSeek。其中 OpenCode + DeepSeek 仅通过单任务工具调用链路，尚未完成多任务验证。内置提供方的端点和凭据要求集中管理；其他兼容提供方可显式配置 HTTPS `endpoints`。OpenCode + OpenAI 和 Kimi + DeepSeek/OpenAI 尚未完成真实链路验证。Skill、原生 MCP 服务和普通可执行工具由环境声明，再由代理 ID 选择；工具通过一个 MCP 网关按需发现，来源目录会锁定摘要，后续改变会触发环境漂移错误。见[代理能力与工具注入](tools.md)。

`Work.scope.repositories` 按仓库设置 `read` 或 `write`；不修改代码的文档任务可使用空仓库列表。`inputs` 可附带本机文件、允许域名的 HTTPS 文件或连接器文件，任务接受前会固定实际字节。`deliverables` 声明必需产出物；文件和图片可从 Git 候选版本交付，也可从专用产出目录交付到本地或配置的对象存储。`acceptance` 写明验收目标；`checks` 引用环境中预先定义的命令。`commitMessage` 可指定 Git 提交标题；未填写时从任务目标提取短标题，任务 ID 放在提交正文中。完整输入、交付配置和结果说明见[生命周期、交付与存储](lifecycle.md)。

如已有本机 Go 仓库，可用 [local-go 示例](local-go.md)根据当前 HEAD 生成三份有效 JSON，再修改目标、检查和交付物要求。

## 会话与任务

```sh
bin/turnyard session create --file session.json
bin/turnyard task add <session-id> --file work.json
bin/turnyard task run <task-id>
bin/turnyard task wait <task-id>
bin/turnyard task show <task-id>
bin/turnyard session complete <session-id>
bin/turnyard session publish <session-id>
bin/turnyard session cancel <session-id> --reason "需求已撤销"
bin/turnyard session events <session-id>
```

上述在线会话与任务命令发现本机 supervisor 尚未运行时会自动启动它。需要显式管理进程时，可用 `bin/turnyard daemon start`、`bin/turnyard daemon status`、`bin/turnyard daemon stop`。`task run` 接受任务后即返回，supervisor 在后台继续执行；`task wait` 轮询至任务停止。有活动任务时 `daemon stop` 会拒绝关闭。目前没有另一个单次执行模式，也没有随机器重启自动拉起 supervisor 的内置服务管理器。

`session create` 默认允许 10 分钟准备仓库，可用 `--timeout <秒>` 调整，最大为 7200 秒。超时后用相同 `idempotencyKey` 重试，先查询已有会话，再决定是否重新准备。

任务需要人工决定时会进入 `needs_input`，用 `task reply <task-id> --text "决定"` 继续原任务。检查失败后可用 `task verify <task-id>` 针对同一候选代码重跑检查，或用 `task retry <task-id>` 发起新代理调用。`task reconcile <task-id>` 为失败任务保存现有工作区检查点；对 `unknown` 任务，操作员应先检查工作区、调用日志和外部工具副作用，该命令确认相关容器已经停止后，记录审计事件并将任务转为可重试的 `failed`。`checkpoint restore <checkpoint-id>` 要求工作区和代理状态目录都不存在，避免覆盖现有状态。

同一需求可依次追加任务；只有所有任务的候选版本均通过检查和交付物校验，才可执行 `session complete`。中途撤销需求时，可在没有活动调用的情况下用 `session cancel` 结束。终态不能再追加任务、调用代理、复验或恢复检查点。`session complete` 返回每个任务的候选摘要与交付物清单。详见[生命周期、交付与存储](lifecycle.md)。

默认状态目录是 `~/.local/share/turnyard`，可通过 `TURNYARD_HOME` 或命令开头的 `--home <目录>` 调整。`daemon.log` 保存结构化运行日志；会话事件保存在 SQLite；每次代理与检查的原始输出记录在私有日志文件中。任务文本和 API key 不写入 `daemon.log`。状态目录包含任务和代理会话数据，应限制访问并备份。

## 结果含义

`verified` 表示该 Candidate 的当前代码版本通过配置的检查及已声明交付物校验，仍需人工审阅是否满足需求。`failed` 表示检查或已分类执行错误；`unknown` 表示超时、进程中断或无法确认副作用，需要先检查工作区和事件，再显式执行 `task reconcile`，不会自动重试。默认只在任务 feature 分支提交；如显式配置远端写入，完成会话后可发布分支。详见 [Git 工作流](git.md)、[架构](architecture.md)与[验证范围](validation.md)。

需要让测试独立于代理可写代码时，见[任务验收边界](acceptance.md)。
