# 生命周期、交付与存储

[使用指南](README.md) · [English](../en/lifecycle.md)

## 一个会话对应一个需求

创建会话时固定仓库起点、环境和主代理。同一需求可以依次追加多个任务；任务可运行、等待人工输入、失败后重试，或针对原候选版本重新检查。计算容器只在调用期间存在。工作区、原生代理状态和检查点让暂停后的任务能够继续。

创建请求需要 `idempotencyKey`。响应丢失或客户端超时后，用同一个键和原始输入重试会取得同一会话；同一键对应不同输入会收到冲突错误。首次准备仓库默认允许 10 分钟，可在 CLI 用 `session create --timeout <秒>` 调整。

| 会话状态 | 含义 | 下一步 |
| --- | --- | --- |
| `ready` | 当前没有活动调用；前一任务完成后可追加任务 | 运行任务、完成或取消 |
| `running` | 正在调用代理 | 等待结果，不接受终态操作 |
| `paused` | 等待人工输入、重试或故障处理 | 回复、复验、重试或取消；`unknown` 先核查并 reconcile |
| `completed` | 全部任务验证通过，需求交付已确认 | 审阅；如显式启用，可发布 feature 分支 |
| `cancelled` | 需求撤销，未完成任务已停止推进 | 只读审阅 |

任务 `verified` 只说明该候选版本通过了机器检查和已声明交付物校验。审阅者确认整个需求完成后，调用 `session complete`。它要求会话处于 `ready`、至少有一个任务、每个任务都指向 `verified` 候选，并检查工作区与最后检查点一致。成功后写入 `completedAt` 和审计事件。需求中途撤销时，操作员用 `session cancel <id> --reason "原因"` 结束 `ready` 或 `paused` 会话；未完成任务标记为 `cancelled`，已有候选保留。取消响应版本为 `turnyard.session-cancellation/v1`。活动调用期间不能完成或取消。重复终态命令返回原时间；终态禁止追加、运行、复验和恢复。已完成会话可按 [Git 工作流](git.md)显式发布已验证 feature 分支；取消的会话不能发布。终态不是自动删除：代码、检查、事件和日志仍留在本机供审阅。

状态为 `unknown` 的任务必须先检查外部副作用并执行 `task reconcile`，才能取消会话。完成或取消后数据不会自动过期；操作员可离线备份完整状态目录，校验后显式清理终态会话。当前没有自动保留期限，备份由使用者自行妥善保存。

## 离线备份、恢复和清理

先停止 supervisor，再执行：

```sh
bin/turnyard daemon stop
bin/turnyard state backup --out /secure/backups/turnyard-2026-09-26
bin/turnyard state verify --from /secure/backups/turnyard-2026-09-26
bin/turnyard session prune <已完成或取消的会话ID> --backup /secure/backups/turnyard-2026-09-26
```

备份是权限受限的目录，包含 SQLite 数据库、工作区、代理状态、日志和检查点；清单记录每个文件的 SHA-256。备份目录必须在状态目录之外。清理前会重新验证备份，并要求当前状态与备份完全一致；清理后删除该会话的数据库记录及文件。每次清理下一个会话前需要重新备份。可用 `state restore --from <备份目录> --to <不存在的目录>` 恢复完整状态；恢复到新目录时会更新数据库中指向状态目录的路径。备份可能包含源代码、任务正文及代理状态，应按敏感数据保管。

## 输入附件与交付物

`Work.inputs` 可携带图片、文档或其他文件的**实际字节**，不是只把链接放进提示词。每项声明 `id`、`source`、可选 `mediaType` 和 `expectedSha256`。`source.kind` 支持：

| 来源 | 字段 | 处理方式 |
| --- | --- | --- |
| `file` | `path` | 从提交任务的机器读取文件；相对路径以 Work JSON 所在目录为基准 |
| `https` | `url` | 只下载 `Environment.inputHosts` 允许的域名；必须提供预期 SHA-256，跳转也检查域名 |
| `connector` | `connector`、`uri` | 用环境预先配置的连接器下载；URI 必须在允许的前缀内，必须提供预期 SHA-256 |

