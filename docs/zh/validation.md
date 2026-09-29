# 验证范围与复现

[使用指南](README.md) · [English](../en/validation.md)

本文汇总已验证的行为、失败结果和复现入口。真实模型、OCI 与外部服务测试需要操作者提供镜像、凭据和相应权限；默认的 Go 测试通过不代表这些可选链路已运行。

历史验收发生于 2026-09-26 至 29 日，覆盖 macOS Apple Silicon、Docker Desktop 内的 Linux/arm64、Apple `container` 和 Podman 等环境。不同记录对应不同源码与镜像版本，不能合并为一次发布验收。原始调用日志、临时数据库和模型输出保存在执行机的被忽略状态目录，**未随开源仓库发布，因此读者不能仅凭本文独立审计那些历史运行**。

## 已验证的路径

| 路径 | 历史结果与边界 |
| --- | --- |
| Go 测试与公开 CI | 单元测试、race、Vet、Staticcheck、Govulncheck、Ent 再生成和 Linux x64 镜像构建曾通过；公开 CI 不调用云模型。 |
| 多仓库执行 | Docker 与 Apple `container` 的 OpenCode 云模型链路完成两个仓库、四个连续任务；包含 feature commit、原生会话续接、人工回复、检查点恢复与交付清单。 |
| 代理与能力注入 | OpenCode、Kimi、Claude Code、Codex CLI 的 Skill/MCP 和可执行工具单任务路径曾通过；原生事件显示工具调用。单任务结果不等同四种代理的完整多任务矩阵。 |
| 受管委派 | Docker `model-only` 下验证了主代理到子代理的任务、候选检查和只读交接；还验证 supervisor 中断后的 `unknown`、显式核查与重试。恢复只在同一机器验证。 |
| 远端 Git | 临时裸仓库测试覆盖固定提交克隆、发布幂等、冲突和部分成功。一次私人 GitHub 仓库验收完成九个授权系统任务并发布 feature 分支，远端读回一致；私人仓库、PR 地址和提交 ID 不属于本项目的公开接口或测试默认值。 |
| 统一产出与附件 | Go 测试覆盖文本、PNG、Git 文件、非 Git 文件封存、PR 读回、连接器下载和上传结果核查。真实 Docker 链路验证 Git 文件加文本，以及无仓库附件到文件交付。 |
| 工具网关 | Docker `model-only` 下验证 `find_tools`、`call_tool`、模型辅助搜索、权限拒绝及代理与工具凭据隔离；Apple `container`、Podman 和 gVisor 有各自的单任务复验。 |
| 结构化人工输入 | Docker + OpenCode 链路通过 `turnyard_input/request_input` 进入 `needs_input`，人工回复后沿同一原生会话完成双仓库任务；其他代理与沙箱组合未在该改动后逐一复验。 |

历史远端 Git 验收中，一次云模型用量上限 429 被记录为 `unknown / AGENT_FAILED`；检查点核查、更换可用凭据和同任务重试后才成功。失败调用没有计入通过。两个相关会话共有九个已验证任务、12 条调用记录、122 条生命周期事件；其中一次 supervisor 中断没有原始调用日志，所以不存在每次调用都完整的逐步记录。云服务端内部推理也不在日志中。后来由 Codex 直接修改私人验证项目及整理提交消息的工作，均不计入 Turnyard 验证任务。

## 沙箱和外部服务的限制

- Docker `model-only` 的真实探针覆盖模型网关可达、代理直接出站被拒、普通 bridge 容器不能使用网关、凭据脱敏和超时清理。普通网络模式仍允许较宽的出站连接，宿主管理员可以查看网关凭据。
- gVisor 在嵌套 Linux 的标准 cgroup v2 下完成资源限额、边界探针和一条云模型任务；同配置的主子委派两次在父代理连接模型时中断，**没有通过**。此前使用 `--ignore-cgroups` 的非 root 运行不能与标准 cgroup 验证合并为单一部署证明。
- Apple `container` 的工具网关单任务通过；该运行使用默认网络，不证明 Docker 严格网络边界。Podman 的 rootful + VFS 单任务通过；rootless 容器冒烟通过，完整任务因嵌套环境未委派 cgroup 控制器失败。macOS Podman 虚拟机启动失败，未算作通过。
- 真实 OSS 测试完成上传及两次 SHA-256 一致的读回，但删除测试对象返回 `AccessDenied`；**完整 E2E 未通过，测试对象仍待有权限的账号清理**。未创建专用 RAM 密钥。当前结果不证明真实外部写入工具的幂等性。
- 当时的私有仓库验证证明克隆和 feature 分支推送，不证明受保护分支、PR 合并或所有远端超时场景。聊天案例只验证消息路由，不证明身份认证或生产级私聊保密性。

## 复现

基础检查无需云模型：

```sh
go generate ./internal/store
go test -race ./...
go vet ./...
go test -tags integration -run '^$' ./test/...
```

最后一条只验证带 `integration` 标签的测试能够编译，**不代表真实代理、OCI 或 OSS 验收通过**。

真实测试由开关显式启用。先设置 `TURNYARD_E2E_PROVIDER`、`TURNYARD_E2E_MODEL`，并将 `TURNYARD_E2E_CREDENTIAL_ENV` 设为自己已有的凭据环境变量名，再运行：

```sh
TURNYARD_E2E=1 go test -tags integration -run TestMultiRepoWorkflow -v -count=1 -timeout 25m ./test/e2e
TURNYARD_DELEGATION_E2E=1 TURNYARD_E2E_BACKEND=docker go test -tags integration -run TestManagedDelegation -v -count=1 -timeout 15m ./test/e2e
TURNYARD_DELEGATION_RECOVERY_E2E=1 TURNYARD_E2E_BACKEND=docker go test -tags integration -run TestDelegationCrashRecovery -v -count=1 -timeout 15m ./test/e2e
```

可用 `TURNYARD_E2E_RUNTIME`、`TURNYARD_E2E_BACKEND` 和 `TURNYARD_E2E_IMAGE` 选择执行路径；恢复测试还必须设置子代理的 `TURNYARD_DELEGATION_CHILD_PROVIDER`、`TURNYARD_DELEGATION_CHILD_MODEL` 和 `TURNYARD_DELEGATION_CHILD_CREDENTIAL_ENV`。聊天室测试需要 `TURNYARD_CHAT_PROVIDER`、`TURNYARD_CHAT_MODEL` 和 `TURNYARD_CHAT_CREDENTIAL_ENV`；模型排序测试需要 `TURNYARD_MATCHER_ENDPOINT`、`TURNYARD_MATCHER_MODEL` 和 `TURNYARD_MATCHER_CREDENTIAL_ENV`；PR 读回需要 `TURNYARD_GITHUB_PR_URL` 与 `TURNYARD_GITHUB_REPOSITORY_URL`。操作者应先准备沙箱镜像和可用权限，测试失败时保留失败状态，不把跳过计为通过。
