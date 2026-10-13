# gobase 第二轮：临时目录阻止可信 Go 测试

## 观察与证据

第二轮固定 Turnyard `5933dcdab11f8d23455d8b08cb60c96b498a2158`，从空 Git 和新状态开始。OpenCode + Kimi K3 完成了首批 set 源码/测试、API 草案和结构化人工输入；负责人回复后沿同一原生会话修改。另一只读产品快照的 Claude + DeepSeek 验收任务也启动，但在本轮作废后由操作员停止，不能计为通过。

首任务的可信 `go test -race -count=1 ./...` 检查成功编译测试二进制，然后失败：

```text
fork/exec /tmp/go-build166702978/b003/set.test: permission denied
FAIL github.com/Covalane/gobase/set 0.000s
```

Vet、Staticcheck、格式检查通过，但任务正确进入 `failed / CHECK_FAILED`，没有被宣告为 verified。这次失败没有证明单元测试实际运行；不能以代理自己的成功描述覆盖可信检查结果。

## 原因与修复

共用 OCI 命令用未带选项的 `--tmpfs /tmp`，Docker 的默认 noexec 阻止标准编译器在该目录执行生成的程序。代理可以手工改用输出目录进行自测，可信检查的只读仓库与封存产物则不应被当成可写构建区。

临时挂载参数现由 Dialect 明确提供。Docker/Podman 的任务和工具容器使用私有可执行 tmpfs，同时保留 nosuid/nodev 与内存大小上界：最多 1 GiB，且按任务声明内存的一半计算。这不代表所有 sidecar 已有同等 CPU/内存硬限制。Apple `container` 保留其原有参数形式，避免把 Docker 的选项语法传入不同解析器；本轮没有宣称 Apple 的真实编译验收通过。模型凭据网关保持原行为。

## 验证边界

- 真实 Docker 回归在只读 Go 仓库中执行标准 `go test`，未设置 GOTMPDIR 或额外可写代码挂载。测试二进制成功执行，产品目录写入仍被拒绝。
- 工具网关回归实际在工具进程中复制并执行临时二进制，保持授权拒绝、模型出站与工具凭据隔离检查。
- 内存容量参数与 Apple 参数形式有默认回归；源码静态门禁及其他 Docker 边界另外复验。
- 新的真实编译回归加入 CI。CI 的基础代理镜像不含 GCC，因此该独立回归使用标准纯 Go 测试；gobase 的竞态测试镜像另预装 GCC。
- 最终源码的全库竞态测试、Vet、Staticcheck、带 integration 标签的编译与 Vet、差异格式检查通过；真实 Docker 挂载/权限/脱敏/超时/断网探针与三项网关边界检查通过。

这轮没有取得可接受的完整工具库。库产物与验收产物都保留为失败证据；修复冻结后第三轮仍从新的空 Git 与状态重新开发。仅需求及负责人制定的语义决策可以带入下一轮，不导入任何生成的产品、测试或 API 文档。

## English summary

The second attempt used Turnyard `5933dcd` and reached candidate verification. The trusted Go check compiled its test binary but failed to execute it from Docker's default noexec tmpfs. Turnyard correctly reported CHECK_FAILED. Dialects now provide explicit temporary-mount policy: Docker/Podman task and tool containers permit compiler execution while retaining nosuid/nodev and a bounded tmpfs size. Apple syntax remains unchanged and was not retested. A real readonly Go-test regression and tool-process temporary-execution check pass locally; the Go regression is added to CI. The attempt is invalidated, and development restarts from empty repositories after freezing the repair.
