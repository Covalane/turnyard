# 验证记录与限制

[使用指南](README.md) · [English](../en/validation.md)

验证环境：2026-09-26 至 28 日，macOS Apple Silicon，Go 1.27.1；`Containerfile` 使用 Node 26 基础镜像与固定版本的代理 CLI。任务调用真实云端模型与真实 OCI 容器；早期测试使用本机 Git 仓库，本轮另加入私有 GitHub 仓库的远端 Git 验证。早期链路使用 `turnyard-agent:0.3.0`，受管委派初次复验使用 `turnyard-agent:0.3.1`，严格网络及多运行时复验使用 `turnyard-agent:0.3.2`；业务数据库使用 Ent v0.14.6 与 SQLite。以下按日期保留不同代码阶段的验证记录；被忽略的 `.turnyard/` 证据只存在于执行机器，不随仓库发布。历史通过记录不自动证明后续修改后的工作树也通过同一真实链路。

## 已留存的验证记录

| 路径 | 结果 | 证据范围 |
| --- | --- | --- |
| Go 测试与静态检查 | 通过 | `go test -race ./...`、`go vet ./...`、`staticcheck ./...`；包含状态机、上下文取消、错误链、消息大小边界与 Ent 旧库重开测试 |
| 旧库迁移 | 通过 | 旧 `task_repositories` 的 base commit 导入 `task_bases`，重复打开无重复写入；还在真实旧库副本上确认两个任务和基准记录保留 |
| Apple `container`、Docker 故障探针 | 均通过 | 仓库读写隔离、`.git` 与容器根保护、凭据脱敏、超时清理、镜像缺失与漂移；Docker 额外检查无网络 |
| Go 聊天室两任务 | 通过 | 使用中文需求描述，先完成无历史记录的群聊，再在同一原生会话中增加私聊；两次候选均 `verified`，测试文件未被改动，产生连续的 feature commit |
| 四种代理的 Skill/MCP 注入 | 全部通过 | OpenCode/Kimi + Ollama Cloud；Claude/Codex + DeepSeek。原生事件证明确实读取 Skill、调用 MCP；候选与检查均通过 |
| 四种代理的可执行工具注入 | 全部通过 | 通用桥接器把普通脚本作为 MCP 工具提供；模型实际调用，声明的环境变量到达脚本，候选与独立检查通过 |
| 两仓库完整链路 | 通过 | Apple `container` 和 Docker + OpenCode + Ollama Cloud：四个任务、两个仓库各自的 feature commit、原生续接、人工回复、删除工作区后从检查点恢复；Docker 重跑验证了交付物和会话完成清单 |

机器证据位于本机被忽略的 `.turnyard/` 目录。例如：

- 聊天室：`.turnyard/chat-e2e-20260926-115428/evidence.json`
- 两仓库：`.turnyard/e2e-apple-container-opencode-20260926-114739/evidence.json`
- 注入：`.turnyard/injection-{opencode,kimi,claude,codex}-*/evidence.json`

Go 验收迁移后，新的完整链路证据为 `.turnyard/e2e-go-20260926-120341-ee239829758924a3/evidence.json`。Go MCP 服务已分别通过 OpenCode、Kimi、Claude 和 Codex 的真实注入测试，对应 `.turnyard/injection-go-20260926-120305-dfcd330d3c6fb141/`、`.turnyard/injection-go-20260926-121020-b0b7b8f9221a241f/`、`.turnyard/injection-go-20260926-121022-4e0196cbc1c566e6/` 和 `.turnyard/injection-go-20260926-121055-571daa4430e51ef6/` 中的 `evidence.json`。旧证据仍用于标注迁移前版本的验证范围。

Docker + OpenCode 的 Go MCP 注入与检查也通过，证据为 `.turnyard/injection-go-20260926-121211-3eac3ff3a449548f/evidence.json`；Docker 完整两仓库链路证据为 `.turnyard/e2e-go-20260926-121258-5db5079b11e11f79/evidence.json`。

生命周期与交付物改动后，Docker + OpenCode + Ollama Cloud 的四轮链路再次通过，证据为 `.turnyard/e2e-go-20260926-125228-83c36ff564c3aeb1/evidence.json`：四个任务各声明两个文件，结果逐项包含候选 commit、SHA-256 与 `present`；会话完成清单包含四个 `verified` 候选。取消命令的 Docker CLI 链路也通过，证据为 `.turnyard/cancel-go-20260926-130156-8280160f33300407/evidence.json`。单元测试另覆盖缺失或哈希不符的交付物、完成/取消终态、结束后拒绝新任务与检查点恢复、危险路径拒绝。此轮尚未在 Apple `container` 上重跑新交付物链路。

代码结构整理后，Docker 多仓库完整链路再次通过，证据为 `.turnyard/e2e-go-20260926-132939-11f2e6d457bc0468/evidence.json`。CLI 消息大小处理调整后的取消链路证据为 `.turnyard/cancel-go-20260926-133302-887f8f6c57257c1a/evidence.json`，调整后的完整 Docker + OpenCode + Ollama Cloud 链路也通过，证据为 `.turnyard/e2e-go-20260926-133428-78dd7c383a0cd146/evidence.json`。随后拆分了 Git 归档与沙箱代码、将仓库读写模式常量化，并把普通 Supervisor 命令的工作超时与通信超时分别设为 30 秒、35 秒。

