# 架构与扩展

[使用指南](README.md) · [English](../en/architecture.md)

## 执行链路

Turnyard 的边界是一台机器及其状态目录。上层系统决定任务顺序与执行机器，通过本机 CLI 提交输入、读取状态和结果；Turnyard 不在多台机器之间分配任务或共享会话状态。一个 supervisor 可持续服务多个会话，每个会话同时只运行一个任务。

1. 输入层校验 JSON Schema、仓库与检查引用、连接器引用、环境资源和运行时绑定。创建会话时锁定镜像及可选的固定仓库版本；追加任务时先将附件下载或复制成带摘要的快照，再保存任务。
2. Supervisor 通过私有 Unix socket 接收命令；SQLite 持久化会话、任务、轮次、调用、候选、检查点和事件。每个会话同一时刻只接受一个代理调用。
3. 通用工具准备层把获准的原生 MCP 服务、普通可执行程序、受管委派和内置的人工输入请求汇集到一个工具网关。网关只向代理暴露发现与调用入口，校验真实目标的授权和参数；普通程序仍由桥接器接收结构化调用。代理驱动把同一个网关、模型绑定、Skill、原生会话 ID 和任务提示转换成各 CLI 的配置与命令。代理会话状态保存在容器外的私有 `/state` 目录。
4. 沙箱后端启动临时 OCI 容器，以只读方式挂载附件快照，并提供本次调用专属产出目录；代码任务还按仓库挂载读写权限，将可写仓库的 `.git` 再挂载为只读。Turnyard 使用 go-git 创建 feature 分支并在代理退出后提交代码。
5. 检查点打包工作区与代理状态并记录 SHA-256；候选摘要绑定任务、每仓库 base/head/tree/branch 和声明产物的字节摘要。非 Git 文件封存于后续代理无法写入的目录。可信检查通过后，外部连接器才上传到内容寻址位置，再下载核对摘要。
6. 所有任务的候选均验证通过、工作区与最后检查点一致时，会话可显式完成。完成清单记录每个任务的候选摘要与交付物结果；终态不再执行或恢复。

## 扩展点

源码按依赖方向组织：

| 目录 | 职责 |
| --- | --- |
| `internal/contracts` | JSON 输入、Schema 与领域输入类型 |
| `internal/lifecycle` | 会话、任务与事件的稳定状态名称 |
| `internal/fault` | 稳定错误码、原因链与错误位置 |
| `internal/agents` | 代理驱动、模型证据、Skill/工具注入与原生状态文件保护 |
| `internal/toolgateway` | MCP 工具目录、检索、逐工具校验和调用路由 |
| `internal/artifacts` | 必需产出清单、调用专属声明、文件/图片/文本检查与可替换 PR 读回 |
| `internal/humaninput` | 调用专属的结构化人工输入请求及校验 |
| `internal/inputs` | 附件下载、快照与运行前摘要复核 |
| `internal/artifactio` | 由可信环境配置的宿主侧文件传输连接器 |
| `internal/sandbox` | OCI 后端、挂载、资源限制与容器清理 |
| `internal/gitstate` | 仓库分支、commit、候选版本与检查点归档 |
| `internal/store` | Ent schema、SQLite 持久化、领域化读写操作与事务；业务层看不到 Ent client 或 SQL 事务 |
| `internal/observe` | 带上下文关联字段的结构化日志 |
| `internal/engine` | 任务状态机、检查、恢复和结果编排 |
| `internal/transport` | Unix socket supervisor 与 CLI |
| `cmd/turnyard` | 可执行程序入口 |
| `cmd/turnyard-tool-gateway` | 代理侧 MCP 入口与 Docker 工具容器服务 |

依赖从命令入口指向执行层，再指向代理、沙箱、Git 和存储；`contracts` 仅提供共享输入与类型，不依赖执行实现。

`AgentDriver` 负责验证模型绑定、启动与解析一个原生代理；四个实现分别位于 `internal/agents/{claude,codex,kimi,opencode}`，由 `internal/agents/registry` 显式组装，不使用初始化时的隐式注册。新增驱动必须明确如何传递凭据、恢复原生 ID、证明实际模型和报告工具调用。人工输入请求由共用工具处理，不依赖各驱动解析自然语言。

内置模型提供方的端点、凭据名与可用 API 协议集中在 `internal/agents/model_provider.go`；各驱动选择自己支持的协议，保留 CLI 专有的配置和模型证据解析。其他提供方可在可信的 `model_binding.endpoints` 中按 `openai_chat`、`anthropic` 或 `responses` 明确给出 HTTPS 地址和凭据变量，无须修改驱动。内置提供方不接受覆盖端点，避免误把其固定凭据发送到另一个地址。配置可被接受不等于该提供方与模型已通过真实链路验收。

环境 JSON Schema 只约束运行时、沙箱、提供方、凭据变量和工具类型的名字格式。会话创建时由 `DriverFactory`、`BackendFactory` 和驱动的能力检查判定这些名字是否真的可用；因此增加新实现不需要把名字写进 Schema 枚举。未安装的实现仍会在创建会话时被拒绝。

