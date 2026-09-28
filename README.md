# Turnyard

**语言：** 中文 · [English](docs/en/README.md)

Turnyard 是一个可独立使用的编码代理任务执行器。它接收结构化 JSON 任务和图片、文档等附件，在隔离的容器中运行代理，核对结果并按任务指定的位置交付文件或文本。代码任务支持多个 Git 仓库，并将修改保存为任务专属 feature 分支和 commit；文档等文件也可不经过 Git，交付到本地或配置的对象存储连接器。

Turnyard 负责单机上的受控执行和标准化结果；任务何时运行、交给哪台机器，由上层调度系统决定。Supervisor 可以常驻并处理多个会话，每次代理调用只临时启动计算容器。执行机可设置并发任务和资源预算，并查看状态目录用量告警。

编码任务常常跨越多个仓库和多轮对话，也可能在中途等待人工决定。Turnyard 将一个需求作为有限的会话管理：新任务沿用原代理的上下文，人工回复接回原任务；计算容器结束后，工作区与代理状态可以从检查点恢复。代理报告完成后，Turnyard 针对确定的代码版本运行检查并确认任务声明的交付物。全部任务完成并审阅后，显式结束会话，交付记录仍可查询。

目前接入 OpenCode、Kimi Code、Claude Code 和 Codex CLI；运行时与模型绑定分别配置。获准的 MCP 服务和可执行工具经统一网关按需发现，代理只需连接一个 MCP 入口。沙箱后端通过接口接入 Apple `container`、Docker 和 Podman；Linux Docker 可选择 gVisor `runsc` 隔离。Docker 的 `model-only` 模式通过调用专属网关提供云端模型访问，同时限制代理的直接出站网络与模型密钥可见性。已验证的具体组合、能力边界及复现命令见[验证记录](docs/zh/validation.md)。

## 开始使用

需要 Go 1.27.1、一个可用的 OCI 沙箱后端以及相应模型提供方的 API key；代码仓库任务还需要 Git。Apple `container` 与 Docker 已完成真实链路验证。以 Apple `container` 为例：

```sh
go build -o bin/turnyard ./cmd/turnyard
container system start --enable-kernel-install
container build -f Containerfile -t turnyard-agent:dev .
export OLLAMA_API_KEY='你的 API key'
bin/turnyard doctor
TURNYARD_E2E=1 go test -tags integration -run TestMultiRepoWorkflow -v -count=1 -timeout 25m ./test/e2e
```

Go 验收测试会创建独立的 Git 测试仓库，运行多轮任务、人工回复和检查点恢复；它不会写入 GitHub。正式使用时，分别提交 [Session](internal/contracts/schemas/session.json)、[Environment](internal/contracts/schemas/environment.json) 和 [Work](internal/contracts/schemas/work.json) JSON；命令与示例见[使用指南](docs/zh/README.md)。

已有 Go 仓库可用 [local-go 输入生成器](docs/zh/local-go.md)生成三份可编辑 JSON。远端仓库固定版本克隆与审阅后的 feature 分支发布见 [Git 工作流](docs/zh/git.md)。会话结束后，使用[离线备份与清理流程](docs/zh/lifecycle.md)保留或移除运行数据。

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

Turnyard 可在显式启用后发布已验证任务的 feature 分支；产出清单可要求代码文件、图片、文本或 PR，并根据类型核对。附件和非 Git 交付见[生命周期、交付与存储](docs/zh/lifecycle.md)。当前还没有受管的自动 PR 创建流程，也不合并分支。

## 许可证

Turnyard 使用 [Apache License 2.0](LICENSE)。
