# Go 仓库输入示例

[使用指南](README.md) · [English](../en/local-go.md)

`local-go` 从本机现有 Git 仓库的 HEAD 生成一组可编辑的 Session、Environment 和 Work JSON，不修改来源仓库。在 Turnyard 仓库根目录运行：

```sh
go run ./examples/local-go --repo /path/to/go-repo --out /tmp/turnyard-input \
  --objective '为现有服务增加健康检查，并补充测试' \
  --commit-message 'feat: add health check' --deliverable health.go
go build -o bin/turnyard ./cmd/turnyard
bin/turnyard session create --file /tmp/turnyard-input/session.json
```

将创建响应中的 `session_id` 用于 `task add`，再用返回的 `task_id` 执行 `task run`、`task wait` 和 `task show`。生成后可修改三个 JSON，按[使用指南](README.md)准备沙箱镜像与 API key。每次新建独立需求须使用新的 `idempotency_key`。
