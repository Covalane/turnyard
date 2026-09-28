# 发布与复验

[文档索引](../../README.md) · [English](../en/release.md)

当前 `turnyard-agent:dev` 是本机开发镜像标签，不是已发布版本。提交和镜像发布时，应固定源码 commit、基础镜像摘要、四种代理 CLI 版本、Go 依赖及构建后镜像摘要。版本号由 Git tag 决定；不要把历史测试所用的 `0.3.2` 镜像标签当作当前版本。

每次变更先运行 `go test -race ./...`、`go vet ./...`、Staticcheck、Govulncheck 和 Ent 再生检查。GitHub CI 另在 Linux 构建当前 `Containerfile`，实际运行 Docker 挂载、只读 Git、超时清理、模型网关网络及工具密钥边界探针；CI 中未配置云模型或 OSS 凭据，不能把它的绿色结果写成真实代理验收。

候选版本的人工验收矩阵至少包括：Docker `model-only` 下四种代理各一条真实工具路径；多仓库多任务续接；主子委派与强制中断恢复；声明产出的读取与校验。计划支持的 gVisor、Apple `container`、Podman 部署档位分别在对应的真实宿主环境复验。gVisor CPU/内存限额必须在正常 cgroup 环境测量；嵌套 `--ignore-cgroups` 运行不计入该项。OSS 外部交付要求专用前缀的上传、双重读回及清理均通过。每条证据记录 commit、镜像摘要、运行时、模型、后端、状态及失败样本。

只有上述对应目标平台的验证通过，才建立版本 tag 和发布镜像。固定版本是可复验的起点；升级第三方工具时先核对上游发布版本，再更新锁定版本并重跑相关链路。