本轮加入会话创建幂等键和可配置超时、显式结果类型、执行阶段与沙箱命令拆分，以及本地 CI 工作流。单元测试覆盖同键重试、重启后重试、输入冲突和并发创建；Ent 再生成前后的文件哈希完全一致。Docker + OpenCode + Ollama Cloud 的四任务两仓库链路再次通过：`.turnyard/e2e-go-20260926-140601-abf1c9bd5857ad3c/evidence.json` 记录四个 `verified` 交付及 `completed` 会话。CLI 取消终态链路也通过，证据为 `.turnyard/cancel-go-20260926-140820-3f1eea658cca395d/evidence.json`。这两项均未推送到 GitHub。

离线保留功能增加了输入生成器、完整状态目录备份、SHA-256 校验、终态会话清理及恢复到新目录。单元测试覆盖备份漂移、篡改、越界符号链接、可执行权限保留和备份后清理。对上面的真实双仓库运行状态创建了 `.turnyard/offline-backup-validation`，恢复到 `.turnyard/offline-restore-validation` 后可读取四个任务和四项交付，数据库中的工作区路径已更新。依赖扫描发现旧版 `golang.org/x/crypto` 的可达漏洞后已升级至 v0.56.0；重新运行 `govulncheck` 报告可达漏洞为零。`go test -race ./...`、`go vet ./...` 和 Staticcheck 再次通过。

复现命令：

```sh
go generate ./internal/store
go test -race ./...
go vet ./...
TURNYARD_INTEGRATION_BACKEND=apple-container go test -run TestRealSandboxFaults -v ./internal/sandbox
TURNYARD_INTEGRATION_BACKEND=docker go test -run TestRealSandboxFaults -v ./internal/sandbox
TURNYARD_CHAT_E2E=1 go test -tags integration -run TestChatroomWorkflow -v -count=1 ./test/acceptance
TURNYARD_E2E=1 go test -tags integration -run TestMultiRepoWorkflow -v -count=1 -timeout 25m ./test/e2e
TURNYARD_INJECTION_E2E=1 go test -tags integration -run TestSkillMCPInjection -v -count=1 -timeout 20m ./test/e2e
TURNYARD_EXECUTABLE_E2E=1 TURNYARD_E2E_BACKEND=docker go test -tags integration -run TestExecutableToolInjection -v -count=1 -timeout 20m ./test/e2e
TURNYARD_DELEGATION_E2E=1 TURNYARD_E2E_BACKEND=docker TURNYARD_E2E_IMAGE=turnyard-agent:0.3.2 TURNYARD_E2E_NETWORK=model-only go test -tags integration -run TestManagedDelegation -v -count=1 -timeout 15m ./test/e2e
TURNYARD_NETWORK_E2E=1 TURNYARD_E2E_IMAGE=turnyard-agent:0.3.2 go test -tags integration -run '^TestModel(OnlyDockerBoundary|GatewayCannotBeUsedFromBridge)$' -v -count=1 ./internal/sandbox
TURNYARD_DELEGATION_RECOVERY_E2E=1 TURNYARD_E2E_BACKEND=docker TURNYARD_E2E_IMAGE=turnyard-agent:0.3.2 TURNYARD_E2E_NETWORK=model-only go test -tags integration -run TestDelegationCrashRecovery -v -count=1 -timeout 15m ./test/e2e
```

通过 `TURNYARD_E2E_RUNTIME`、`TURNYARD_E2E_BACKEND`、`TURNYARD_E2E_PROVIDER`、`TURNYARD_E2E_MODEL`、`TURNYARD_E2E_CREDENTIAL_ENV` 以及对应的 `TURNYARD_DELEGATION_CHILD_*` 可选择主子代理路径。测试会自行构建 Turnyard CLI；运行前须在对应沙箱中构建所选的 `turnyard-agent:0.3.2` 镜像。恢复测试需要主代理的 `OLLAMA_API_KEY` 和子代理的 `DEEPSEEK_API_KEY`。

以前的版本还通过 Kimi、Claude、Codex 的完整两仓库链路；本轮 Ent 迁移及 Go 验收迁移后，对这三种代理重跑了 Skill/MCP 单任务路径，但没有重跑它们的完整多任务链路。这些旧证据不能代替当前版本的完整链路证明。

一次 Claude 测试请求 `deepseek-chat` 时，服务端回报 `deepseek-v4-flash`，执行器将任务标记为 `unknown (MODEL_MISMATCH)`；改用实际模型名后完整链路通过。一次 OpenCode 测试遇到容器内证书校验错误，执行器保留 `unknown`，显式清理和恢复后重新运行通过。这些故障没有计入成功次数。

