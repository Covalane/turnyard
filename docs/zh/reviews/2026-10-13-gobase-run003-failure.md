# gobase 第三轮：无交互权限与未完成调用

第三轮使用 Turnyard `00c352e`，从空仓库开始。首批集合候选完成了机器检查；独立 Claude + DeepSeek 会话在只读产品快照上生成并执行了黑盒测试、示例、fuzz 和基准。它们只证明首批集合的已声明行为，不代表完整工具库。

## 已观察的失败

1. Ollama 主凭据在真实模型输出后返回429会话额度错误。任务进入unknown，经检查与reconcile，结束其他活动调用后使用同一二进制重启supervisor、映射已有备用凭据，再沿原生会话重试成功。没有更换绑定模型或伪造零用量。
2. 第二批的OpenCode调用建立待办后，读取镜像内依赖缓存的bash工具请求被原生权限机制拒绝。拒绝文本来自OpenCode的无交互处理，不是人类实际否决。CLI仍退出0，最后结构化step_finish原因为tool-calls，没有正常stop。
3. 原解析器仅采集会话ID与工具活动，没有检查调用终态，因此认为该调用成功。该Work只声明既有API/场景文档作为产物，旧集合测试仍通过，候选因此被标verified；第二批所要求的三个包根本没有生成。负责人源码审阅拒绝将它计为需求完成。这里既有原生调用终态识别缺陷，也有任务提交者必需产物约束不足；不能假定自然语言acceptance已被自动判定。
4. 立即执行daemon stop/start时，新进程曾先于旧进程释放状态锁而启动，出现ALREADY_RUNNING及DAEMON_FAILED；第二次启动成功。这是已证实的停止确认竞态。

## 修复与验证

- OpenCode在沙箱中使用其`--auto`无交互模式，保持显式deny；环境配置允许访问镜像与挂载范围内的外部目录，委派交接编辑仍deny。实际能力边界仍由沙箱、网络和MCP目录执行，不以原生ask替代隔离。
- 解析器使用结构化结束原因：最后一轮必须正常stop，否则返回AGENT_INCOMPLETE。工具错误后若恢复并最终stop仍可接受；没有依据英语文本或待办数量判断业务是否完成。
- daemon stop确认状态目录锁已经释放后返回；真实CLI回归无sleep连续stop/start三次，并保留此前进程组分离验证。
- 后续输入逐批声明实际必需的源代码与测试文件。文件存在也不证明业务正确，负责人和独立验收仍按具体行为评估。
- 真实预检用最终修复二进制、OpenCode和云模型，实际执行`cat /opt/go/pkg/mod/cache/download/go.uber.org/goleak/@v/v1.3.0.mod`，没有权限拒绝，最后正常stop并交付verified文本。初步宽泛权限试验不计为最终配置验证；最终使用`--auto`及保留deny的配置单独重跑。

前述库产物及独立集合验收保留为第三轮证据；因执行器需要修改，本轮作废。第四轮重新建立空Git与状态，不能导入这轮生成的产品、测试或API文档。修复预检不是完整工具库开发验收。

参考：[OpenCode权限文档](https://opencode.ai/docs/permissions/)。本轮不能支持“任意Go开发90%”或所有代理组合生产就绪的结论。

## English summary

Run003 reached trusted set verification and independent readonly acceptance, then a headless OpenCode permission request rejected image-cache access. The CLI exited zero with a final tool-calls step, and the old parser treated it as successful. Existing tests and weak required-output declarations consequently passed despite absent new packages. The fixes use native auto approval while retaining deny and sandbox boundaries, require an explicit completed model turn, and make daemon stop wait for lock release before restart. A final-binary live preflight reads the external image cache and returns a verified witness; regression tests cover incomplete/recovered streams and immediate real-CLI restarts. Required source/test artifacts will also be strengthened. Run003 is retained as evidence and invalidated; the library restarts from empty repositories after freezing repairs.
