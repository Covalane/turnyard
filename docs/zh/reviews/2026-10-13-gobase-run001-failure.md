# gobase 首轮开发中断与修复

## 冻结基线与观察

首轮 Turnyard commit 为 `e7444c40e20507d2604e7d4bb71ae0e1bd120d04`，独立状态目录和空 Git 起点。开发任务仅已提交，尚未取得可验收的产品候选。没有模型调用/usage 证据，不将此次计为真实模型路径通过，也不据此断言费用为零。

自动启动的 supervisor 在启动命令结束后退出。后续查询再次启动 supervisor，将未结束的调用置为 `unknown / INTERRUPTED`，并确认相关容器已停止。恢复状态没有错误地报告任务成功，但用户预期的常驻执行未能持续。

## 已确认原因

`StartDaemon` 调用 `Process.Release`，但没有让子进程脱离启动方的进程组。Unix 下释放 Go 进程句柄不会创建独立会话；终端或任务运行器结束该进程组时，supervisor 也会被终止。

受控复现为启动者建立独立进程组，再运行真实 `daemon start`。旧版本的 supervisor 与启动者具有相同 PGID；向该组发送 SIGTERM 后 supervisor 消失。信号只发给本次复现创建的进程组，没有操作其他服务。

## 修复与验证

- Unix 启动通过 `Setsid` 创建独立进程会话；其他平台没有新增兼容性承诺。
- 默认回归测试构建真实 CLI，启动独立进程组中的启动者，确认 supervisor 已脱离该组，结束启动组后验证原 PID 仍响应 RPC，最后正常停止。
- 定向竞态测试已通过。在实际命令执行环境中分别执行启动与查询，supervisor 保持同一 PID，PPID 为 1，PGID 为自身 PID，随后正常停止。
- `go test -race -count=1 ./...`、`go vet ./...`、`go tool staticcheck ./...` 和差异格式检查均通过；最终测试调整后再次通过定向竞态、Vet 和 Staticcheck。进程分离不等于主机重启恢复、模型推理或业务验收通过。

首轮证据保留。冻结修复后的 Turnyard 后，从新的空 Git 仓库、状态目录和原始需求重新开发 gobase，禁止导入首轮生成产品。这份报告说明执行器缺陷，不作为工具库完成或生产就绪的声明。

## English summary

The initial gobase attempt used Turnyard commit `e7444c4` and did not produce an accepted library candidate. The automatically started supervisor inherited the launcher's process group; terminating that group killed the supervisor. `Process.Release` alone does not detach a Unix process. The fix starts the supervisor in a new session. A real-CLI regression checks survival after launcher-group termination, and separate command invocations confirm the same supervisor PID survives. The failed attempt remains evidence; library development restarts from an empty repository with a newly frozen Turnyard version. This does not prove model inference or production readiness.
