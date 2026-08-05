# RW-004 真实 Controller 验证

本门禁只允许使用经 QA/Security 批准、与生产隔离且可恢复的 Controller 测试环境。真实密码、
Token、Cookie、客户名称、邮箱、IP、镜像名或响应正文不得提交到仓库；凭据仅从环境变量或
批准的秘密挂载注入，原始采集数据留在受控证据目录并按测试数据策略销毁。

## 1. 数据批准与脱敏

在批准工单中记录环境、版本、数据集、隔离方式、恢复点、允许的变更操作和清理负责人。
采集后的 Controller fixture 在进入共享证据目录前执行确定性脱敏：

```bash
export RW004_SANITIZE_KEY='<来自秘密存储的至少 32 字符随机值>'
python3 tools/migration/sanitize_controller_fixture.py \
  --input /secure/rw004/controller-fixtures.raw.json \
  --output /evidence/rw004/controller-fixtures.sanitized.json \
  --approval-id '<批准工单中的环境批准 ID>'
sha256sum /evidence/rw004/controller-fixtures.sanitized.json
```

工具对 Token、密码、Cookie、Credential 和私钥直接清除；对用户、名称、ID、邮箱、IP、URL
使用稳定伪名，以保留跨响应关联。门禁还会再次扫描脱敏结果，并核对批准 ID 和 SHA-256。
若 fixture 的请求路径包含真实资源 ID，应先在批准环境中使用专用 `rw004-*` 测试数据，禁止
把客户资源路径原样归档。

## 2. Scala/Go 真实差分

Scala 和 Go Manager 必须连接同一批准 Controller 快照；涉及写操作时，每个实现从相同快照
独立恢复，禁止让 Scala 的创建/删除结果污染 Go 运行。凭据由环境变量注入：

```bash
export MANAGER_TEST_TOKEN='<批准测试用户的临时 Token>'
python3 tools/migration/contract_runner.py \
  --manifest docs/admin-go-migration/baseline/contract-manifest.m2-poc.json \
  --left-url "${RW004_SCALA_URL}" --right-url "${RW004_GO_URL}" \
  --output /evidence/rw004/real-controller-contract.json \
  --approval-candidates-output /evidence/rw004/difference-candidates.json \
  --approvals /evidence/rw004/difference-approvals.json \
  --strict-normalization --insecure
python3 tools/migration/contract_coverage.py \
  --routes docs/admin-go-migration/baseline/routes.json \
  --manifest docs/admin-go-migration/baseline/contract-manifest.m2-poc.json \
  --output /evidence/rw004/contract-coverage.json --strict
```

Manifest 当前包含 308 个场景并保持 263/263 路由覆盖；额外负例覆盖缺 Token、非法分页和缺
查询参数。特性门禁要求成功、错误、gzip、响应 Header、Cookie、联邦、至少 1 MiB 响应和
有状态 transaction 全部通过。Normalization 只能忽略带理由的精确 JSON Pointer；全局仅允许
忽略 `Date` 和 `Server`。预期差异不能靠 ignore 隐藏，必须以 case ID、difference kind、精确
path 登记批准，通配符、未使用或过期条目均应拒绝。
二进制/非 JSON Body 差异必须同时固定左右 decoded body 的 SHA-256；digest 变化后原批准自动
失效，不能用一个长期的 `/body` 条目掩盖任意内容变化。

## 3. UI 和 CLI 工作流

QA 浏览器套件和打包后的 CLI 套件由 `workflow_runner.py` 统一编排。Runner 首先检查 Go 内部
metrics 中的 `go_goroutines`，确保工作流目标不是 Scala，然后只归档退出码、耗时、输出摘要
SHA-256 和截图摘要，不保存 stdout/stderr 或凭据：

```bash
export RW004_GO_METRICS_URL='https://127.0.0.1:19090/metrics'
python3 tools/migration/workflow_runner.py \
  --config /evidence/rw004/workflows.json \
  --output /evidence/rw004/workflows-report.json
```

最低 UI 覆盖为登录、Dashboard、Workloads、Network Policy、Risk 和 Settings；最低 CLI 覆盖
为登录、System、Workloads 和 Federation。UI 截图、浏览器 console/network 错误、CLI 输出和
Controller 审计日志由 QA 在受控证据系统查看，仓库报告只保存摘要。

## 4. 最终关闭

复制 `evidence-template.json` 到证据目录，填写真实文件路径和 digest。Controller 环境、fixture、
QA 与 UI Owner 四类批准均必须包含 owner、ticket、date 并显式设为 `approved: true`：

```bash
python3 tools/migration/real_controller_gate.py \
  --config /evidence/rw004/evidence.json \
  --output /evidence/rw004/gate-result.json
```

缺少环境、脱敏 fixture、308 场景报告、263/263 coverage、任一特性、任一 UI/CLI 工作流、
未解释差异或 Owner 批准时，命令返回非零。只有 `gate-result.json` 为 PASS 且证据包由 QA
归档后，才能勾选 RW-004。
