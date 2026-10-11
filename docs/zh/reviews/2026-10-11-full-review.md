# Turnyard 全面代码审查（2026-10-11）

审查基线：`f206156ffc516fc0b8c8eea689fd20c3f78eb3c5`。审查开始时工作区干净；原审查仅新增这份报告，未修改实现。下方结论、问题、行号和复现结果描述原基线；F1–F7 的后续处理及新镜像验证见[修复与复验](#修复与复验2026-10-11)。

审查范围：架构与产品方向、模块依赖、会话与任务状态机、并发与容量、数据库与文件恢复、四种 Agent 驱动、工具与沙箱权限、Git 与外部交付、可观测性、性能、测试/CI 和文档。采用源码检查、现有本地检查，以及临时源码副本中的故障复现。没有使用真实模型、OSS 或 GitHub 凭据，没有操作现存业务状态。

## 结论与处理顺序

**项目的单机执行内核方向成立，但当前提交不宜直接作为发布候选。** 已有领域边界和审计链比普通 CLI 包装器完整：任务有明确状态，候选绑定 Git 与交付物摘要，存储拥有事务，委派子任务拥有独立工作区，外部不确定结果要求核查。当前主要风险集中在功能组合与恢复语义，以及镜像构建/依赖门禁。

确认 7 项问题：3 项 P1、4 项 P2。优先级表示修复顺序，并非漏洞 CVSS；没有确认 P0 或实际沙箱逃逸。

| 编号 | 优先级 | 问题 | 验证级别 |
| --- | --- | --- | --- |
| F1 | P1 | Containerfile 漏复制必需的 `internal/collections`，镜像构建无法完成 | 按 COPY 文件集本地构建复现 |
| F2 | P1 | Connector 将任意 GET 错误当成可上传条件，可能覆盖既有对象 | 本地脚本连接器复现 |
| F3 | P1 | 状态恢复的路径重定位对中文路径失效，双引号目标路径会破坏 JSON | 直接调用真实 SQLite 重定位函数复现 |
| F4 | P2 | 仅有内置人工输入工具时，本地网关缺少目录锁挂载 | 真实准备代码与 SandboxRun 捕获；未运行 OCI |
| F5 | P2 | 委派裁剪模型绑定，破坏独立模型的 `llm-rerank` | Engine 委派夹具加真实工具准备函数复现 |
| F6 | P2 | 允许恢复旧检查点，却仍以最新检查点作为续跑基准 | 实际归档、恢复及后续校验复现 |
| F7 | P2 | 当前 Go 工具链使仓库漏洞检查门禁失败 | 实际运行仓库规定的 Govulncheck |

建议先修 F1–F3，再把 F4–F6 的组合场景加入回归；F7 与 F1 一并纳入发布准备。完成这些工作后，再对选定后端和 Agent 做真实验收。无需为了修复这些问题先拆分服务或重写执行器。

## 已确认问题

### F1 · P1 · 镜像构建阶段遗漏内部依赖

**位置：** `Containerfile:4-9`；引用方 `internal/toolgateway/gateway.go:22`；CI 入口 `.github/workflows/ci.yml:46-47`。

构建阶段只复制 contracts、fault、humaninput、toolgateway 及 gateway 命令。`toolgateway` 直接导入 `internal/collections`，而该目录从未复制进构建层。完整工作树中的 `go test` 可以通过，镜像内的裁剪源码树则无法解析此导入。

**复现与影响：** 在临时目录中严格复制第一个 `go build` 之前的 COPY 文件集，用本机 Go 1.27.1 执行同一 gateway 构建，退出码 1：

```text
internal/toolgateway/gateway.go:22:2: no required module provides package
github.com/Covalane/turnyard/internal/collections
```

这是源码文件缺失，与本机是否有 Docker、镜像拉取成功与否无关。CI 的 Docker job 会在边界探针之前被构建阻断。历史构建成功记录不能证明此提交可构建。

**务实修复：** 补上 collections 的 COPY；同时选择一种稳定的依赖复制策略，例如复制全部内部源码，或根据 `go list -deps` 维护构建闭包。继续保留真实镜像构建门禁。

**验收：** 无缓存构建镜像，确认 bridge 与 gateway 都能执行；跑现有 Docker boundary job。本次只验证了源码构建故障，没有实际执行镜像构建。

### F2 · P1 · 连接器读取失败后仍写入，不能保证不覆盖

**位置：** `internal/artifacts/delivery.go:43-50`；错误建模 `internal/artifactio/connector.go:135-148`；连接器契约 `internal/contracts/types.go:164`。

`DeliverConnector` 先 GET；任何非 nil 错误都会继续 PUT。连接器没有区分对象不存在、权限失败、网络超时或读取对象超过限额。对于存在但暂时不可读的对象，PUT 可能覆盖原内容。即使 GET 明确返回不存在，GET 与 PUT 之间也存在竞争窗口。

**复现与影响：** 本地脚本连接器模拟“对象已存在且内容不同，第一次 GET 暂时失败，PUT 具有普通覆盖语义，第二次 GET 成功”。真实 `DeliverConnector` 把远端内容改成 `new`，最终结果为 `status=present`。这不是 OSS 实测，而是接口允许的连接器行为复现。

`delivery.go:15-17` 声明 conflicting object 永不覆盖，现有实现本身无法兑现。文档 `docs/zh/lifecycle.md` 已要求上传命令使用“已存在则跳过”，因此风险依赖连接器配置；示例中的 `--ignore-existing` 可降低此风险，但 Schema 和执行器没有强制该语义。内容寻址 URI 也不能证明目标当前一定存放对应内容。

**务实修复：** 给连接器引入明确的 `NOT_FOUND` 与 `UNKNOWN/FAILED` 结果；只有明确不存在才尝试创建。写入接口要求原子的 create-if-absent，或显式声明并验收这一能力。对不能提供条件写入的连接器，应收窄“不覆盖”承诺并拒绝把未知读取状态变为覆盖式 PUT。保留上传后读回；未知结果靠读回收敛。

**验收：** 测试 GET 超时/403 不调用 PUT、已有同摘要跳过、已有不同摘要拒绝、GET/PUT 之间竞争、PUT 超时后读回成功、读回失败仍未验证。对真实连接器另做专用前缀验收。

### F3 · P1 · 恢复重定位可能留下旧路径或写坏持久化 JSON

**位置：** `internal/stateops/relocate.go:31-32,56-69`；恢复调用 `internal/stateops/backup.go:419-424`；后续读取 `internal/engine/service.go:138-140`。

有两个触发方式，来自同一重定位实现：

1. SQL `substr` 按字符定位，传入的 `len(from)` 是 Go UTF-8 字节数。旧状态根包含中文等多字节字符时，WHERE 前缀比较不匹配，`sessions.workspace`、`checkpoints.archive_path`、`invocations.log_path` 留在旧根。
2. candidates.checks 与 events.payload 使用原始字符串替换，没有按 JSON 字符串重新编码。目标根包含 `"` 时，替换后 JSON 无效；原路径若带需转义字符，也可能根本匹配不到。

**复现与影响：** 使用临时 SQLite 表调用真实 `relocateDatabase`：

```text
from=/tmp/状态, to=/tmp/restored
workspace="/tmp/状态/sessions/one/workspace"   # 未重定位

from=/tmp/state, to=/tmp/new"state
checks={"log_path":"/tmp/new"state/log"}      # json.Valid=false
```

两次调用都返回 nil。外层恢复会将目录正式重命名为目标路径并返回成功；用户删除/移走原状态根后，后续续跑、日志和检查点读取可能失败。旧根仍存在时，还可能继续引用旧工作区。JSON 损坏则影响任务结果解析。这是恢复正确性问题，不应仅靠要求用户使用 ASCII 路径解决。

**务实修复：** 逐字段以 Go 的 `filepath.Rel` 检查目录边界并拼接新根，或使用 SQL `length(?)` 保持字符语义。JSON 必须解析为结构后仅重写定义为运行路径的字段，再 Marshal；避免替换任意任务正文中的相似字符串。重定位完成后重新解码所有受影响 JSON，并检查运行路径均属于目标根，再正式安装恢复目录。

**验收：** ASCII、中文、空格、双引号、反斜杠及相同前缀兄弟目录；以完整 Backup → Restore → OpenStore → SessionResult/TaskResult → 续跑为主线。此次动态复现针对内部重定位函数，完整中文路径恢复业务链未运行。

### F4 · P2 · 内置人工输入工具未计入本地网关挂载条件

**位置：** `internal/agents/runtime.go:69-73`；`internal/agents/tools.go:100-108,170-172`；`internal/toolgateway/gateway.go:338-340,379-382`；`internal/sandbox/command.go:23-35`。

`PrepareTools` 总是添加内置 `turnyard_input` 后端，并将 CatalogLock 设为 `/turnyard-catalog/<agent>.sha256`。但 RunMain 只在用户声明了 Tools 或 Delegates 时为本地网关设置 CatalogDir。用户不配置额外工具/委派时，内置人工输入仍依赖网关，挂载条件却为 false。

**触发范围：** Apple container、Podman 的本地网关，以及 Docker `network=none`；Docker 常规网络和 model-only 的工具 sidecar 自带目录锁挂载，不受此条件影响。非 Docker 的默认网络也使用同一本地条件。

**复现与影响：** 临时测试调用真实 PrepareTools 与 RunMain，捕获 SandboxRun。在上述三种后端的 network=none 配置下均得到本地 gateway 启动命令和空 `CatalogDir`。网关加载目录时仍试图创建 `/turnyard-catalog`；OCI 根文件系统只读，该路径也未在镜像预建。工具发现/调用会因此失败，人工问题不能可靠进入 `needs_input`。

该问题是**目录锁挂载缺失**，不是 `/workspace/.turnyard-output` 缺失；后者由共用沙箱代码正常挂载。

**务实修复：** 所有使用本地网关的调用都挂载 CatalogDir，或让无用户工具的内置网关明确不使用目录锁。优先前者，使准备配置和挂载配置由同一能力描述驱动。

**验收：** 无 Tools、无 Delegates 的最小配置，在 Apple/Podman/Docker-none 上实际发现并调用 request_input，写入本次 invocation 记录，再由 Engine 读回。本次未实际启动 OCI，因此“容器中创建路径失败”依据只读根与挂载代码推导，挂载参数缺失已动态确认。

### F5 · P2 · 委派环境丢失工具搜索所需模型绑定

**位置：** `internal/engine/delegation.go:153-157`；`internal/agents/tools.go:109-113,157-167`；Kimi 实际调用点 `internal/agents/kimi/kimi_driver.go:64-66`。

子环境继承父环境的 ToolSearch，却将 ModelBindings 缩减成子 Agent 主模型这一项。父环境将 llm-rerank 绑定到另一个合法模型时，子环境留下悬空引用。LoadSession 对顶层配置的引用检查（`internal/contracts/session.go:72-74`）无法保护这一运行时生成的配置。

**复现与影响：** 在已有成功委派测试夹具中增加独立 `reranker` 绑定，并在子驱动中执行真实 `agents.PrepareTools`。子会话/任务能被创建，但子调用报 `INVALID_SPEC: tool search model binding is unavailable`，状态成为 `unknown`；正常 verified 断言失败。

单纯保留原有 mock driver 时测试仍通过，因为 mock 不执行工具准备；本次专门补上真实准备步骤后才复现。这说明现有 Engine 委派测试覆盖了隔离与交接流程，却不能证明组合后的环境能被真实驱动消费。四个驱动均调用共用 PrepareTools，因此不限 Kimi。

**务实修复：** 按依赖闭包保留子 Agent 主绑定和工具搜索绑定，并去重。不要盲目复制全部凭据；绑定保留与凭据注入分别控制。对子环境运行公共语义校验，在创建子会话之前暴露缺失引用。

**验收：** 独立 rerank 绑定、与主模型共用绑定、lexical 三组委派；断言子配置可准备、搜索凭据进入正确容器、模型凭据不扩大。该复现未调用真实模型。

### F6 · P2 · 旧检查点恢复成功与后续准入规则不一致

**位置：** `internal/engine/checkpoint.go:74-113`，尤其 `90-108`；`internal/engine/checkpoint.go:19-38`；`internal/store/queries.go:183-191`；续跑入口 `internal/engine/task.go:101-102`。

Restore 接受会话任意检查点，按所选归档恢复，并写一条 restored 事件。它不更新当前恢复基准，也不要求所选检查点是最新。后续 verifyCheckpointHead 总按 created_at 读取最新检查点。当两个检查点的仓库状态不同时，旧归档恢复会报告成功，但后续准入拒绝该状态。

**复现与影响：** 在同一会话创建 A、B 两个实际归档，B 的工作区多一个文件；移除测试工作区和 Agent 状态，Restore(A) 返回 `restored=true`，紧接着调用正式续跑所用的校验函数返回 `WORKSPACE_DRIFT: workspace differs from last checkpoint`。

此外，归档元数据写入了 native_id（`checkpoint.go:63`），Restore 读取结构没有该字段，不能将这条接口当成完整历史回滚。当前文档仅说明目录须不存在，没有说明“只能恢复最新点才可继续”。

**务实修复：** 若产品只支持灾难恢复，第一步直接拒绝非最新检查点，并在任何解包前提示可恢复的 ID。若确需回滚，应另定义 active checkpoint、原生会话 ID、后续任务/候选的失效规则和审计事件；不能仅将旧检查点重新保存为最新来绕过一致性检查。

**验收：** A/B 两点的旧点拒绝、最新点恢复后实际续跑；若支持回滚，还需验证 NativeID 和已验证交付物不会引用错误历史。本次已运行归档与校验复现，未运行真实 Agent 原生会话回滚。

### F7 · P2 · 当前依赖基线未通过漏洞检查门禁

**位置：** `go.mod:3,60`；`Containerfile:1`；`.github/workflows/ci.yml:31-32`。

本机实际工具链为 `go1.27.1 darwin/arm64`。运行仓库规定的命令：

```sh
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
```

命令非零退出（被运行工具报告 `exit status 3`，go run 外层退出 1）。扫描器报告代码命中 9 个标准库漏洞：GO-2026-6617、6613、6611、6610、6608、6607、6605、6604、6603，标准库修复版本均标为 Go 1.27.2；部分条目同时列出 x/net v0.57.0 → v0.60.0。另报告 2 个 imported-package 和 5 个 required-module 层面的未命中调用问题。

**影响与证据边界：** 确认的是当前规定门禁失败及构建镜像仍固定旧工具链，不能据此断言 9 项均可在本产品被实际利用。例如扫描器明确将 GO-2026-6604 标为 Windows，而本次宿主是 macOS；HTTP server 条目也要按真实网络入口再判断。未进行漏洞利用或生产威胁验证，也未查询远端 GitHub Actions 的运行状态。

**务实修复：** 按官方公告确认适用平台/调用路径，更新 Go 工具链、基础镜像及摘要；对 x/net 等依赖做针对性升级后重跑扫描。保留扫描器为门禁，不因“单元测试通过”忽略它。不要只修改 go.mod 而留下容器内旧 Go。

**验收：** 宿主二进制和镜像内 gateway/bridge 的工具链一致，扫描器通过或有逐项、可审计的例外判断；升级后重跑 race 与 Docker boundaries。

## 架构、模块与产品方向

### 保留单机执行内核，先稳定契约

`docs/zh/architecture.md` 将边界定为一台机器及其状态目录，当前实现与此一致。Unix socket supervisor、SQLite、本地工作区与 OCI 运行器是一组紧密相关的状态管理组件。没有证据表明拆成网络服务会改善目前的问题；拆分反而要新增跨服务事务、租约、凭据和恢复协议。

建议继续作为可嵌入上层调度系统的执行节点：上层拥有跨机器编排和需求审批，Turnyard 拥有单机调用、候选、检查和恢复事实。`verified` 只代表配置的机器检查及声明交付物通过，文档对此界定正确，应持续保留。

### 当前模块边界总体合理

| 模块 | 已具备的边界 | 后续改进 |
| --- | --- | --- |
| contracts / lifecycle / fault | 输入、状态名称和错误码有独立归属 | 将 LoadSession 的语义检查抽为可重用函数，覆盖生成的子环境 |
| store | Ent client 私有，领域命令拥有事务；Engine 不直接写 SQL | 持续以迁移夹具约束领域不变量；避免只凭实体字段存在就认可旧库 |
| engine | 编排任务、候选、检查、委派、恢复 | 明确恢复基准及“准备/已启动/外部结果未知”的阶段事实 |
| agents | 四驱动与 registry 分离，共享工具、模型和状态保护 | 用真实准备函数覆盖驱动组合；把运行前检查从 Invoke 中显式拆出 |
| sandbox | 共用 OCI 生命周期和独立方言；后端能力可拒绝 | 工具/模型网关编排偏重 Docker，可提炼内部能力对象，尚不需要独立服务 |
| artifacts / artifactio / gitstate | 候选字节验证、传输命令和 Git 分工明确 | 连接器增加错误/条件写入契约；远端 Git 与对象交付采用一致的不确定性术语 |
| transport | CLI/RPC、关联 ID 与大小/超时限制 | 统一有副作用操作的会话互斥入口，并准备分页查询 |

受管委派已经有实质实现：子会话、独立任务、独立仓库、父任务引用、只读交接和父级重新验证。本次不能沿用早期“只有设计、未实现”的描述。它仍是深度受限的受管委派机制，不是完整分布式多 Agent 平台。

## 生命周期、并发与持久化审阅

正向证据包括：`Store.AddTask` 在事务内阻止有未解决任务的会话追加新任务（`internal/store/task_commands.go:40-45`）；RunTask 和 VerifyCandidate 使用 Service 会话预留；supervisor 还维护活动集合；容量控制为委派保留子任务空间（`internal/engine/capacity.go:150-153,213-232`）。已有 race 测试通过，但这不能证明所有多操作交错均正确。

SQLite 使用单连接、外键、WAL 和 busy timeout（`internal/store/store.go:43-45`）。迁移使用编号及校验和，升级前备份、完整性检查、事务和外键检查（`internal/store/migrations.go:64-152,170-190`）。这适合当前单 supervisor 模式；没有必要先改成共享数据库。

数据库与磁盘是两个事实域。检查点先落文件后入库，备份/恢复另有独占锁和暂存安装流程。仍应明确断电与 ENOSPC 的可恢复状态：文件 rename 并不等于已经证明断电持久性，文件和 DB 也没有跨介质事务。本次未做断电注入、磁盘写满、大型旧库迁移或真实机器迁移。

待决策事项：

- **恢复范围：** F6 应先选择“仅最新点灾难恢复”还是“历史回滚”。这将决定任务和候选能否保持现有语义。
- **准备失败分类：** `internal/engine/turn.go:117` 在进入整个 Driver.Invoke 前设置 agentAttempted，`task.go:158-159` 因而将配置/鉴权等运行前失败也分类为 unknown。F5 的复现实际观察到该结果。保守分类不会造成自动重试副作用，但会要求无模型调用的配置故障也人工 reconcile。建议驱动返回可证明的阶段信息；不要简单按错误码把所有错误改成 failed。
- **互斥归属：** Restore/Reconcile 与 Run/Verify 的保护路径并不统一，`transport/server.go:265-272` 直接调用前两者。现有目录和状态检查能阻止一些冲突，本次未确认可利用的并发破坏；应对这几类命令建立统一 Service 级操作预留并增加交错测试，尤其覆盖子任务不在 supervisor.active 中的情况。
- **容量语义：** 当前预留按 Agent sandbox 的 CPU/内存计算；sidecar、数据库、日志缓冲和宿主连接器成本另计。将限额表述为任务准入预算更准确，尚不能作为整机资源硬上界。

## Agent、工具、沙箱与权限

安全设计的主要优点是权限有实际执行点：仓库只读/读写挂载、只读 `.git`、只读根、调用专属输入输出目录、工具网关按允许目录和参数 Schema 路由、外部 schema loader 被拒绝，以及恢复提取时拒绝路径穿越和逃逸链接。敏感能力由可信 Environment 提供，Work 不能自定义连接器命令。

Docker 的模型/工具 sidecar 与私有网络是明确的隔离档位；其他后端同容器工具无法提供同等密钥隔离，当前文档承认这一点。提示词、Agent 自己的 permission 规则、工具不出现在目录里，都不应扩大解释为完整安全边界。默认网络仍能出站，也不能把 `git.remote_writes=none` 理解为对所有任意程序外发的通用网络封锁。

需要建立组合矩阵，至少按“运行时 × 后端 × 网络模式 × 是否用户工具 × 是否委派 × 是否 rerank”选取代表性覆盖。F4 和 F5 都来自单个模块测试通过、组合配置失效；增加更多相同 mock 的 happy path 不能解决这个问题。

建议冻结的部署承诺：优先提供一个完整复验的 Docker model-only 档位，再分别声明 Apple、Podman、gVisor 的功能和隔离差异。不要把一次单任务验收扩展为同等级恢复、资源限额和委派证明。

## Git、检查与交付

候选摘要、Git base/head/tree/branch、不可写封存输出、可信检查后上传的顺序是正确的。`internal/engine/checks.go:122-158` 复验候选状态及摘要；检查以只读 scope 挂载仓库。受管子任务的结果经只读交接返回父任务，父任务仍需完成自己的检查，而非子任务通过就自动继承 verified。

Git 发布要求会话 completed、显式 remote writes，并核对每任务 feature 分支（`internal/engine/publish.go:25-91`）；`internal/gitstate/publish.go:61-89` 先读远端，拒绝不同 head，推送后读回，对超时追加核查。该部分的实现比通用连接器更完整，应作为 F2 设计参考。

有两个产品语义需保持清楚：多仓库推送可能部分成功，不具备远端全局事务；PR 目前是读回验证，受管创建/合并不是已有能力。发布失败结果仍可进一步区分“确认未写入”和“写入结果未知”，但本次没有确认其造成错误重复写入。

可信检查本身由环境管理员定义；检查脚本可能读取 Agent 生成的代码、依赖和测试数据，所以 verified 仍不是完整需求/安全证明。需要需求级人工审阅时，应在上层显式提供审批流程。

## 可观测性、性能与容量

可观测性已有 request/session/task/invocation 关联 ID、稳定错误码、来源/操作链、事件和受限原始日志。`internal/observe/logging.go:90-108` 不直接复制错误正文到运行日志，`internal/sandbox/run.go:197-219` 对已知凭据值脱敏并限制日志大小。这些是正向实现证据；不是“任意秘密均不会泄漏”的证明，Agent 原生状态和内容本身仍应按敏感数据管理。

后续最有价值的指标是准备排队、模型启动前失败、模型首次响应、工具延迟、检查耗时、checkpoint 体积/耗时、清理确认耗时、unknown 数量和 reconcile 年龄。当前没有足够证据给出吞吐或延迟 SLA，本次没有负载基准。

性能上的可见成本与建议：

- `internal/gitstate/archive.go:56-112` 每次遍历并打包整个 workspace 和过滤后的 Agent state；`internal/engine/turn.go:96` 调用前即保存检查点。大仓库、依赖目录及长会话会放大 I/O。先量化 P50/P95 体积与时延，再决定排除可再生缓存、增量归档或内容寻址复用。
- 归档写入端与恢复端的接受范围应对齐：恢复有单文件 2 GiB、总量 8 GiB 限制（`internal/gitstate/archive.go:218-224`），保存端未在对应遍历中预先施加同样限制。应在保存前校验可恢复性；本次未创建 GB 级文件做边界复现。
- `Store.Events` 与 Tasks/Invocations 读取完整结果集，RPC 最终响应限 32 MB（`internal/store/queries.go:99-135`、`internal/transport/frame.go:10-12`）。长会话应增加 limit/cursor；当前属于规模风险，未做达到限额的复现。
- 输出缓冲按 stdout/stderr 分别上限 16 MB，并可能再拼接字符串；并发额度需包含这部分宿主内存。工具 sidecar 的额外 CPU/内存也应单独统计。
- 文档准确说明当前只有磁盘告警，没有硬配额或自动清理。优先在磁盘紧张时拒绝新工作，保留查询、备份和恢复能力；不要先自动删除 unknown/paused 会话。

## 测试、CI 与文档一致性

本次执行结果如下。现有测试的 cached 结果按 Go 测试工具的有效缓存记录，不宣称全部重新执行；定向复现均使用 `-count=1`。

| 检查 | 本次结果 | 能证明什么 |
| --- | --- | --- |
| `go test -race ./...` | 通过，部分包使用缓存 | 现有默认测试与竞态检查；不含需显式启用的真实外部链路 |
| `go vet ./...` | 通过 | 默认构建路径静态检查 |
| `go run honnef.co/go/tools/cmd/staticcheck@v0.8.1 ./...` | 通过 | 当前静态规则 |
| `gofmt -l ./cmd ./internal ./test ./examples` | 无输出 | 当前源码格式 |
| `go test -tags integration -run '^$' ./test/...` | 通过，no tests to run | 带标签测试可编译，不是集成执行 |
| `go vet -tags integration ./...` | 通过 | 带标签路径静态检查 |
| `go generate ./internal/store` | 临时源码副本再生后，与原 Ent 文件逐文件摘要一致 | 生成代码未漂移；没有改原仓库 |
| 仓库规定的 Govulncheck | 失败 | F7 的扫描门禁问题 |
| Containerfile COPY 子集构建 | 失败，缺 collections | F1 的构建闭包错误 |
| `TestReviewRelocation` | 两个故障均复现 | F3；测试断言的是观察到故障，不代表修复通过 |
| `TestReviewConnectorReadFailureOverwrites` | 复现覆盖且返回 present | F2；仅本地模拟连接器 |
| `TestReviewOldCheckpointRestore` | 复现 restore=true 后 drift | F6；真实归档与 SQLite，未接真实 Agent |
| `TestReviewBuiltinHumanInputCatalogMount` | 三组均捕获空 CatalogDir | F4；未执行容器 |
| `TestReviewDelegationRerank` | 正常成功断言失败，子任务 unknown / INVALID_SPEC | F5；真实工具准备 + mock Agent/OCI |

定向测试只添加在本次临时源码副本中，没有并入仓库。报告提供了触发条件、实际输出和应增加的回归标准；后续修复应将最小回归测试纳入正式测试套件。

CI 已包含镜像构建与真实 Docker 边界探针，方向正确。F1 会阻断这些探针，F7 会阻断 Go job。不能仅根据基础测试通过写“CI 全绿”；本次也没有查询远端 CI 状态。

文档审阅结果：

- `docs/zh/validation.md:5-7,24-30` 明确区分历史记录、未公开原始日志、失败及未通过路径，证据边界写得较好；本次不把这些历史声称当成重新验收结果。
- `docs/zh/release.md` 要求固定提交/镜像和真实矩阵，建议保留；把本报告修复后的基线加入新的验证记录。
- `docs/zh/lifecycle.md` 关于恢复到新根会更新路径的保证，需要 F3 修复；关于人工输入通用工具路径的说明，需要 F4 覆盖。
- `docs/zh/README.md:65` 的 checkpoint restore 用法应补充 F6 的最新点限制或回滚语义。
- 架构文档已区分 Docker 与其他后端的凭据边界、PR 验证与创建、verified 与完整正确性。这些限制应同步保留在英文版；本次以中文技术文档及代码为主，未逐句校对中英文翻译。

## 已排除或未提升为确认缺陷的候选

| 候选 | 审阅结论 |
| --- | --- |
| unknown 后可开同会话新任务 | 排除。Store.AddTask 在事务内拒绝任何非 verified/cancelled 任务，见 `internal/store/task_commands.go:40-45`。不能只看某一层的状态判断就报绕过。 |
| 非 Docker 人工输入没有输出目录挂载 | 排除该归因。输出目录有挂载；确认的问题是 F4 的 CatalogDir 条件。 |
| 一个委派 happy path 通过即可证明 rerank 组合 | 排除。原 mock driver 绕过 PrepareTools；补入真实准备函数后 F5 才显现。 |
| 旧 checkpoint 可以静默继续覆盖最新成果 | 未作此判断。已观察到后续 WORKSPACE_DRIFT 拒绝；F6 是恢复成功语义与准入不一致。 |
| 任意工具名/外部 JSON Schema 引用可绕过网关 | 未找到确认依据。网关有目录匹配、参数校验和 denyExternalSchemaLoader。未做完整渗透测试。 |
| 容器 stdout 无限占用宿主内存 | 排除简单版本。cappedBuffer 有限额并继续 drain，超限返回明确错误；仍需把并发缓冲算入容量。 |
| 管理子任务共享父可写仓库 | 排除。子环境按父当前 Git 状态准备独立仓库，scope 不能升级，交接另有摘要复核；详见 delegation 与 export 测试。 |
| 历史云/OCI/OSS 验收等于当前提交验收 | 排除。文档已承认版本与环境差异；本次没有运行这些链路。 |
| 已证实具体云攻击、凭据外泄、断电恢复损坏 | 未验证，不作此断言。 |

## 建议的下一轮验收

1. 修复 F1、F2、F3，并将本次最小复现转为正式回归；先确保可以构建且恢复/外部交付不错误报告成功。
2. 修复 F4、F5，增加真实配置准备与最小 OCI 工具调用的组合测试；选择 F6 的恢复语义并冻结契约。
3. 升级并验证 F7 的依赖基线，重跑仓库全部静态/竞态/生成代码门禁及 Docker boundaries。
4. 在最终 commit 和固定镜像摘要上复验 Docker model-only 的四种 Agent、人工回复、委派与中断恢复、多任务多仓库及交付。其他后端按承诺档位分别验收。
5. 后续再进行大型状态/磁盘压力/断电演练和可观测性指标建设，依据测量决定增量 checkpoint、分页、硬配额及调度扩展。

**未验证的真实路径：** 本次没有启动 Docker/Apple/Podman/gVisor，没有云模型推理、真实 MCP 服务、OSS 上传/删除、GitHub 推送/PR 操作，也没有负载、漏洞利用或断电测试。没有对原业务数据库及 `.turnyard` 中历史证据进行内容审计。

## English summary

The single-host execution architecture is coherent and already includes durable lifecycle records, isolated managed delegation, candidate verification, and explicit reconciliation. This commit is not release-ready: seven findings cover an incomplete container build context, unsafe connector retry semantics, path relocation errors, missing local gateway mounts, delegated reranker configuration, ambiguous historical checkpoint restore behavior, and a failing vulnerability gate. Local default checks pass except Govulncheck; targeted reproductions confirm the reported failures. No live cloud, OCI, OSS, or GitHub acceptance was performed.

## 修复与复验（2026-10-11）

用户确认先修复 F1–F7；F6 采用仅恢复最新检查点的契约。以下记录对应原基线之上的修复，不改写上面的审查复现结果。其他性能与规模建议仍属于后续评估，不计为本轮已完成。

| 问题 | 实现与回归 |
| --- | --- |
| F1 | 构建阶段复制完整 `internal` 目录，避免依赖闭包随新包增加再次遗漏。完整镜像构建及真实容器结果见下表。 |
| F2 | 连接器新增明确的缺失退出码和 `create-only` 写入声明。普通读取失败不再触发上传；写入命令必须在服务端原子拒绝覆盖。上传回执丢失、竞争创建后按读回摘要确认；无法读回保持 `unverified`。8 个本地子进程场景覆盖已有一致/冲突对象、读取不确定、缺失、竞争一致/冲突、丢失回执及读回失败，既有对象保持不变。 |
| F3 | 路径按目录边界重定位；JSON 解析后仅调整受管 `log_path` / `local_path` 字段，保留自由文本、URL 和大整数。操作位于同一 SQLite 事务内。中文、空格、引号、反斜杠、同名前缀目录及无效 JSON 回滚均有回归；完整备份/恢复/重新打开也使用特殊字符根目录。 |
| F4 | 本地工具网关始终传递目录锁挂载，包括只有内置人工输入工具的环境。Docker `none`、Podman、Apple `container` 的准备代码均有捕获断言；真实 Docker MCP 调用另见下表。 |
| F5 | 子环境保留主代理及工具排序器实际引用的模型绑定，并在建会话前统一校验引用。覆盖无搜索、词法搜索、共享模型、独立模型及缺失模型；受管委派测试执行真实 `PrepareTools`，不再跳过触发故障的准备路径。模型绑定保留与凭据注入分开，后者仍遵循既有工具网关和沙箱权限规则。 |
| F6 | 旧检查点在解包前返回 `CHECKPOINT_NOT_CURRENT`，消息提供当前最新 ID。Restore 使用会话操作预留，拒绝与执行/校验重叠。真实 A/B 归档回归验证旧点拒绝且工作区未改变、忙碌会话拒绝、最新点恢复后通过续跑准入检查。未增加历史回滚。 |
| F7 | Go 与基础镜像升级至 1.27.2，更新相关 `x/*` 依赖。Ent、Staticcheck、Govulncheck 改用 `go.mod` 固定的 `tool` 声明，生成与 CI 使用同一组依赖。Staticcheck 使用上游提交 `452d5bb86b45`，搭配 `x/tools` 0.52.0，解决稳定版 0.8.1 对新导出格式的兼容问题。 |

现有写入连接器配置需要补上 `put_mode` 与 `not_found_exit_codes`，并更换为真正提供原子条件创建的命令；只添加声明不能使可覆盖写入变安全。文档示例 `/opt/connectors/object-transfer` 是部署方提供的可信适配程序，不是仓库自带的可执行文件。真实 OSS 测试改为显式要求该适配程序，避免继续使用不安全的通用失败码和预检查方案。

### 本轮验证记录

| 检查 | 结果与范围 |
| --- | --- |
| `go test -race -count=1 ./...` | 通过；本轮重跑默认测试，非缓存结果。真实外部路径按开关跳过，不计为通过。 |
| `go vet ./...`、`go vet -tags integration ./...` | 通过。 |
| `go tool staticcheck ./...` | 通过。 |
| `go tool govulncheck -show verbose ./...` | 退出码 0，无可达漏洞、无导入包漏洞；仍报告 `x/crypto/openpgp` 的模块级维护警告 `GO-2026-5932`，本项目没有导入/调用该包，公告没有修复版本。 |
| `go generate ./internal/store` 及 Ent 文件差异检查 | 通过，生成文件无差异。 |
| `go test -tags integration -run '^$' ./...` | 通过，只有编译，没有执行可选集成测试。 |
| 格式与 `git diff --check` | 通过。 |
| 完整 `Containerfile` 构建 | 通过，Docker Desktop 上构建 Linux/arm64。首次因 Debian 包下载 502 失败；增加下载重试后完成，包括新版 Go 编译和四种代理安装。 |
| 四种 CLI 启动检查 | 在新镜像、无网络下全部退出 0：OpenCode 1.18.32、Kimi 2.1.1、Claude Code 2.1.283、Codex 0.157.1；Go 为 1.27.2。这只证明启动与版本，不是模型推理验收。 |
| `TestRealSandboxFaults` | 新镜像上实际执行，通过挂载与只读权限、日志脱敏、超时清理、网络关闭探针。 |
| `TestModelOnlyDockerBoundary` | 新镜像上实际执行，通过模型网关可达、代理直接出站/访问宿主被拒，以及路径绕过被拒。 |
| `TestModelGatewayCannotBeUsedFromBridge` | 新镜像上实际执行，普通 bridge 容器不能使用模型网关。 |
| `TestToolGatewayKeepsCredentialOutsideAgent` | 新镜像上实际执行，通过 MCP 发现、未授予调用拒绝、已授予工具执行及工具凭据隔离。 |
| `TestBuiltinInputLocalGateway` | 新镜像的 Docker `none` 环境、无用户工具/委派，完成真实 MCP 初始化、发现与人工输入调用；宿主读取到中文问题，目录锁持久化成功。该测试及 CLI 启动检查已加入 CI。 |

验证镜像为 `turnyard-agent:review-fixes`，本地镜像 ID：`sha256:148b907b586748500dc5aff44770a4b265cb67d49b991595c5f408579409b86f`。它是本次构建标识，没有推送 registry，不是跨平台 manifest 摘要。上表记录本地复验结果；远端 CI 应按实际提交查询 [GitHub Actions](https://github.com/Covalane/turnyard/actions)。

真实 Docker 探针的复现命令：

```sh
docker build -f Containerfile -t turnyard-agent:review-fixes .
TURNYARD_INTEGRATION_BACKEND=docker TURNYARD_INTEGRATION_IMAGE=turnyard-agent:review-fixes \
  go test -count=1 -run '^TestRealSandboxFaults$' ./internal/sandbox
TURNYARD_NETWORK_E2E=1 TURNYARD_E2E_IMAGE=turnyard-agent:review-fixes \
  go test -tags integration -count=1 -run '^Test(ModelOnlyDockerBoundary|ModelGatewayCannotBeUsedFromBridge|ToolGatewayKeepsCredentialOutsideAgent)$' ./internal/sandbox
TURNYARD_NETWORK_E2E=1 TURNYARD_INTEGRATION_BACKEND=docker TURNYARD_E2E_IMAGE=turnyard-agent:review-fixes \
  go test -tags integration -count=1 -run '^TestBuiltinInputLocalGateway$' ./internal/agents
```

本轮没有重新执行云模型、真实 OSS 写入、远端 GitHub、Apple `container`、Podman 或 gVisor 验收，没有负载或断电演练。默认回归和本地连接器模拟不应扩展为这些路径的验收结论。

### English repair summary

The F1–F7 repairs include targeted regressions and updated Chinese/English guides. Restore now accepts only the latest checkpoint. Connector writes require explicit confirmed absence and a trusted atomic create-only operation; uncertain reads never authorize a write. Race tests, vet, Staticcheck, vulnerability scanning, integration compilation, and unchanged Ent regeneration pass. The full Linux/arm64 image builds successfully; all four CLI startup checks and the five Docker boundary/input tests above pass on that image. Live cloud agents, OSS, other OCI backends, and GitHub task workflows have not been retested in this repair round. The table records local checks; consult the matching GitHub Actions run for remote CI results.
