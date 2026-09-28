# Git 工作流

[使用指南](README.md) · [English](../en/git.md)

Turnyard 在独立工作区克隆每个输入仓库的指定 commit。任务只能修改 `Work.scope.repositories` 中标为 `write` 的工作树；代理不能直接改 `.git`。Turnyard 在宿主侧创建 `feature/<task-id>` 分支、提交代码，并把 base、head 和 tree 记录在候选版本中。多个仓库会分别得到自己的分支和 commit。

提交标题取自 `Work.commitMessage`，建议写清这次变更，例如 `feat: add Google OIDC sign-in` 或 `fix: bind OAuth state to browser`。该字段限单行、80 个字符，不能包含控制字符；未填写时从 `objective` 提取短标题。任务 ID 留在提交正文的 `Turnyard-Task` trailer，重试后再次提交会在标题末尾注明 `(revision 2)` 等序号。已经发布的提交不会由 Turnyard 自动改写。

## 配置仓库

本机仓库使用 `local-git` 和 `path`。远端仓库使用 `remote-git` 和 `url`，例如：

```json
{"id":"app","type":"remote-git","url":"https://github.com/OWNER/REPO.git","commit":"0123456789abcdef0123456789abcdef01234567"}
```

`commit` 必须已经存在于来源仓库，且必须是完整的 40 位 SHA。刚创建的空仓库需先写入并推送初始 commit，才能作为远端输入。支持 HTTPS、SSH URL 和绝对路径的 `file://` URL；输入不能包含密码或 token。可选 `pushUrl` 指定发布目标；远端仓库不填写时，发布到 `url`。本机仓库要发布时必须填写 `pushUrl`。

私有 HTTPS 仓库使用宿主机现有的 Git credential helper；SSH URL 使用宿主机 SSH agent。凭据不放进 JSON，也不传入执行代理的沙箱。使用 GitHub CLI 时，可先执行 `gh auth login` 和 `gh auth setup-git`，然后确认 `git credential fill` 能获得对应主机的凭据。不要把 token 写入任务、日志或远端 URL。

## 发布已验证分支

包含仓库的会话必须显式配置 `"git":{"localCommits":true,"remoteWrites":"none"}`。要允许发布，改成 `"remoteWrites":"push"`。这只是授权能力，不会在任务结束时自动写远端。无仓库会话可以省略 `git`；它仍能交付文件、图片或文本。Git 是代码候选版本与发布的一种能力，不是所有任务的必需输入。代码任务的 feature commit 由 Turnyard 在宿主侧创建，以锁定检查所针对的版本；代理在沙箱内不能直接改写受保护的 `.git` 元数据。

完成检查、人工审阅并结束会话后，执行：

```sh
bin/turnyard session complete <session-id>
bin/turnyard session publish <session-id> --timeout 600
```

发布只处理已验证任务的可写仓库。Turnyard 在推送前重新核对候选分支的 head 和 tree，拒绝覆盖已有的不同远端分支，不使用 force push；推送后读取远端 ref，确认它等于候选 head。重复调用会返回 `already_published`。多仓库中途失败时，结果为 `partial`，列出每个分支的状态和错误码，CLI 返回非零退出码；核查后重试同一命令即可对已发布分支做幂等核对。

会话完成只证明配置的机器检查及已声明交付物通过；发布也不代表人工代码审查、PR、合并或部署已经完成。远端可能设置分支保护或权限限制，遇到拒绝时 Turnyard 保留本地候选和事件供排查。