Turnyard 在 `task add` 时复制并校验附件，保存为会话内的只读快照；后续源文件或 URL 变化不影响任务。原始 URL、签名查询参数和本机源路径不写入任务记录。单个附件目前上限为 256 MiB；接收、排队和入库合计默认允许 20 分钟，可用 `task add --timeout <秒>` 调整。代理在 `/workspace/.turnyard-input/<快照目录>/<id>` 读取文件；这保证容器内能访问字节，图片能否被模型当作视觉输入仍取决于所选运行时和模型。

`Work.deliverables` 声明必需产出，文件、图片、文本和 PR 共享 `id`、`kind`、`status`、`verification` 等结果字段：

每个任务至少要声明一项可信检查或一项交付物；二者都为空的任务不能被标记为已验证。

| 类型 | 产出位置 | 校验 |
| --- | --- | --- |
| `file` | `repository` + `path` 指向候选 commit，或不填仓库、由代理写入 `/workspace/.turnyard-output/files/<path>` | Git tree 或封存文件的 SHA-256；文档、压缩包等任意二进制格式使用此类型 |
| `image` | 与文件相同 | 同时检查 PNG/JPEG/GIF/WebP 文件头；其他格式可作为通用 `file` 交付 |
| `text` | 本次调用的 `artifacts.json` 声明 | 非空 UTF-8、64 KiB 限额和 SHA-256；不证明文字结论正确 |
| `pull_request` | 本次调用的 URL 声明 | 托管平台读回仓库、状态、目标分支和候选 commit |

文件或图片的 `destination` 可为 `{"kind":"local"}` 或 `{"kind":"connector","connector":"oss","uri":"oss://bucket/reports/{sha256}.pdf"}`。仓库文件省略 `destination` 时仍留在 Git commit；无仓库文件必须明确给出目的地。`local` 返回会话状态目录中的 `localPath`；`connector` 在检查通过后上传到内容寻址 URI，再下载并核对 SHA-256，结果包含 `uri`。外部上传失败或读回不确定时任务不会通过；`task verify` 可对同一候选重试读回。连接器上传命令应采用“已存在则跳过”的语义，避免覆盖不同内容。

可信检查运行时，封存的文件按产物 `id` 只读挂载在 `/workspace/.turnyard-output/<id>`，因此可以在外部交付前核查 PDF、图片或其他文档内容。通用 `file.mediaType` 是任务声明的元数据；系统目前只对 `image` 的上述格式检查文件头。

例如，一个不需要 Git 仓库的文档任务可在 `Session.repositories` 和 `Work.scope.repositories` 中使用空数组，并声明：

```json
{
  "inputs": [{"id": "brief", "source": {"kind": "file", "path": "brief.png"}, "mediaType": "image/png"}],
  "deliverables": [{"id": "report", "kind": "file", "path": "report.pdf", "mediaType": "application/pdf",
    "destination": {"kind": "connector", "connector": "oss", "uri": "oss://my-bucket/turnyard/{sha256}.pdf"}}]
}
```

环境配置连接器的命令与 URI 边界，任务无法覆盖命令：

```json
{
  "artifactConnectors": [{"id": "oss", "uriPrefix": "oss://my-bucket/turnyard/",
    "getArgv": ["ossutil", "cp", "{uri}", "{file}", "-f"],
    "putArgv": ["ossutil", "cp", "{file}", "{uri}", "--ignore-existing"]}]
}
```

上述片段放入完整的 Environment JSON。连接器在 supervisor 宿主机执行，使用它自己的 OSS 配置；代理无需得到存储凭据。也可以用同一接口配置其他 CLI，但命令须由可信的环境管理员提供，能将一个 URI 与一个文件相互复制。`getArgv`、`putArgv` 分别只能使用一次 `{uri}` 和 `{file}`。`inputHosts` 仅约束 Turnyard 对 HTTPS 附件的下载；当前容器的默认网络仍可访问外网，不能把提示词或未注入某个工具当作完整的网络访问控制。

