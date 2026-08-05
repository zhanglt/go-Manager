# M0 Baseline Assets

本目录保存 M0 阶段可复现的现状基线，不保存密码、Token、Cookie 或生产流量。

## Route Inventory

- `routes.json` 是后续契约工具使用的机器可读清单；
- `routes.md` 是供评审的生成视图；
- 使用以下命令重新生成：

```bash
python3 tools/migration/inventory_admin_routes.py \
  --json docs/admin-go-migration/baseline/routes.json \
  --markdown docs/admin-go-migration/baseline/routes.md
```

清单区分 HTTP 方法 DSL 出现数和语义路由数。当前 Scala 源码中
`/file/export-config-fed` 存在嵌套 `post { post { ... } }`，应计为一个语义操作。
`/dashboard/details` 和 `/workload/scanned` 没有显式方法 directive，运行时接受任意方法，
清单以 `ANY` 记录；需求规格和当前 UI 均将它们作为 GET 使用。
相同 method/path 仍可能是由不同参数谓词分流的多个操作，不能自动合并。

## Performance Baseline

本机空闲基线使用：

```bash
python3 tools/migration/benchmark_idle.py \
  --jar admin/target/scala-3.3.5/admin-assembly-1.0.jar \
  --output docs/admin-go-migration/baseline/scala-idle-local.json
```

该结果只验证脚本和提供初始参考，不能替代 M0-T04/T05 的正式结果。正式基准必须使用：

- 与正式镜像一致的 Java 17、容器限制和节点规格；
- 固定 Controller、数据集、用户数、请求组合和并发曲线；
- idle、steady、burst、50 MB Body、上传下载、Controller 故障等场景；
- 至少三次独立运行，报告 median、P95/P99、RSS/CPU 和峰值内存；
- Scala 与 Go 使用相同节点、采集方式、预热时间和测试持续时间。

正式结果建议命名为 `scala-release-baseline.json`，并记录镜像 digest、Commit、测试命令、
原始结果位置和批准人。真实环境的凭据与响应不得提交到仓库。

## Contract Test Preparation

启动支持临时自签名证书的 Controller mock：

```bash
python3 tools/migration/controller_mock.py \
  --fixtures docs/admin-go-migration/baseline/controller-fixtures.example.json \
  --https \
  --port 19443
```

将测试 Scala Manager 指向 mock 时设置 `CTRL_SERVER_IP=127.0.0.1` 和
`CTRL_SERVER_PORT=19443`。mock rule 可以匹配 method、path、query、Header 和 Body
SHA-256；响应支持 JSON、文本、Base64、gzip、延迟及主动断连。示例中的身份数据全部是
保留域名和虚构值，不得替换为生产凭据后提交。

当 Scala 和 Go Manager 均已启动后执行差分：

```bash
export MANAGER_TEST_TOKEN='<test-only token>'
python3 tools/migration/contract_runner.py \
  --manifest docs/admin-go-migration/baseline/contract-manifest.example.json \
  --left-url https://127.0.0.1:18443 \
  --right-url https://127.0.0.1:28443 \
  --output /tmp/manager-contract-report.json \
  --insecure
```

Manifest 中只有完整 `${ENV:NAME}` 字符串会被替换，未设置变量时立即失败。Runner 不
跟随重定向，比较状态、Header、Cookie 和解压后的 JSON/Body。默认报告不写响应正文，
仅写长度、SHA-256、脱敏 Header 和差异 JSON Pointer。`Authorization`、`Cookie`、
`Set-Cookie`、`Token`、`X-Auth-Token`、`X-R-Sess` 的值始终脱敏。

允许忽略的 Header 和 JSON Pointer 必须逐项写入 Manifest。建议只全局忽略 HTTP
`Date`、Server 实现标识等非确定字段；禁止忽略整个 Body、认证 Cookie 或业务对象。

可以使用现有 fat JAR 一次性验证 mock、Scala 和 runner 的完整链路：

```bash
python3 tools/migration/scala_mock_smoke.py \
  --jar admin/target/scala-3.3.5/admin-assembly-1.0.jar \
  --fixtures docs/admin-go-migration/baseline/controller-fixtures.example.json \
  --manifest docs/admin-go-migration/baseline/contract-manifest.example.json \
  --output /tmp/scala-contract-smoke.json
```

该 smoke 会启动临时 HTTPS Controller mock 和 HTTP Scala Manager，并将同一 Scala
实例作为差分两端，以验证工具链的确定性。它不证明 Scala/Go 兼容；Go 服务可用后仍须
使用 `contract_runner.py` 对两个独立实现执行同一 Manifest。

检查 Manifest 对路由清单的覆盖率：

```bash
python3 tools/migration/contract_coverage.py \
  --routes docs/admin-go-migration/baseline/routes.json \
  --manifest docs/admin-go-migration/baseline/contract-manifest.m2-poc.json \
  --output docs/admin-go-migration/baseline/contract-coverage.json \
  --strict
```

CI 最终应增加 `--strict`，要求覆盖率 100%、无未知 case、无重复 case ID 且无歧义。
通常可通过 method/path 自动关联唯一 Route ID；`/processProfile` 和 `/fileProfile` 这类
相同 method/path 的参数分流必须在 case 中显式设置 `covers`，Route ID 可从
`routes.md` 或 `routes.json` 查询。当前 M2 Manifest 有 308 个 smoke case，覆盖全部
263 个语义路由；部分路由为不同请求分支保留多个用例，因此 case 数大于覆盖路由数。
