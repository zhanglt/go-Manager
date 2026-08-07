# RW-011 蓝绿切换与回滚门禁

本目录定义蓝绿切换、观察和自动回滚的机器证据格式。门禁不会直接调用生产负载均衡器；流量
变更仍由已批准的环境适配器或运维平台执行。这样既避免在通用脚本中保存集群凭据，也确保
每次外部操作都能通过不可变报告、摘要、时间线和工单进行复核。

RW-011 依赖 RW-010。只有 `release_qualification_gate.py` 对同一 Go 镜像 digest 输出
`passed: true`，蓝绿门禁才允许通过。模板或本地合成报告不能替代正式资格报告。

## 1. 冻结制品、备份和窗口

从批准 registry 记录普通 Go RC 和最后验证的 Scala 镜像，两个引用都必须使用
`name@sha256:...`。在切换前生成并保护以下材料：

- Manager 配置备份；
- 证书挂载或证书引用备份；
- 蓝色部署及入口配置备份；
- 经 Operations 批准的回滚 runbook。

每份备份必须记录 SHA-256、生成时间，并在隔离环境实际执行恢复测试。证书私钥、Token、
Cookie、registry 凭据和 kubeconfig 不得进入仓库或 JSON 报告。门禁只读取受控证据目录中的
文件并验证摘要。

变更工单必须在窗口开始前批准。`change.window_start` 到 `change.window_end` 应覆盖预检之后的
切流、观察以及必要的生产回滚，不能在现场扩大窗口或放宽阈值。

## 2. 绿色预检

先部署不接收用户流量的 Go 绿色实例，生成 `preflight-report-template.json` 对应的报告：

- internal `/readyz` 返回 200；
- 实际服务证书已按批准的主机名/指纹验证，且有效期超过变更窗口；
- `/manager/version` 与预期 RC 版本完全一致；
- 静态 UI 请求数大于零且无错误；
- Controller smoke 请求数大于零且无错误；
- shadow 只发送批准的 GET/HEAD 等只读请求，mutation 数为零且没有未批准差异。

禁止将登录、配置 PATCH/POST/DELETE、策略变更、扫描启动、文件上传或 support command 双写
到蓝绿实例。shadow 报告只能保存状态、计数、延迟和脱敏摘要，不能保存请求/响应正文或认证
信息。

## 3. 切流和观察

入口只把新连接切到 Go；旧 Scala 连接按批准的 drain 策略结束。切流报告必须证明初始目标为
冻结 Scala digest、最终目标为 Go digest、未重放有副作用请求、重复 mutation 数为零。

观察报告按批准采样间隔记录以下十类数值，且每个样本都绑定 Go digest：

| 指标 | 默认判定方向 |
| --- | --- |
| `http_5xx_rate` | 最大值 |
| `login_success_rate` | 最小值 |
| `p95_ms`、`rss_mib`、`cpu_percent`、`goroutines` | 最大值 |
| `cache_utilization`、`controller_error_rate` | 最大值 |
| `active_file_jobs`、`active_command_jobs` | 最大值 |

实际阈值和持续时间必须由 SRE/Operations 在窗口前批准，模板不提供可直接用于生产的默认值。
每条规则都必须启用 `automatic_rollback`。单个瞬时坏样本不会触发持续时间规则；连续越限达到
批准时间后，回滚必须在一个采样间隔内开始。关键路径失败、凭据/跨用户泄露、证书或 FIPS
错误、Controller 请求放大不允许作为普通限制接受。

## 4. 自动回滚演练

无论正式观察是否触发阈值，都必须先执行一次故障注入回滚演练，并生成
`rollback-report-template.json`：

1. 注入一条已批准阈值对应的信号；
2. 自动把新流量恢复到冻结 Scala digest；
3. 等待 Scala readiness，并执行证书、版本、UI 和 Controller smoke；
4. 记录从阈值触发到 Scala ready 的恢复秒数；
5. 不执行数据回滚；
6. 按批准决策记录 session 是保留还是要求重新登录，并引用发布通知工单。

若正式观察发生持续越限，还必须提供 `reports.live_rollback`，内容与演练报告相同，但
`injected` 为 `false`，`trigger_rule_id` 必须与门禁从 observation 样本重新计算出的规则一致。

建议的环境适配器动作见 [`rollback-runbook-template.md`](rollback-runbook-template.md)。模板不含
任何特定集群命令，生产命令必须在变更评审中单独审批。

## 5. 关闭门禁

复制 `evidence-template.json` 及四类报告模板到受控证据目录，填写真实值和 SHA-256 后执行：

```bash
python3 tools/migration/blue_green_gate.py \
  --config /evidence/rw011/evidence.json \
  --output /evidence/rw011/gate-result.pre-sign.json
```

第一次运行会输出 `execution_digest` 并因缺少签字而失败。Operations 和 Release Owner 审查
同一证据包后，将两类 approval 绑定 Go digest、Scala digest 和 `execution_digest`；签字时间
必须晚于全部执行证据。再次执行，只有输出 `passed: true` 且退出码为零，才能关闭 RW-011。

任何镜像、备份摘要、阈值、报告摘要或时间线变化都会改变 execution digest，使旧签字失效。
归档时保留 evidence、原始监控导出、入口变更审计、四类报告、runbook、最终 gate 和工单；
仓库只保留无敏感信息的模板和摘要。
