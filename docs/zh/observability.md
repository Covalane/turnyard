# 错误与可观测性

[架构](architecture.md) · [English](../en/observability.md)

Turnyard 把**机器可判断的错误代码**与给人看的消息分开。错误代码集中在 `internal/fault`。`fault.New/Wrap` 记录错误位置；`Wrap` 保留底层原因供 `errors.Is/As` 追查。跨执行阶段用 `fault.At` 增加操作名；未分类错误在请求或后台任务边界用 `fault.Ensure` 补上 `INTERNAL_ERROR` 和位置。`observe.LogFailure` 统一记录代码、位置、操作链、最底层错误类型，以及适用时的进程退出码、系统错误号和取消/超时标记；不把底层错误正文或代理输出写入操作日志。调用失败与发布失败仍可记录使用固定消息捕获的堆栈。SQLite 事件保存任务结果和代码，不保存完整 Go 错误链。

RPC 响应包含 `request_id`；主任务的关联 ID 随 `context.Context` 进入执行层、代理与沙箱。独立的托管子任务使用自己的任务 ID。JSON 操作日志可关联请求、会话、任务和调用 ID。容器清理失败会保留删除命令的退出原因，并记录涉及的调用和阶段；连接器交付结果为 `unverified` 时，导致这一结果的可捕获操作错误也会写入本机日志。原始错误链只存在于当前进程，跨 RPC 后需要使用 `request_id` 和任务 ID 查日志。代理输出、任务正文和密钥不进入操作日志。

日志的 `sites` 字段按错误包装顺序列出各层源码位置，因此可以从恢复入口继续追到具体的沙箱或存储故障点；`operations` 列出对应的操作阶段。镜像身份检查保留容器命令的失败原因；只有命令明确报告镜像不存在时才归类为 `IMAGE_MISSING`，容器命令或守护进程故障归类为 `IMAGE_INSPECT_FAILED`。

新增错误路径遵循四条规则：业务判定使用 `fault.New`；系统调用或第三方错误使用 `fault.Wrap` 保留 cause；跨子系统时用 `fault.At` 标出阶段；请求与后台任务出口用 `fault.Ensure` 和 `observe.LogFailure` 统一记录。只有明确不影响结果的尽力清理才可忽略返回值；清理是否成功会影响任务状态时，必须确认外部资源已消失，否则保留 `unknown`/`unverified`。

`context` 控制同步请求、Git 克隆、容器进程和检查的取消与期限。异步任务在请求返回后保留关联 ID，并按任务自己的执行期限继续；取消中的容器清理使用独立的有界上下文，以便确认清理结果。对外部输入的某些底层错误会有意隐藏，例如下载已签名 URL 失败时不会把完整 URL 放入 CLI 错误；日志仍保存不含正文的错误类型和位置。

这套实现提供本机可追查记录，**目前没有导出到 OpenTelemetry 或 Sentry**；引入错误库不会自动发送错误报告。后续可在 `observe` 边界增加 OpenTelemetry span 与 OTLP exporter；记录错误时要同时设置 span 的错误状态。若需要集中错误告警，可选接 Sentry Go SDK，但应先明确数据脱敏、采样和部署配置。选型参考：[OpenTelemetry Go 状态及导出器](https://opentelemetry.io/docs/languages/go/)、[OpenTelemetry Go 错误记录与上下文](https://opentelemetry.io/docs/languages/go/instrumentation/)、[Sentry Go SDK](https://github.com/getsentry/sentry-go)、[CockroachDB 的错误链与安全详情做法](https://github.com/cockroachdb/errors)。