文本和 PR URL 由代理写入本次调用专属的 `/workspace/.turnyard-output/artifacts.json`，例如：

```json
{"schemaVersion":"turnyard.artifact-claims/v1","artifacts":[{"id":"summary","text":"本次工作的结论。"}]}
```

文件字节按固定 Git 版本或封存副本核对，声明路径本身不能作为通过依据。PR 仍由可替换的验证器读回；默认实现使用宿主机已授权的 GitHub CLI。目前还没有受管的自动 PR 创建流程。`candidate.deliverables` 和 `session complete` 的 `deliveries` 给出产物摘要与位置，调用方据此取得文件或继续交付。产物存在与字节匹配不自动证明业务目标；`acceptance` 和可信检查仍用于验收。

## 单机状态与 SQLite

supervisor 默认最多同时执行两个任务；同一会话仍只能运行一个。超出的任务按到达顺序排队，默认最多等待 15 分钟；父任务正在等待的受管子任务可越过暂时无法准入的普通任务，以免发生容量死锁。等待超时返回 `CAPACITY_WAIT_TIMEOUT`，单个任务声明的资源超过主机预算返回 `CAPACITY_EXCEEDED`。允许委派的主任务会预留一个子任务位置，避免父任务占满全部位置后等待子任务。仓库准备与任务附件接收另有独立准入：默认同时最多两个准备请求、最多八个等待请求；队列满返回 `CAPACITY_EXCEEDED`。`daemon status` 返回任务和准备请求的活动数与等待数，以及已预留的任务资源。宿主可在启动 supervisor 前设置 `TURNYARD_MAX_ACTIVE_TASKS`、`TURNYARD_MAX_ACTIVE_PREPARATIONS`、`TURNYARD_MAX_PENDING_PREPARATIONS`、`TURNYARD_MAX_ACTIVE_CPUS`、`TURNYARD_MAX_ACTIVE_MEMORY_MB` 和 `TURNYARD_QUEUE_WAIT_SECONDS`；CPU 和内存总预算设为 `0` 表示不启用该预算。这里按每个任务声明的沙箱限额做准入，不能代替容器运行时实际执行的限额。

supervisor 启动时清理未被已入库任务引用的附件暂存批次，并在启动时及每十分钟扫描状态目录。`daemon status` 返回 `stateBytes`、`stateWarning`；默认超过 10 GiB 记录一次警告，`TURNYARD_STATE_WARN_MB` 可调整阈值，`0` 关闭警告。这是用量告警，不是硬磁盘配额；结构迁移现状及自动保留期限见[状态升级与保留](state-evolution.md)。

Turnyard 的执行边界是一台机器。数据库保存会话、状态转换、事件及产出清单，短文本产出也会直接存入候选结果；Git 仓库、检查点归档及原始日志存为文件。supervisor 持有状态目录的独占锁，一个进程可持续处理多个会话。SQLite 符合这一单机边界，无需另设数据库服务。会话结束后的记录保留期限由操作者决定，与 supervisor 是否常驻无关。

上层调度系统可以在不同机器上分别运行 Turnyard 实例，并收集其标准化结果；各实例维护自己的状态目录。Turnyard 不负责在机器之间迁移正在执行的会话，也不允许多台机器通过网络文件系统共享同一个 SQLite 文件。

Ent 支持多种数据库方言，但当前 `store.OpenStore`、备份恢复和版本化迁移均依赖 SQLite 的具体行为；更换 DSN 不是本项目单机执行目标的必要步骤。结构升级以编号 SQL 和校验和审查；数据保留与备份策略仍由部署者明确制定。

参考：[SQLite 适用场景](https://www.sqlite.org/whentouse.html) · [SQLite 与网络文件系统](https://www.sqlite.org/useovernet.html)
