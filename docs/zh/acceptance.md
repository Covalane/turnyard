# 任务验收的边界

[使用指南](README.md) · [English](../en/acceptance.md)

Turnyard 对固定的 Candidate 运行 `Work.checks` 引用的命令，并核对 `Work.deliverables` 声明的产物。Git 文件按候选 commit 读取；非 Git 文件按封存副本读取；图片另检查文件头，短文本检查格式与摘要，PR 则读回远端状态和候选 head。任务至少需要一项检查或产物，所有声明的机器条件通过后才会标记为 `verified`。`Work.acceptance` 是交给代理与审阅者的目标文字，当前不会被逐条自动判定。通过编译、测试、产物存在或远端 PR 读回，也不能单独证明业务目标已经实现。

## 独立验收代码

为了避免代理修改产品代码的同时修改对应测试，可把验收程序放在另一个固定 commit 的 Git 仓库中，在 `Session.repositories` 中登记，并在任务里将它设为 `read`。产品仓库设为 `write`。例如：

```json
{
  "scope": {
    "repositories": [
      {"id": "product", "mode": "write"},
      {"id": "oracle", "mode": "read"}
    ]
  },
  "checks": ["independent-acceptance"]
}
```

在可信的 `Environment.checks` 中定义命令，让它读取候选产品仓库并执行验收仓库中的程序：

```json
{
  "id": "independent-acceptance",
  "argv": ["/workspace/oracle/verify", "/workspace/product"],
  "repositories": ["product", "oracle"],
  "timeoutSeconds": 600
}
```

运行代理时，`oracle` 只读；运行检查时，列出的仓库均以只读方式挂载。验收仓库的固定 commit 会进入会话的仓库版本向量；检查记录包含检查 ID、定义摘要 `specDigest`、退出码、超时状态及原始日志路径。验收程序应通过公开接口或最终构建产物验证行为，覆盖正常流程、拒绝路径和必要的故障场景。

验收仓库应由任务提交者或审阅者维护，并在代理执行前固定版本。即使这种检查通过，未编码成断言的需求仍需要人工审阅；真实第三方服务、浏览器环境、部署与合并状态也需要分别验证。若把所有测试都放在代理可写的产品仓库，`go test` 通过只能说明该候选版本中的测试通过，不能算独立验收。
