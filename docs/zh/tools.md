# 代理能力与工具注入

[使用指南](README.md) · [English](../en/tools.md)

Turnyard 把能力分为代理运行时、Skill、工具、沙箱资源和宿主侧操作。`Environment` 登记可用的 Skill 与工具，`agents[].skills` 和 `agents[].tools` 选择要交给某个代理的部分；`Session.primaryAgent` 固定本会话使用的代理。四种运行时只连接一个 Turnyard MCP 工具网关，网关再连接本次代理获准使用的工具。

## 与开源实现的对照

[OpenCode](https://opencode.ai/docs/tools)保留内置 shell/文件工具，同时支持自定义工具与 MCP，并通过代理权限控制工具使用；[ClineCore](https://docs.cline.bot/cline-sdk/sessions)将工具批准回调放在会话运行层；[OpenHands](https://docs.openhands.dev/api-reference/store-settings)允许把不同传输的 MCP 服务加入运行环境。Turnyard 的网关负责**发现、逐工具授权、参数校验和调用路由**，运行时决定何时调用，沙箱负责**文件、进程与网络边界**，可信检查负责**结果验收**。

可执行工具桥接保留任意程序的 `argv` 和标准输入，不要求为每个 CLI 写代理驱动；原生 MCP 保留服务自己的参数 Schema。桥接的通用 `run(args, stdin)` 没有特定业务命令的参数结构，因此复杂工具应优先提供原生 MCP 服务或薄适配器。网关只允许调用 `agents[].tools` 选中的后端，不能禁用运行时自带的 shell 或文件工具；后者仍由沙箱限制。Turnyard 目前没有跨四种运行时一致的逐次人工批准回调。

## 统一网关与按需发现

代理侧的 MCP `tools/list` 只暴露 `find_tools` 和 `call_tool`。代理先查询工具，网关最多返回 8 个匹配项的 ID、说明与参数 Schema；代理再用准确 ID 和参数调用。网关执行前重新确认 ID 属于本代理的授权清单，并按后端 Schema 校验参数。搜索结果最多 64 KiB，调用结果最多 256 KiB；搜索仍会消耗一次模型交互，返回内容仍会进入上下文。

网关启动时会连接本代理获准的后端并读取目录，再按需把命中的定义返回给代理。因此减少的是主代理上下文中的工具定义；已授权后端的启动时间和内存开销仍会产生，尚未实现按搜索结果才启动后端。

默认匹配使用本地关键词，不调用额外模型。可选配置让网关在关键词结果不足或接近时，用指定的 OpenAI Chat 兼容模型为**已授权工具**排序；模型失败则退回本地搜索，模型返回的陌生 ID 会被丢弃。模型不会直接执行工具。当前模型排序最多处理 64 个授权工具，更大的目录仍使用本地搜索。

```json
"toolSearch": {"mode": "llm-rerank", "modelBinding": "search-model"}
```

`search-model` 必须是 `modelBindings` 中支持 OpenAI Chat 接口的一项。省略 `toolSearch` 等同本地关键词模式；`network: "none"` 不接受模型排序。

## 两种工具来源

| `kind` | 配置内容 | 代理看到的能力 |
| --- | --- | --- |
| `mcp` | 已实现 stdio MCP 协议的程序及 `argv` | 网关发现该服务声明的工具，ID 为 `<配置 ID>/<工具名>` |
| `executable` | 普通可执行程序的固定命令前缀、`description` 和可选目录 | 网关暴露 `<配置 ID>/run`；模型提供附加参数和可选文本输入 |

可执行工具的 `argv` 由可信环境配置，例如 `['{toolDir}/stamp.sh']` 或 `['ossutil']`。桥接器使用进程 API 直接启动它，**不会把模型参数拼接成 shell 命令**。`path` 可指向包含程序及依赖文件的目录；Turnyard 锁定目录摘要，并复制到会话的 `/state/tools/<id>`。`{toolDir}` 在配置时替换为这个容器路径。命令也可以来自镜像中预装的程序，此时不需要 `path`。桥接器本身必须包含在代理镜像中，项目的 `Containerfile` 已安装它。缺少程序或桥接器时 MCP 连接或调用会失败，任务不能据此视为通过。

`passEnv` 按名称从宿主环境读取变量；缺少值会在调用前报错。Docker 的 `default` 和 `model-only` 模式把工具进程放在本次调用专属的工具容器，代理容器只运行 MCP 代理程序，收不到工具密钥。`model-only` 下代理仍只连接内部网络，工具容器可按已授予的能力访问外部服务。Apple `container`、Podman 及 `network: "none"` 使用同容器网关，声明的 `passEnv` 对代理进程可见，因此不能把它当作密钥隔离。普通可执行程序的子进程只继承基础路径变量和声明的 `passEnv`。`timeoutSeconds` 限制单次命令运行时间；桥接器还限制输入、参数数量和输出长度。退出码非零会作为工具错误返回。外部写操作超时后结果可能未知，需先查询外部状态再重试。

Docker 工具容器只挂载工具配置、工具包和独立工具状态，不挂载代理原生会话状态；代理也看不到目录摘要锁。工具容器目前没有逐工具的出站域名白名单，具备的网络访问仍需由部署方的网络策略约束。

独立工具状态与目录摘要锁随会话保留，但不会随代理工作区检查点一起回滚。恢复后涉及外部写入的工具调用仍须核查其外部结果，再决定是否重试。

### 已验证的可执行工具

仓库中的 [`examples/executable-witness/stamp.sh`](../../examples/executable-witness/stamp.sh) 是普通脚本，没有实现 MCP。对应环境片段：

```json
{
  "agents": [{"id": "lead", "runtime": "opencode", "modelBinding": "cloud", "tools": ["stamp_cli"]}],
  "tools": [{
    "id": "stamp_cli", "kind": "executable",
    "description": "Generate a witness token from one value.",
    "path": "./examples/executable-witness",
    "argv": ["{toolDir}/stamp.sh"],
    "passEnv": ["TURNYARD_EXEC_WITNESS_MODE"],
    "timeoutSeconds": 30
  }]
}
```

运行前，宿主环境需要提供 `TURNYARD_EXEC_WITNESS_MODE=enabled`。代理先用 `find_tools` 搜索，再调用 `call_tool`，传入 `{"toolId":"stamp_cli/run","arguments":{"args":["alpha"]}}`。桥接器把声明的变量交给脚本，脚本返回 `EXEC_WITNESS:alpha`；代理把它写入仓库，Turnyard 对候选代码运行独立检查。具体真实模型验收情况见[验证记录](validation.md)。这是环境 JSON 的片段；完整文件还需要沙箱和模型绑定。只有包含 Git 仓库的会话才需要 Git 策略。

例如要让代理调用 `ossutil`，可将它安装在镜像中，配置 `kind: "executable"`、`argv: ["ossutil"]`、清楚的 `description` 和所需 `passEnv`。模型会通过 `run.args` 提交子命令。用于**最终文件交付**时，通常应优先使用 [宿主侧交付连接器](lifecycle.md)：由 Turnyard 在检查通过后上传并读回校验，避免把交付是否成功完全交给代理的文字报告。真实 OSS 桶已验证上传与双重读回；测试对象清理因当前账号缺少删除权限而失败，见[验证记录](validation.md)。

## Skill、沙箱与记录

Skill 是操作说明，不能单独提供新程序。镜像自带的 shell、语言工具、允许挂载的仓库，以及沙箱允许的网络仍可被代理使用；`agents[].tools` 不是容器进程的系统调用白名单。需要限制文件或网络访问时，应由沙箱挂载、镜像、网络策略及凭据配置承担。Docker 的 `network: "model-only"` 允许代理经专属网关调用指定云端模型，同时阻止代理直接访问公网或宿主；所选工具通过独立网关使用其凭据。`network: "none"` 完全断网，不能直接调用云端模型。需要可信的最终交付时仍可使用宿主侧连接器。Apple `container` 和 Podman 当前不支持 `model-only`，配置会在运行前被拒绝。

Turnyard 分别记录已配置的 Skill、网关连接情况、原生日志里观察到的 `find_tools`/`call_tool` 调用、网关日志里的实际后端工具 ID，以及候选检查结果。`configuredSkills` 只说明配置，不代表实际使用；Docker 网关日志保存在调用日志旁的 `.gateway.log` 文件。首次启动会将工具目录摘要锁定在会话中、代理状态检查点之外，恢复时定义变化会报错。新增工具来源时扩展网关后端，不需为每个具体 CLI 程序写代理驱动。
