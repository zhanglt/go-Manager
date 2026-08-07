# RW-011 本地蓝绿与回滚演练（2026-08-07）

## 结论

当前正式结论为 **NO-GO / FAIL-CLOSED**。RW-011 门禁、证据模型、运维模板和合成演练已经
完成，但 RW-010 仍未取得正式 PASS，且本轮没有操作真实 registry、负载均衡器、集群、监控
或生产流量。不得据此勾选 RW-011。

## 已完成实现

`tools/migration/blue_green_gate.py` 将以下材料绑定为一个 execution digest：

- 已通过的 RW-010 报告和同一 Go immutable digest；
- 冻结 Scala immutable digest；
- 配置、证书和蓝色部署备份及恢复测试；
- 绿色 readiness、证书、版本、静态 UI、Controller 和只读 shadow 预检；
- 批准窗口内只切新连接、零 mutation 重放/重复的入口审计；
- 5xx、登录成功率、P95、RSS、CPU、goroutine、cache、Controller 错误、文件任务和命令任务；
- 持续阈值检测、一个采样间隔内自动回滚、Scala readiness、恢复时间和 session 通知；
- Operations 与 Release 两类执行后签字。

任一镜像、备份摘要、阈值、报告或时间线变化都会改变 execution digest，使旧签字失效。持续
越限时必须提供与重新计算结果匹配的 live rollback 报告；没有真实越限时仍强制要求一次故障
注入回滚演练。

## 本地演练结果

合成环境模拟了 Scala 蓝色入口、Go 绿色预检、只切新连接、10 分钟观察和 30 秒 Scala 自动
恢复。独立 CLI 读取文件和 SHA-256 后结果如下：

| 项目 | 结果 |
| --- | --- |
| 门禁检查 | 68/68 PASS |
| 持续阈值触发 | 0 |
| 回滚演练恢复时间 | 30 秒，低于合成阈值 120 秒 |
| execution digest | `sha256:1163f949500411e3ae1e821f1bcd39c4a7cb557e87c190e8dfd1cf910e611813` |
| gate result SHA-256 | `1ecc4aadd1daebe79faafd9a1ed9b482d8ff19337d1ae0c73a348d114d07da23` |

另外覆盖了以下负向场景：RW-010 未通过、备份未恢复测试、shadow mutation、重复副作用请求、
瞬时阈值抖动、持续越限但无 live rollback、匹配的自动 live rollback、恢复超时和陈旧签字。
未填写模板执行 68 项检查时有 65 项失败并返回非零，符合 fail-closed 预期。

上述 execution digest 和报告位于临时合成证据目录，只验证工具行为，不是正式发布证据。

## 正式阻断项

- RW-010 仍为 NO-GO，缺少已批准且绑定正式 Go RC digest 的资格报告。
- 尚未从批准 registry 冻结正式 Go RC 和最后验证的 Scala rollback digest。
- 尚未生成并恢复测试真实配置、证书引用和蓝色部署备份。
- 尚无目标环境负载均衡器/Service/Ingress 的已审批适配器、权限和入口审计记录。
- 尚未在真实绿色实例执行 readiness、证书、版本、UI、Controller 和只读 shadow。
- 登录、错误率、延迟和资源阈值及持续窗口尚未得到 SRE/Operations 批准。
- 尚无真实观察窗口、监控导出、关键路径结果和零副作用切流证据。
- 尚未在目标环境注入阈值并验证自动恢复 Scala、readiness、恢复时间与 session 通知。
- Operations 和 Release Owner 尚未对最终 execution digest 签字。

只有上述阻断全部关闭，并由 `blue_green_gate.py` 对受控证据输出 `passed: true`，才能将
RW-011 标记为完成。