`SandboxBackend` 封装探测、配置能力检查、镜像身份、执行和容器清理；OCI 生命周期与挂载策略集中在 `OCIBackend`，Apple `container`、Docker、Podman 各有独立的命令方言，Docker 与 Podman 仅复用兼容的 CLI 操作。Docker/Podman 的代理容器使用 Turnyard 进程的 UID/GID 访问私有挂载；非 root 的 Podman 进程选择 `keep-id` 用户命名空间，rootful Podman 直接使用现有身份映射。Docker 的可选 `isolation: "gvisor"` 在调用代理、检查与原生会话查询时选择 `runsc`，创建会话前检查 Docker daemon 已注册该运行时。gVisor 在 Docker 自定义网络上无法可靠解析网关别名，因此模型网关使用调用专属内网地址写入代理容器的 hosts 映射；不改用宿主网络。新增后端必须保住只读根、仓库权限、Git 元数据保护、资源限制、超时清理和日志脱敏等边界。最终网关已在 Apple `container`、嵌套 Linux 的 Podman 与 gVisor 上通过真实单任务验证；Podman 的 macOS AppleHV 虚拟机仍待复验；gVisor 的标准 cgroup 限额已在嵌套 Linux 环境测量，独立部署主机仍需复验，见[验证记录](validation.md)。

`engine` 不持有 SQL 句柄或事务对象；会话、调用、候选与检查点的原子操作由 `store` 实现。OCI 生命周期与挂载策略位于 `sandbox`，命令方言由 `Dialect` 隔离。代理驱动共用调用准备与主进程判定，每种 CLI 保留自己的配置、原生续接和证据解析。错误及关联 ID 的处理见[错误与可观测性](observability.md)。

执行器对创建会话、追加任务、运行任务、复验、恢复与完成等操作返回显式结果类型；持久层将 Ent 实体转换为领域读模型，再由传输层序列化为 JSON。任务执行依次准备 Git 分支、调用代理、固化候选和运行检查；沙箱把挂载与凭据参数组装收在后端内部。

Turnyard 自有的 Session、Environment、Work 输入、RPC 结果、委派消息、产物声明及日志字段统一使用 `snake_case`。当前仍为未正式发布的 v1 契约；先前以驼峰字段生成的测试输入或状态数据需要重新生成。代理原生事件、MCP、Docker、GitHub 及 JSON Schema 标准字段遵循各自的外部协议，不改写为 Turnyard 命名。

存储模型由 `internal/store/ent/schema` 定义，`go generate ./internal/store` 再生成为 `internal/store/ent`。`store` 把生成实体转换成稳定的领域结果，并拥有事务边界。打开已有工作区时会把旧的 `task_repositories` 基准数据一次性导入 `task_bases`，重复打开不会重复写入。OpenCode 驱动读取的是该 CLI 自己的原生 SQLite 会话文件，不属于 Turnyard 的业务数据库，按其原生格式只读校验模型证据。

模型绑定与代理 ID 分离，但**不是任意运行时与任意模型都可组合**。每个驱动负责验证自己的提供方兼容性；当前通过真实链路的组合见[验证记录](validation.md)。

`Session.primary_agent` 指定主代理；环境内的其他代理不会自动启动。主代理通过 `agents[].delegates` 获得受管委派工具，子任务使用独立会话、工作区和候选结果；运行时原生子代理仍由运行时自行管理。委派的状态、隔离与产出约束见[主代理与委派边界](delegation.md)。

## 恢复与审阅边界

任务等待人工回复时，关闭的是计算容器，原生会话和 Git 工作区仍持久化。检查点用于在工作区被移走后恢复磁盘状态；它不是内存快照，也不迁移订阅账号登录状态。API key 从宿主环境按调用读取，值不写入环境 JSON。Docker `model-only` 模式把真实模型凭据留在模型网关容器，工具凭据留在独立工具容器；代理容器只有模型占位凭据和调用专属内部网络。Docker 常规网络模式也把工具凭据留在工具容器，但代理仍有一般出站连接与自身的模型凭据。其他后端在同容器运行工具网关，不能据此隔离工具密钥。

`verified` 表示配置的机器检查及必需产出物均通过对应核对；它不是对需求正确性或安全性的完整证明。检查失败可复验同一 Candidate。超时或 supervisor 中断后的 `unknown` 需人工核查，因为外部 MCP 可能产生无法自动判定的副作用；显式 `task reconcile` 会先确认相关容器及调用专属网络已停止，再保存检查点并允许同一任务重试。远端 feature 分支发布由宿主侧 go-git 实现，需会话完成且显式启用；PR 已有只读结果验证接口，受管创建尚未实现。跨代理受管委派采用独立子会话，子候选通过校验后才交接给主代理；Docker 工具网关已做调用与网络边界验证，其他后端尚无同等级的工具密钥隔离。