新增的远端 Git 单元链路已用本机裸仓库验证：固定 commit 的 `remote-git` 克隆、两仓库已验证候选的 feature 分支发布、重复发布幂等、远端分支冲突和部分成功结果。`go test -race ./...`、`go vet ./...`、Staticcheck v0.8.1 通过。随后使用已授权的 `rwasayc` 账号创建私有 `rwasayc/turnyard-auth`：固定 commit 的私有 HTTPS 克隆通过；真实 OpenCode + Ollama Cloud `glm-5.3-flash` 在 Docker 中连续完成 Google OIDC、手机号登录、安全修订、应用级授权及可靠性修订五个任务，均生成 `verified` 候选。主会话 `ses_639031d65c49ec0d` 完成后，Turnyard 首次发布五个 feature 分支到 GitHub；再次发布全部返回 `already_published`。远端 `ls-remote` 读回最终分支 `feature/work_9ae8b9704d46e4b8` 的 SHA 为 `80b2358e89d7b2c1ff33d2669dacc0d9e0fb0a15`。对该固定候选独立运行 `go test -race -count=1 ./...`、`go vet ./...`、Staticcheck v0.8.1 及 `govulncheck@latest` 均通过，关键并发用例另重复 20 次通过。以上证明了本账号对私有仓库的克隆与 feature 分支推送，尚不证明受保护分支、合并或网络中断后的所有远端结果。详细运行证据保存在被忽略的 `.turnyard/auth-validation-20260926/`。

