# Turnyard

**语言：** 中文 · [English](docs/en/README.md)

Turnyard 是一个可独立使用的编码代理任务执行器。你用 JSON 描述任务、执行环境、验收要求和交付物；Turnyard 在单机沙箱中调用编码代理，保存可恢复的会话，检查确定的候选结果，并返回可查询的交付记录。

编码任务可能涉及多个仓库、多轮对话、人工回复，以及代码之外的文件或文本。Turnyard 将这些步骤放进一个有明确终点的会话，让调用过程和最终产出都能被追踪。它负责单机执行；任务何时运行、交给哪台机器，由上层系统决定。

## 能做什么

| 能力 | 当前方式 |
| --- | --- |
| 代理与模型 | OpenCode、Kimi Code、Claude Code、Codex CLI；运行时和模型分别配置，具体组合须按运行时能力验证 |
| 会话与恢复 | 同一代理的后续任务沿用原生会话；支持人工回复、检查点恢复和显式结束 |
| 沙箱 | Apple `container`、Docker、Podman 后端；Linux Docker 可选择 gVisor `runsc` |
| 代码与交付 | 多个可选 Git 仓库、任务 feature 分支和 commit；文件、图片、文本也可交付到本地或配置的对象存储连接器 |
| 工具与委派 | Skill、原生 MCP 服务、普通可执行工具经单一 MCP 网关发现和调用；主代理可委派给受管子代理 |
| 验收与记录 | 按任务声明运行检查、核对交付物，并保存候选版本、状态事件和调用日志 |

沙箱后端的安全能力并不相同。Docker 的 `model-only` 模式可限制代理直接出站，并将模型与工具凭据留在独立网关；其他模式的边界见[架构与扩展](docs/zh/architecture.md)和[验证记录](docs/zh/validation.md)。`verified` 只表示配置的检查和交付物校验通过，仍需人工审阅需求是否真正完成。

## 快速开始

下面以 Docker、OpenCode 和 Ollama Cloud 处理一个**已有且工作区干净的 Go Git 仓库**为例。需要 Go 1.27.1、Docker、Git 和相应的模型 API key。先在 Turnyard 仓库根目录构建程序与代理镜像：

```sh
go build -o bin/turnyard ./cmd/turnyard
docker build -f Containerfile -t turnyard-agent:dev .
export OLLAMA_API_KEY='你的 API key'
bin/turnyard doctor
```

生成可编辑的 Session、Environment、Work 输入。输出目录必须位于目标仓库之外，且不能已有同名 JSON 文件：

```sh
go run ./examples/local-go \
  --repo /absolute/path/to/your/go-repo \
  --out /tmp/turnyard-first-task \
  --objective '为服务增加健康检查，并补充测试' \
  --commit-message 'feat: add health check'
```

检查生成的三份 JSON，尤其是模型、检查命令和期望产出；如需指定必需文件，可给生成器添加 `--deliverable`，或编辑 `work.json`。运行时将命令响应中的 `sessionId` 和 `taskId` 赋给下面的变量：

```sh
bin/turnyard session create --file /tmp/turnyard-first-task/session.json
SESSION_ID='填入返回的 sessionId'
bin/turnyard task add "$SESSION_ID" --file /tmp/turnyard-first-task/work.json
TASK_ID='填入返回的 taskId'
bin/turnyard task run "$TASK_ID"
bin/turnyard task wait "$TASK_ID"
bin/turnyard task show "$TASK_ID"
bin/turnyard session complete "$SESSION_ID"
```

`task run` 提交任务后立即返回；常驻 supervisor 在后台执行。完成前可继续向同一会话追加任务。代码默认只保存在本机的任务 feature 分支；远端发布需要显式配置并在会话完成后执行。Turnyard 目前不受管创建 PR，也不合并分支。完整命令、人工回复和失败处理见[使用指南](docs/zh/README.md)。

## 输入与结果

| 输入 | 用途 |
| --- | --- |
| [Session](internal/contracts/schemas/session.json) | 需求会话、固定版本的可选仓库和主代理 |
| [Environment](internal/contracts/schemas/environment.json) | 沙箱、代理与模型绑定、工具、检查及交付连接器 |
| [Work](internal/contracts/schemas/work.json) | 本次任务目标、附件、可写范围、验收条件和必需交付物 |

结果包含任务状态、调用记录、候选版本、检查和交付物清单。遇到 `needs_input` 可以回复并续接；遇到 `unknown` 应先检查可能的外部副作用，再显式核对和重试。输入和输出的生命周期见[生命周期、交付与存储](docs/zh/lifecycle.md)。

## 文档

| 中文 | English |
| --- | --- |
| [使用指南](docs/zh/README.md) | [User guide](docs/en/README.md) |
| [Go 仓库输入示例](docs/zh/local-go.md) | [Go repository input example](docs/en/local-go.md) |
| [架构与扩展](docs/zh/architecture.md) | [Architecture and extensions](docs/en/architecture.md) |
| [代理能力与工具注入](docs/zh/tools.md) | [Agent capabilities and tool injection](docs/en/tools.md) |
| [主代理与委派边界](docs/zh/delegation.md) | [Primary agent and delegation boundary](docs/en/delegation.md) |
| [验证记录与限制](docs/zh/validation.md) | [Validation and limits](docs/en/validation.md) |
| [任务验收边界](docs/zh/acceptance.md) | [Task acceptance](docs/en/acceptance.md) |
| [错误与可观测性](docs/zh/observability.md) | [Errors and observability](docs/en/observability.md) |
| [生命周期、交付与存储](docs/zh/lifecycle.md) | [Lifecycle, delivery, and storage](docs/en/lifecycle.md) |
| [Git 工作流](docs/zh/git.md) | [Git workflow](docs/en/git.md) |
| [状态升级与保留方案](docs/zh/state-evolution.md) | [State migration and retention proposal](docs/en/state-evolution.md) |
| [发布与复验](docs/zh/release.md) | [Release and validation](docs/en/release.md) |

## 开发与许可证

修改 Go 代码后运行 `go test -race ./...`、`go vet ./...`；修改 Ent schema 后运行 `go generate ./internal/store` 并检查生成差异。真实代理和 OCI 验收需要另行配置，范围见[验证记录](docs/zh/validation.md)。Turnyard 使用 [Apache License 2.0](LICENSE)。
