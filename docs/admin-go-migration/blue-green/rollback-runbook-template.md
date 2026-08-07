# RW-011 回滚 Runbook 模板

本模板必须由目标环境的 Operations Owner 填入已评审的入口控制命令、超时、责任人和审计
位置。不要在文件中记录 kubeconfig、Token、Cookie、私钥或 registry 凭据。

## 前置确认

1. 确认当前绿色目标与证据中的 Go immutable digest 一致。
2. 确认蓝色 Scala deployment、镜像 digest、配置及证书备份未被修改。
3. 确认触发规则、检测时间、变更窗口和 Incident/Change 工单。
4. 禁止现场发布新代码、修改阈值或执行数据回滚。

## 自动动作

1. 停止向 Go 建立新连接，记录入口审计事件和时间。
2. 将新连接目标恢复到冻结 Scala digest；不得重放正在处理的 mutation 请求。
3. 等待 Scala internal readiness 成功，达到批准超时立即升级事件级别。
4. 验证证书、版本、静态 UI、Controller 只读 smoke 和登录入口。
5. 记录 Scala ready 时间、恢复秒数、最终入口目标和零数据回滚声明。

## 会话与通知

按批准决定记录 `preserved` 或 `relogin-required`。若要求重新登录，发布通知必须说明切换和
回滚均可能使进程内 session 失效，并引用通知工单。

## 收尾

保留 Go 实例和日志用于取证，但隔离用户流量；导出脱敏监控、入口审计和 Manager 日志，生成
rollback report，计算 SHA-256，并由 Operations 与 Release Owner 复核后关闭变更。