随后从该私有仓库以 `remote-git` 固定 `80b2358e89d7b2c1ff33d2669dacc0d9e0fb0a15` 创建会话 `ses_e92d000467250af0`。Docker 中的 OpenCode + Ollama Cloud 连续完成四个任务，候选均为 `verified`，最终 commit 为 `d87b2e45cc93187e7eb53d1d4c51438d9fb9f44b`。Turnyard 完成会话并发布四个 feature 分支；再次发布均返回 `already_published`，`git ls-remote` 读回最终分支的相同 SHA。中途原凭据收到 Ollama Cloud 会话用量上限 `429`：该调用被记为 `unknown / AGENT_FAILED`，经 `task reconcile` 保存检查点后，使用已验证可用的另一凭据重启 supervisor，并在**同一任务 ID**上重试成功；失败调用没有计为成功。最终候选独立通过 `go test -race -count=1 ./...`、`go vet ./...`、Staticcheck v0.8.1 和 `govulncheck@latest`。真实浏览器确认默认无提供商时页面提示准确、正常页面控制台无错误；预置旧版 HttpOnly CSRF Cookie 后访问 `/api/me`，同值 Cookie 自动改为可读，带令牌的同源请求通过 CSRF 层。私有仓库的[草稿 PR #1](https://github.com/rwasayc/turnyard-auth/pull/1) 最初指向上述最终 SHA，后续由 Codex 直接更新（见下文），尚未合并。该轮验收时，Turnyard 项目本身仍只保存在本地 Git 分支，尚未推送到 GitHub。

逐次调用索引位于被忽略的 `.turnyard/auth-validation-20260926/trace/README.md`，同目录的 `trace-bundle.tar.gz` 打包了任务快照、状态事件、agent 原始日志和检查日志。两个会话合计 9 个已验证任务、12 次 invocation、122 条状态事件；11 份原始调用日志可用。首个会话最早一次 supervisor 中断的 invocation 有状态记录，但没有原始调用日志，因此不能宣称所有调用都有完整逐步记录。日志也不包含云模型服务端的内部推理。

在上述 Turnyard 运行结束后，Codex 直接修订了授权项目的同一 feature 分支，新增提交 `040062f`，用于加固 CSRF/OAuth 持久化、补充进程外验收和 CI。该提交**不属于**上述 9 个 Turnyard 已验证任务。本地 `go test -race -count=1 ./...`、`go vet ./...`、Staticcheck 与 Govulncheck 均通过；该提交的 GitHub Actions [push 检查](https://github.com/rwasayc/turnyard-auth/actions/runs/36251981944)和[PR 检查](https://github.com/rwasayc/turnyard-auth/actions/runs/36251982065)也均通过。Turnyard 此次新增的检查定义摘要 `specDigest` 只经过本地 Go 检查，尚未重新跑云端代理完整链路；详见[任务验收边界](acceptance.md)。

随后发现 Turnyard 曾把内部任务 ID 硬编码为 Git 提交标题。Codex 在 PR 分支上对 11 个提交做了**仅修改消息的历史整理**：逐项保留原文件树与顺序，当前 PR HEAD 为 `9c68e92764d7729f5372212ee1ac904ae907dff0`，原 HEAD `040062f10f7130dcbbb7192d28357750bde14a5d` 保存在私有备份分支 `archive/turnyard-auth-before-commit-titles`，本机映射记录为 `.turnyard/auth-validation-20260926/history-rewrite-map.json`。新 HEAD 的 GitHub Actions [push 检查](https://github.com/rwasayc/turnyard-auth/actions/runs/36253151118)和[PR 检查](https://github.com/rwasayc/turnyard-auth/actions/runs/36253153703)均通过。上面的 Turnyard 候选 SHA 与调用日志均指向**原始历史**；新 SHA 是 Codex 的历史整理结果，不应算作新的 Turnyard 验证。Turnyard 此后的任务输入可提供 `commitMessage`，任务 ID 改放在提交正文。

尚未证明：Podman 真实链路、Kimi 的 DeepSeek/OpenAI 组合，以及不受信任务在宽泛出站网络下的安全性。Turnyard 的单机会话不支持跨机器或跨运行时迁移；任务分配、跨机器迁移和 PR 合并由外部系统负责。Ent 当前在打开数据库时执行追加式自动建表/迁移；后续对正式数据库结构做破坏性升级前，需要引入可审查的版本化迁移。聊天室验收只验证加入用户名后的消息路由，不证明身份认证或生产级私聊保密性。镜像与依赖为固定的已验证版本，固定版本不表示永远最新。

统一产出清单的新增 Go 测试覆盖：旧文件格式兼容、短文本与 PNG 进入会话完成清单、缺少 PR 读回时阻止完成、错误 PR head 被拒绝、冻结的声明在复验时继续使用。使用已授权的 `gh` 对私有 `rwasayc/turnyard-auth` PR #1 做了只读真实读回，确认 URL、base、head 与状态。Docker + OpenCode + Ollama Cloud `glm-5.3-flash` 的新增真实链路也通过：任务同时产出 Git 文件与短文本，两项均为 `present`，会话完成清单包含它们；证据在 `.turnyard/output-go-20260927-012758-ba13e14807908942/evidence.json`。首次尝试将声明写入 `/state` 时被 OpenCode 的目录权限拒绝，任务按 `DELIVERABLE_MISSING` 失败；改用与 Git 仓库隔离的专用 `/workspace/.turnyard-output` 挂载后重跑成功。此链路不覆盖自动 PR 创建。

附件与非 Git 交付的新增 Go 测试覆盖本机文件快照、连接器下载、快照篡改拦截、无仓库文件封存、外部上传响应丢失后的读回恢复、连接器下载超限时中断与临时文件清理，以及 HTTPS 跳转域名拦截。真实 Docker + OpenCode + Ollama Cloud `glm-5.3-flash` 链路使用无仓库会话：代理读入附件后生成包含附件编号的文本文件，结果经 SHA-256 封存，可信检查在只读挂载的封存文件上通过，任务和会话完成；最新证据在 `.turnyard/artifact-go-20260927-095751-81aec3c1a20a316d/evidence.json`。连接器读写目前用独立 Go 测试进程模拟了对象存储的“已存在则跳过”语义，**尚未对真实 OSS bucket 上传/下载**。连接器下载期间采用定时大小监控并中止超限进程，不能将其视为文件系统级硬配额。真实图片视觉理解、PDF 生成质量和默认网络隔离也未由这次验收证明。

通用可执行工具桥接器加入后，用普通 shell 脚本而非 MCP 服务作工具源，真实 Docker 代理必须通过 `stamp_cli.run` 调用它，将返回的 `EXEC_WITNESS:alpha` 写入候选仓库，并通过独立 `grep` 检查。后续把脚本改为必须获得环境配置声明的变量，再对四种运行时复验：OpenCode、Kimi、Claude、Codex 均通过，原生事件记录了工具调用。最终镜像构建后的 OpenCode 证据为 `.turnyard/executable-go-20260927-104353-f930caa05eabac4d/evidence.json`；其余三种运行时的证据依次位于 `.turnyard/executable-go-20260927-103817-d6d28b9c6d48e4ed/evidence.json`、`.turnyard/executable-go-20260927-103841-1a9a1503c396dc2b/evidence.json`、`.turnyard/executable-go-20260927-103901-c19015e69562060a/evidence.json`。OpenCode 首次复跑遇到容器证书校验错误，在工具调用前被标记为 `unknown / AGENT_FAILED`，证据位于 `.turnyard/executable-go-20260927-103726-fd224691b8171169/evidence.json`，不计为通过。原有 Skill/MCP 路径在重构后也由 Docker + OpenCode 重跑通过，证据为 `.turnyard/injection-go-20260927-103506-4e120f21afa76064/evidence.json`。桥接器单元测试覆盖参数不经 shell 解释、非零退出、超时中止和凭据过滤；尚未测试真实 OSS CLI 或外部写入工具的幂等行为。

模型提供方目录与可选 Git 策略改动后，`go test ./...`、`go vet ./...` 通过。无仓库会话省略 `git` 的契约与执行器测试通过；自定义 HTTPS 提供方只通过配置校验测试，尚未调用真实服务。真实 Docker 可执行工具链路再次通过：OpenCode + Ollama Cloud（`.turnyard/executable-go-20260927-112242-f9e026cf7d88424f/evidence.json`）、Kimi + Ollama Cloud（`.turnyard/executable-go-20260927-112348-43c654bde5507a3e/evidence.json`）、OpenCode + DeepSeek（`.turnyard/executable-go-20260927-112434-99d13dabee4f5f1d/evidence.json`）、Claude + DeepSeek（`.turnyard/executable-go-20260927-112711-a4410c2315711bf7/evidence.json`）及 Codex + DeepSeek（`.turnyard/executable-go-20260927-112732-4430af0e1d4778fc/evidence.json`）。每次任务均为 `verified`，但这些是单任务工具路径，不替代完整多任务回归。省略 `git` 的无仓库附件→文件交付也在真实 Docker + OpenCode + Ollama Cloud 下通过，证据为 `.turnyard/artifact-go-20260927-113039-11362e49d2470583/evidence.json`。

受管委派的 Go 测试覆盖：只允许配置的子代理、仓库范围不扩大、父工作区有未提交改动时拒绝、独立子会话、候选验证、只读交接、同键重放及同键异内容冲突；还覆盖宿主路径符号链接、FIFO 请求、Git 与非 Git 交接命名冲突、确定性交接损坏后的原子任务重试、瞬时导出错误后的同候选重查。代理的人工输入标记解析覆盖了“无需输入”的误报和正常疑问。当轮代码通过 `go test -race ./...`、`go vet ./...`、`go run honnef.co/go/tools/cmd/staticcheck@latest ./...` 和 `git diff --check`。真实 Docker + Ollama Cloud `glm-5.3-flash` 路径以 OpenCode 为主代理、Kimi 为子代理：主代理通过 `turnyard_delegate` 提交并查询子任务，子任务在独立 feature 分支产出并通过检查，主代理读取只读交接文件后集成，父子任务均为 `verified`，会话完成。最终证据为 `.turnyard/delegation-go-20260927-131738-617f0a7c8ae7139e/evidence.json`；SQLite 与原生调用日志分别记录了 OpenCode 和 Kimi 各一次调用，父子候选的产物 SHA-256 相同而 commit 不同。首次真实运行因 OpenCode 拒绝读取工作区外的交接目录而失败；对该只读目录明确授权后通过。第二次运行曾因 Kimi 把“无需输入”写成特殊标记而多了一次续接；收紧解析后的最终复验只有一次 Kimi 调用。此轮仅覆盖 OpenCode → Kimi 的一条受管委派链路。

2026-09-27 扩展验证使用 Docker `turnyard-agent:0.3.2` 与 `model-only` 网络。四条真实主子代理链路均完成会话，父子候选均为 `verified`：Codex + DeepSeek → Kimi + Ollama Cloud（`.turnyard/delegation-go-20260927-145504-cfccfc0732f6ce93/evidence.json`）、Claude Code + DeepSeek → Kimi + Ollama Cloud（`.turnyard/delegation-go-20260927-145624-9fe6ecfd69bf442d/evidence.json`）、OpenCode + Ollama Cloud → Codex + DeepSeek（`.turnyard/delegation-go-20260927-145952-1b4c5531889bd550/evidence.json`）、OpenCode + Ollama Cloud → Claude Code + DeepSeek（`.turnyard/delegation-go-20260927-150056-fa6af33036fd2dcc/evidence.json`）。这里验证的是指定运行时与模型组合、受管委派和候选检查，不代表所有提供方组合都兼容。

中断恢复试验在子任务进入 `running` 时强制结束 supervisor：重启后父子任务均为 `unknown / INTERRUPTED`，调用专属 Docker 容器和网络已清理；分别显式 `task reconcile`、重试后，父子候选均恢复到 `verified` 并完成会话。证据为 `.turnyard/delegation-recovery-20260927-145815-e364571a4bb43761/evidence.json`。网络隔离的真实 Docker 探针覆盖占位凭据、模型网关可达、直接公网与宿主连接被拒绝、路径转发绕过被拒绝，以及默认 bridge 上其他容器无法使用网关。严格模式仅为 Docker 实现；宿主管理员可见网关凭据，带凭据的代理侧外部工具会被拒绝，常规网络模式仍有宽泛出站权限。恢复验证仅限同一机器；外部工具的真实副作用未由这些试验证明。

## 2026-09-27 源码与文档核对

当前工作树通过 `go test ./...`、`go test -race ./...`、`go test -tags integration ./...`、`go vet ./...`、Staticcheck v0.8.1 和 `git diff --check`。集成测试未设置真实模型或 OCI 的启用变量，因此此处的 `-tags integration` 结果只证明编译及未跳过的测试通过，**不是新一轮真实代理验收**。`deadcode -test -tags integration ./...` 仅报告 Ent schema 中供代码生成器调用的方法；这类方法不是运行时死代码。本轮移除了只为测试查询首个检查点的存储 API，测试改用任务调用返回的检查点 ID；文档链接与本机历史证据路径均已核对。

单机进程冒烟测试使用临时状态目录和当前源码构建的 CLI：`daemon start`、`daemon status`、`daemon stop` 通过；停止后在线 `session show` 自动重新启动 supervisor，随后再次停止成功。该测试没有运行代理任务。

gVisor 接入新增了 `sandbox.isolation: "gvisor"`、Docker daemon 的 `runsc` 注册检查、代理容器的运行时选择，以及 `model-only` 网关内网地址映射；四个代理驱动已拆到各自的包。单元测试覆盖缺失运行时拒绝、非 Docker 后端拒绝、网关地址缺失拒绝与组装参数。该阶段 macOS Docker Desktop 未注册 `runsc`，尚未运行真实 gVisor 容器或代理链路；后续嵌套 Linux 验证见下文。在已注册 `runsc` 的 Linux 执行机上，先运行 `TURNYARD_INTEGRATION_BACKEND=docker TURNYARD_INTEGRATION_ISOLATION=gvisor go test -run TestRealSandboxFaults -v ./internal/sandbox`，再运行 `TURNYARD_NETWORK_E2E=1 TURNYARD_E2E_ISOLATION=gvisor go test -tags integration -run TestModelOnlyDockerBoundary -v ./internal/sandbox`，最后使用 `TURNYARD_E2E_BACKEND=docker TURNYARD_E2E_ISOLATION=gvisor TURNYARD_E2E_NETWORK=model-only` 执行代理完整链路。

本次修改后，`go test ./...`、`go test -race ./...`、`go test -tags integration ./...`、`go vet ./...`、Staticcheck v0.8.1 与 `git diff --check` 均通过。真实 Docker 的读写隔离、凭据脱敏、超时清理和 `model-only` 网关探针再次通过。拆包后的四个代理也分别完成真实 Docker `model-only` Skill/MCP 任务：OpenCode + Ollama Cloud（`.turnyard/injection-go-20260927-173904-e26ff9c6a0bfa30a/evidence.json`）、Kimi + Ollama Cloud（`.turnyard/injection-go-20260927-173950-06ea2bac72329c98/evidence.json`）、Claude Code + DeepSeek（`.turnyard/injection-go-20260927-174033-f2e25c15dff4ff03/evidence.json`）、Codex CLI + DeepSeek（`.turnyard/injection-go-20260927-174113-fb31bd5e1b99aa76/evidence.json`）。这些结果验证四种驱动在当时 Docker 隔离下的单任务调用、工具与 Skill 注入；后续 gVisor 验证见下文，四条完整多任务链路仍未重跑。

## 2026-09-27 Linux gVisor 与代码整理复验

在 Docker Desktop 的 Linux/arm64 虚拟机内启动独立 Docker 29.2.1 daemon，安装校验过 SHA-512 的 gVisor `runsc` release-20260921.0，并注册为 `runsc` 运行时。`docker run --runtime=runsc hello-world` 成功。Turnyard 的 `TestRealSandboxFaults` 在真实 gVisor 下通过仓库读写、`.git` 与根目录限制、凭据脱敏、超时清理、镜像缺失/漂移和无网络探针；`TestModelOnlyDockerBoundary` 与 `TestModelGatewayCannotBeUsedFromBridge` 验证了模型网关可达、直接出站被拒和普通 bridge 容器无法使用网关。独立环境使用 `node:26-bookworm-slim` 作沙箱探针镜像。

首次 OpenCode 代理任务因 Linux 私有挂载目录由 root 创建、镜像默认 UID 10001 而以 `TOOL_UNAVAILABLE` 结束，底层错误为 `/state/data/opencode/log` 的 `EACCES`。随后让 Docker/Podman 代理容器按 Turnyard 进程的 UID/GID 运行；用 UID 10002（不同于镜像默认 UID）启动 Turnyard，OpenCode + Ollama Cloud `glm-5.3-flash` 的真实 `model-only`、Skill/MCP、候选检查链路通过。保留的证据为 `.turnyard/gvisor-linux-20260927/evidence.json`、`runtime.json` 和 `logs/`。Kimi + Ollama Cloud、Claude Code + DeepSeek、Codex CLI + DeepSeek 的事件结构体重构回归也在普通 Docker `model-only` 下通过，证据依次为 `.turnyard/injection-go-20260927-182856-3ad71df8ca5984f3/evidence.json`、`.turnyard/injection-go-20260927-182947-f1566bee511ed0cb/evidence.json`、`.turnyard/injection-go-20260927-183021-757dc78ef6ade5c8/evidence.json`。

嵌套 daemon 的 cgroup 配置与 Docker Desktop 冲突，本次仅在隔离的 DinD daemon 中给 `runsc` 设置了 `--ignore-cgroups`；因此**未验证 CPU/内存限额由 gVisor 执行**。还需在独立 Linux 主机或 VM 中用标准 cgroup 设置复验限额与完整多任务恢复路径。Podman 的 `keep-id` 身份方案已有参数测试，尚未用真实 Podman 验证。当前代码的 `go test ./...`、`go test -race ./...`、`go test -tags integration ./...`、`go vet ./...`、Staticcheck 与 `git diff --check` 已通过；集成标签全量测试中未设置真实代理开关的案例仍是跳过。

## 2026-09-28 统一工具网关验证

四种代理驱动现在都只配置一个 Turnyard MCP 入口，由它提供 `find_tools` 和 `call_tool`；授权目录按代理区分。单元测试覆盖目录搜索、参数 Schema 校验、未授权 ID 拒绝、恢复后的目录漂移、目录锁在代理检查点外保留，以及模型排序返回陌生 ID 时的过滤。可选模型排序还用真实 Ollama Cloud `glm-5.3-flash` 通过 `TestRealModelRanksGrantedTool` 验证。Docker 探针 `TestToolGatewayKeepsCredentialOutsideAgent` 通过：代理拿不到工具密钥或目录锁，工具拿不到代理原生状态，未授权调用被拒，代理不能直接访问公网。

真实 Docker `model-only` 链路中，OpenCode + Ollama Cloud 的普通可执行工具任务通过，且网关日志明确记录了模型辅助搜索和 `stamp_cli/run` 调用：`.turnyard/executable-go-20260928-001814-eec825a201d9b7aa/evidence.json`。原生 MCP 加 Skill 的任务通过：`.turnyard/injection-go-20260928-000600-c83414bc4a240795/evidence.json`。OpenCode → Kimi 的受管委派及父子候选检查通过：`.turnyard/delegation-go-20260928-001419-c32525211578c1b3/evidence.json`。强制中断 supervisor 后，父子任务先成为 `unknown`，没有遗留调用专属 Docker 容器和网络；显式核查、重试后两者均为 `verified`：`.turnyard/delegation-recovery-20260928-001615-f9010cb8a55d9f4b/evidence.json`。

以上验收使用固定的本机镜像 `turnyard-agent:tool-gateway-dev`。早期四种运行时的可执行工具单任务证据分别在 `.turnyard/executable-go-20260927-233347-15969615d80a8072/`、`.turnyard/executable-go-20260927-233437-7c78a8d9fa149478/`、`.turnyard/executable-go-20260927-233552-136714061f5075c1/`、`.turnyard/executable-go-20260927-233617-8768d0076adfb1af/`，但它们早于最后的工具状态隔离改动，不能当作四种运行时对最终镜像的完整回归。当时尚未用最终网关验证 gVisor、Apple `container`、Podman、真实 OSS 工具外部写入及逐工具出站域名策略；本轮真实模型排序覆盖一条 Ollama Cloud 路径。前三个后端的后续复验见下节。


## 2026-09-28 最终网关跨沙箱复验

Apple `container` 1.4.1 构建并运行了当前 `Containerfile` 镜像 `turnyard-agent:gateway-apple-20260928`。OpenCode + Ollama Cloud `glm-5.3-flash` 的可执行工具任务通过，`find_tools`/`call_tool` 的调用和已验证候选见 `.turnyard/executable-go-20260928-010616-10f739468b3885db/evidence.json`。Skill 加原生 MCP 任务也通过，见 `.turnyard/injection-go-20260928-010655-18065d3908688f89/evidence.json`。这两条使用 `network: default` 和同容器网关，不能证明 Docker `model-only` 的密钥及出站边界在 Apple 后端同样成立。

在 Docker Desktop Linux/arm64 内的独立 Docker 29.2.1 daemon 中，以校验过 SHA-512 的 `runsc` release-20260921.0 运行最终镜像。`--runtime=runsc` 的容器启动通过，`dmesg` 显示 gVisor。Turnyard 以 UID 10002、Docker socket 所属组运行；首次未加入该组时，会话创建因无权读取 Docker runtime 列表而拒绝。修正测试进程的组权限后，真实 `model-only` 可执行工具和 Skill/MCP 任务均产生已验证候选，证据分别在 `.turnyard/gvisor-gateway-retest-20260928/executable/evidence.json` 和 `injection/evidence.json`，运行参数见同目录 `runtime.json`。代理、工具网关与模型网关容器在任务结束后均已清理。本次独立 daemon 仍需 `runsc --ignore-cgroups`，没有验证 gVisor 对 CPU/内存限额的执行。

Podman 5.8.7 的官方容器镜像在独立 Linux/arm64 容器中以 VFS 存储及 rootful 模式执行最终镜像。可执行工具与 Skill/MCP 两条真实 OpenCode + 云模型任务均通过检查并形成已验证候选，证据在 `.turnyard/podman-gateway-retest-20260928/executable/evidence.json` 和 `injection/evidence.json`。首次复现发现 rootful Podman 使用 `--userns=keep-id` 时嵌套环境无法挂载 `sysfs`；现已让 rootful 路径直接使用 UID/GID，非 root 路径仍使用 `keep-id`。该嵌套环境的 overlay 无法启动容器，改用 VFS；私有网络被 nftables 限制，任务沿用 `network: default` 并使用嵌套环境的 host 网络。因此这组结果只验证 Podman 后端的任务、工具和产物链路，不证明私有网络隔离。rootless + VFS + `keep-id` 的容器冒烟测试通过，进程确以 UID 10002 运行；完整任务在检查前因嵌套 Docker 未向非 root 用户委派 cgroup 控制器而以 `TOOL_UNAVAILABLE` 结束，底层错误是创建 `/sys/fs/cgroup/...` 被拒绝，证据见 `.turnyard/podman-gateway-retest-20260928/rootless-failed/evidence.json`。这不能算 rootless 完整任务通过，也不能通过省略 CPU/内存限制掩盖。另在 macOS 安装了 Podman 6.1.2，但 AppleHV 虚拟机进入 emergency mode，日志显示根文件系统 UUID 缺失；libkrun 方案缺少 `krunkit`，均未被算作通过。

在这次跨沙箱复验结束时，真实 OSS 写入仍待提供允许测试的 bucket/前缀。本机阿里云配置能认证，但配置的 Region 为 `oss-cn-guangzhou`，OSS CLI 拼出了错误域名；临时指定 `cn-guangzhou` 后，请求到达服务端，却因无 `oss:ListBuckets` 权限不能枚举可用桶。尚未进行真实上传、读回校验或删除测试对象；外部写入能力仍只有本地模拟连接器的验收证据。已增加需显式设置 `TURNYARD_OSS_E2E=1`、`TURNYARD_OSS_E2E_PREFIX` 和 `TURNYARD_OSS_E2E_REGION` 的 `TestRealOSSConnector`；它将执行真实任务、上传、双重读回比对和测试对象清理，但目前只验证了编译与跳过路径，不能据此宣称 OSS 通过。

## 2026-09-28 容量、OSS 与标准 cgroup 复验

本轮加入单机任务并发与资源预算：默认最多两个活动任务，同一会话仍串行；允许受管委派的主任务预留子任务位置。Go 测试覆盖排队、释放、超限、超时与父子容量死锁，真实 Docker `model-only` 下的 OpenCode → Kimi 委派再次完成，父子候选均已验证，证据为 `.turnyard/delegation-go-20260928-093252-5ee3e4101804e8fc/evidence.json`。状态目录启动时及每十分钟计量，超过可配置阈值只告警，不提供硬配额或自动清理。GitHub CI 新增真实 Docker 边界作业；本机等价探针通过，远端 CI 结果以推送后实际记录为准。

在操作者提供的 OSS 测试桶专用前缀执行 `TestRealOSSConnector`：真实 OpenCode + Ollama Cloud 任务通过可信检查，宿主连接器上传文件并读回校验，独立 `aliyun ossutil cp` 再次读回，SHA-256 一致。证据在 `.turnyard/oss-go-20260928-091648-53b5a0ffd989c6aa/evidence.json`。清理该精确测试对象时服务端返回 `AccessDenied`，因此**整项 E2E 测试失败，不能算完整通过**；测试对象仍需有删除权限的账号清理；完整 URI 留在本机证据中。当前 RAM 用户也无 `ram:CreateUser` 权限，未创建 Turnyard 专用密钥。后续只给专用前缀的上传、读取、删除权限并重新运行完整测试；不要复用全桶或管理员密钥。

另一套 Docker Desktop Linux/arm64 内的 Docker 29.2.1 daemon 以标准 cgroup v2 注册同一 `runsc` release，**没有**使用 `--ignore-cgroups`。直接运行的 gVisor 容器请求 `--cpus=0.5 --memory=128m` 后，cgroup 显示 `cpu.max=50000 100000`、`memory.max=134217728`，忙循环使 `nr_throttled` 从 0 增至 21，记录在 `.turnyard/gvisor-normal-cgroup-20260928/limits.json`。同一 daemon 中，真实 OpenCode + Ollama Cloud 无仓库附件任务完成，生成文件经检查和会话完成，证据为 `.turnyard/artifact-go-20260928-014343-d99826c68f18409d/evidence.json`。 标准 cgroup 下的 `TestRealSandboxFaults`、`TestModelOnlyDockerBoundary`、`TestModelGatewayCannotBeUsedFromBridge` 与 `TestToolGatewayKeepsCredentialOutsideAgent` 也通过，分别检查内网模型访问、代理直接出站阻断、普通 bridge 容器无法调用模型网关，以及工具密钥与代理状态隔离。这验证嵌套 Linux 标准 cgroup 与一条完整任务链路；本轮 Turnyard 进程在嵌套 daemon 内以 root 运行，以便写入 macOS 挂载目录。此前的非 root 任务使用了 `--ignore-cgroups`，两组证据不能合并成单一的生产部署姿态；部署主机仍需单独验收。再次尝试 gVisor 主子委派时，两次都在父代理调用云模型阶段返回 `Cannot connect to API`，没有到达子任务，分别见 `.turnyard/delegation-go-20260928-014447-7ebf229ea1e2c911/` 和 `.turnyard/delegation-go-20260928-014715-26a6809e0a3e5ac8/`。它们不能作为 gVisor 委派通过证据。

超时清理的第一次标准 cgroup 探针虽然返回通过，事后却发现 Docker 留下一个 `created` 容器。为此加入迟到容器检查，并在超时或取消后等待 CLI 退出、重复确认与删除。新检查最初复现了该泄漏；调整等待顺序后，`TestRealSandboxFaults` 再次通过，且随后 `docker ps -a` 无残留。先前的表面通过不计为清理验收。

本轮当前工作树通过 `go test ./...`、`go test -race ./...`、`go vet ./...`、Staticcheck v0.8.1、Govulncheck v1.8.0 和 Ent 再生；容器镜像从固定的 Go 1.27.1、Node 26 基础摘要构建成功。`go test -tags integration -run '^$' ./test/...` 仅证明可编译，不计入真实代理、OSS 或 OCI 验收。
