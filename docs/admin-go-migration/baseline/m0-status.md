# M0 规格冻结与现状基线状态报告

| 属性 | 内容 |
| --- | --- |
| 状态 | In Progress，尚未通过 G0 |
| 日期 | 2026-08-01 |
| 需求基线 | `../01-requirements-specification.md` 0.1 |
| 设计基线 | `../02-architecture-design.md` 0.1 |
| 开发计划 | `../03-migration-development-plan.md` 0.1 |

## 1. 执行摘要

M0 已完成本地可执行的代码事实盘点、静态路由清单工具和本机空闲资源采集。规格审批、
真实 Controller 响应样本、生产等价性能基线及 FIPS 工具链确认依赖外部角色或环境，
因此当前不能宣告需求冻结或通过 G0。

## 2. M0 任务状态

| 任务 | 状态 | 当前证据 | 剩余工作 |
| --- | --- | --- | --- |
| M0-T01 文档评审 | Pending | 三份 Draft 已建立并完成内部一致性检查 | Product、Backend、UI、QA、Security、Release 批准 |
| M0-T02 路由扫描 | Completed | `routes.json`, `routes.md`，扫描器及 3 个单元测试 | 由 Scala Maintainer 人工复核参数谓词 |
| M0-T03 响应样本 | Blocked by environment | 已定义采样和脱敏要求 | 需要测试 Controller、认证方式和安全凭据注入渠道 |
| M0-T04 正式负载模型 | Partial | `README.md` 定义最小场景与同条件原则 | 固定数据集、节点、并发曲线、SLO 和测试时长 |
| M0-T05 Scala 基线 | Partial | `scala-idle-local.json` | 在正式 Java 17 镜像中完成 idle/load/大 Body/故障基线 |

`Blocked by environment` 只描述当前缺少外部测试环境，不代表项目不可行；获得测试环境后
继续执行，不绕过凭据或安全审批。

## 3. 路由事实基线

扫描器从 `admin/src/main/scala/com/neu/api/**/*Api.scala` 提取：

| 域 | 语义操作数 |
| --- | ---: |
| Authentication | 42 |
| Cluster | 11 |
| Dashboard | 6 |
| Device | 33 |
| Group | 42 |
| Notification | 24 |
| Policy | 58 |
| Risk | 21 |
| Sigstore | 8 |
| Workload | 18 |
| **合计** | **263** |

同时存在 262 个显式 HTTP 方法 DSL 出现点。`DeviceApi.scala` 中
`/file/export-config-fed` 的 `post { post { ... } }` 嵌套运行时仍是一个 POST；另一方面，
`/dashboard/details` 和 `/workload/scanned` 没有显式方法 directive，运行时接受任意方法，
清单以 `ANY` 记录。另有 `/processProfile` 和 `/fileProfile` 两组相同 method/path 的 GET，
它们由不同查询参数谓词分流，清单保留为独立操作。

### 路由清单限制

- 静态扫描可确定 method、path、Symbol query、Header、Cookie 和源代码位置；
- 可选/必填参数、请求 Body、响应 Body、状态和条件分支仍需源码复核及 golden 样本；
- `PATH_PREFIX` 是全局运行时前缀，不重复写入每个路由；
- 清单不能替代真实 Pekko Route 执行测试，M1 差分契约仍是兼容性裁决依据。

## 4. 本机 Scala 空闲基线

本次测量用于验证基准工具并提供重构前初始参考：

| 指标 | 结果 |
| --- | ---: |
| JAR 大小 | 104,768,942 bytes（约 99.92 MiB） |
| 启动至 TCP 监听 | 5.321 s |
| 空闲 VmRSS | 1,004,080 KiB（约 980.55 MiB） |
| VmHWM | 1,004,096 KiB（约 980.56 MiB） |
| 虚拟内存 VmSize | 6,271,248 KiB |
| 线程数 | 38 |
| 文件描述符 | 15 |

测量使用本机 OpenJDK 21.0.11、`-Xms256m -Xmx2048m`、`MANAGER_SSL=off`、端口
18443、5 秒预热和 8 个一秒间隔样本。没有 Controller 请求；结果不是正式发布基线，
也不能直接与未来容器内 Go 数据比较。原始值、主机信息、JAR SHA-256 和限制记录在
`scala-idle-local.json`。

## 5. G0 门禁差距

G0 当前为 **Not Passed**。以下项目全部关闭后方可进入正式 M1：

1. 六类责任角色完成三份文档评审并记录批准；
2. Scala Maintainer 对 263 个语义操作和参数分流完成复核；
3. 测试 Controller 环境可用，并通过批准的秘密注入方式采集脱敏响应；
4. 正式性能节点、Java 17 镜像、数据集和请求模型冻结；
5. Security/FIPS Owner 确认候选 Go 工具链、密码模块、基础镜像和验证环境；
6. 明确生产 Manager 副本/粘性会话、Controller TLS 例外和缓存持久性要求。

## 6. 下一执行顺序

1. 人工复核 `routes.md` 中的重复路由、参数谓词和 Header；
2. 准备测试 Controller 与脱敏 fixture 采集规范，执行 M0-T03；
3. 在正式 Scala 镜像中执行可重复的 idle、steady、burst、上传下载和故障基线；
4. 完成 FIPS 预研结论和文档审批；
5. G0 通过后开始 M1 Controller mock 与差分比较器开发。

## 7. M1 非阻断准备状态

在不宣告 G0 通过的前提下，已完成可独立验证的 M1 工具骨架：

| M1 任务 | 状态 | 说明 |
| --- | --- | --- |
| M1-T01 Controller mock | Partial | HTTP/HTTPS、临时证书、匹配、gzip、延迟、断连已完成；全路由 fixture 待采集 |
| M1-T02 黑盒 runner | Implemented | 支持同一 Manifest 对 Scala/Go 双端执行 |
| M1-T03 HTTP 差分比较器 | Implemented | 状态、Header、Cookie、JSON Pointer、二进制 Hash |
| M1-T04 规范化与覆盖门禁 | Partial | 支持全局/单 case 忽略及 Route ID 覆盖统计；具体白名单待真实样本批准 |
| M1-T05 REST 主流程转换 | Pending | 需要测试 Controller 和测试账户 |
| M1-T06 CI 集成 | Pending | G0 和 fixture 策略批准后执行 |

工具不会把响应正文写入报告；敏感 Header 始终脱敏，测试秘密只允许通过环境变量注入。
这些准备工作不替代 G0 审批，也不构成正式 M1 完成声明。

M0 验收时示例 Manifest 含 37 个用例，按修正后清单覆盖 263 个语义操作中的 36 个（13.69%）；无未知、歧义
或重复 case ID。此数字只证明覆盖门禁可运行，M1 退出条件仍要求完整路由契约。

## 8. 已执行验证

```text
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s tools/migration -p 'test_*.py' -v
Ran 12 tests: OK

Route inventory: 263 semantic actions / 262 explicit DSL method occurrences
Idle benchmark: process exited cleanly; port 18443 released
Contract tools: HTTP/gzip, generated HTTPS certificate, redaction and dual-endpoint runner passed
Scala integration: 37/37 smoke cases passed against HTTPS Controller mock
Coverage gate: 36/263 routes (13.69%), 0 unknown, 0 ambiguous, 0 duplicate case IDs
```

后续迁移已修复 methodless route 的扫描缺口，并将当前 Manifest 扩展至 304 个用例、覆盖
全部 263 个语义路由（100%）；最新机器可读结果见 `contract-coverage.json`。
