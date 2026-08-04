# Admin 后端 Go/Gin 迁移开发计划

| 属性 | 内容 |
| --- | --- |
| 文档状态 | Draft，设计文档冻结后进入执行 |
| 需求基线 | `01-requirements-specification.md` 0.1 |
| 设计基线 | `02-architecture-design.md` 0.1 |
| 估算单位 | Person-day（PD），1 PD = 1 人工作日 |
| 最后更新 | 2026-08-01 |

## 1. 执行目标

本计划将 Go/Gin 重构拆分为可独立评审的阶段和业务波次。每一阶段均设置进入条件、
交付物、测试和退出门禁。开发可以按领域并行，但正式发布只允许在全量兼容后整体
切换，避免 Scala 与 Go 进程内 Session 状态不一致。

## 2. 团队、周期与估算

建议核心团队：

| 角色 | 建议投入 |
| --- | --- |
| Go Backend Engineer | 3 人全职，其中 1 人兼任技术负责人 |
| QA/Automation Engineer | 1 人全职 |
| UI Engineer | 0.25 人，负责 E2E 与问题定位 |
| Security/FIPS Engineer | 0.25~0.5 人，阶段性投入 |
| Release/DevOps Engineer | 0.25 人，阶段性投入 |
| Scala Maintainer | 0.25 人，解释历史行为和评审契约 |

初始工程估算为 **156~194 PD**，预计 **18~26 个自然周**完成开发和发布候选版本，
之后保留 **4~8 周稳定观察期**。估算不包含 Controller 新功能、公开 API 重新设计、
外部 FIPS 认证排队或前端大规模修改。

估算在以下节点更新：接口契约盘点完成、POC 门禁完成、每个业务波次完成。估算变化
超过 20% 时必须更新本计划并重新批准，而不是静默压缩测试。

## 3. 分支、提交与变更控制

- 在主分支正常 PR 流程下增量提交 `admin-go/`，不创建长期脱离主线的重写分支；
- 每个 PR 只覆盖一个基础能力或一个可验证的路由子集，并引用任务 ID 和需求 ID；
- Scala 行为仅用于增加测试或修复阻断迁移的可观测缺陷，不在同一 PR 改变两种实现；
- golden fixture 必须脱敏，不包含真实 Token、密码、Cookie、用户邮箱或集群信息；
- 需求、设计或契约发生变化时，先更新并批准文档，再修改代码；
- 所有例外必须记录期限、责任人和删除条件，不接受无期限 TODO。

## 4. 总体里程碑

| 里程碑 | 内容 | 估算 | 主要输出 |
| --- | --- | ---: | --- |
| M0 | 规格冻结与基线 | 8~10 PD | 已批准规格、路由目录、性能基线 |
| M1 | 契约测试平台 | 16~18 PD | Controller mock、差分与 golden 框架 |
| M2 | FIPS/基础设施 POC | 20~26 PD | 可启动 Go 服务、POC 报告、Go/No-Go |
| M3 | 基础能力完成 | 16~22 PD | Config、TLS、Client、Session、Cache、Static |
| M4 | 业务域迁移 | 66~84 PD | 全部 API 域及领域测试 |
| M5 | 发布与全量验收 | 18~22 PD | 多架构/FIPS 镜像、性能、E2E、RC |
| M6 | 切换与稳定 | 8~12 PD | 蓝绿切换、观察、Scala 清理决策 |

关键依赖链为：`M0 → M1 → M2 Go/No-Go → M3 → M4 → M5 → M6`。M4 内部业务波次
可部分并行，但共享代码修改必须由技术负责人串行评审。

## 5. M0：规格冻结与现状基线

**进入条件：** 本仓库、可运行的 Scala Manager、测试 Controller 环境和性能节点可用。

| 任务 | 内容 | 角色 | 估算 | 需求 |
| --- | --- | --- | ---: | --- |
| M0-T01 | 评审需求、设计和本计划，登记待评审项负责人 | Lead/PO/QA/Security | 1~2 | 全部 |
| M0-T02 | 自动扫描 Scala Router，生成 method/path/parameter/Header 清单 | Backend/QA | 2 | `IF-001`~`IF-008` |
| M0-T03 | 采集正常及错误响应样本并完成敏感信息清理 | QA/Scala | 2~3 | `AC-001`, `AC-002` |
| M0-T04 | 固定性能数据集、流量模型、节点规格和测量脚本 | QA/DevOps | 2 | `NFR-PERF-001` |
| M0-T05 | 采集 Scala idle/load RSS、CPU、吞吐、P50/P95/P99、启动时间 | QA/DevOps | 1~2 | `NFR-PERF-*` |

**退出门禁 G0：**

- 三份文档获得角色批准，所有待评审项有结论或明确阻断状态；
- 路由扫描数量与人工分类一致；
- 性能基线可由另一名工程师重复得到，偏差在批准阈值内；
- FIPS 责任人明确工具链候选和验证环境。

## 6. M1：兼容契约测试平台

### 6.1 测试工具

| 任务 | 内容 | 估算 | 完成定义 |
| --- | --- | ---: | --- |
| M1-T01 | 建立可编排的 Controller mock，支持状态、Header、gzip、延迟和断连 | 4 | 可按 fixture 重放全部响应类型 |
| M1-T02 | 建立 Manager 黑盒请求 runner | 2 | 同一 case 可指向 Scala 或 Go 地址 |
| M1-T03 | 实现 HTTP 差分比较器 | 3 | 比较状态、Header、Cookie、Body、重定向 |
| M1-T04 | 建立 JSON 规范化白名单 | 2 | 仅忽略批准的时间/request ID 等字段 |
| M1-T05 | 把现有 REST shell 主流程转为可重复自动化 case | 3~4 | 无手工 Token 拷贝，不写真实凭据 |
| M1-T06 | CI 集成与报告归档 | 2~3 | PR 可查看域、路由和差异详情 |

### 6.2 契约用例最小集合

每个 endpoint/method 至少包含成功用例和一个失败用例；有 Body 的接口包含空 Body、
非法 JSON 和边界字段；有查询参数的接口包含缺失、空字符串、重复与转义值。公共场景：

- Token Header 缺失、无效、过期；
- Controller 301/302、400、401、403、404、408、409、429、500、503；
- Controller 延迟、DNS/连接失败、响应中途断开；
- gzip 与 identity、空 Body、`null`、大整数和未知字段；
- `PATH_PREFIX` 未设置、空白、普通值和需要 URL 转义的值；
- 1 byte、接近 50 MB 和超过 50 MB 的 Body；
- multipart 文件名、Content-Type、事务 Header 和流式下载。

**退出门禁 G1：** 测试平台能对 Scala 运行全部已登记路由；失败可归因到 fixture、环境
或实现；比较器无全局忽略 Body/Header 的逃逸选项。

## 7. M2：FIPS 与基础设施 POC

M2 是投入全面迁移前的强制 Go/No-Go 阶段。

| 任务 | 内容 | 估算 | 需求 |
| --- | --- | ---: | --- |
| M2-T01 | 初始化 `admin-go` module、构建信息和基础 CI | 2 | `NFR-BUILD-*` |
| M2-T02 | 实现 Config、Gin Server、recovery、日志和优雅退出 | 3~4 | `FR-CFG-*`, `NFR-OBS-*` |
| M2-T03 | 实现 Server/Client TLS、证书读取和自签名证书 | 3~4 | `NFR-SEC-001`~`003` |
| M2-T04 | 构建并验证批准的 FIPS 二进制及运行镜像 | 4~6 | `NFR-SEC-001` |
| M2-T05 | 实现 Controller Transport、URL Resolver 和错误骨架 | 3~4 | `FR-CTL-*` |
| M2-T06 | 实现 POC 路由：登录、Sigstore CRUD、Dashboard 聚合 | 3~4 | `FR-AUTH-*`, `FR-DATA-*` |
| M2-T07 | 执行契约、race、性能和镜像大小对比 | 2 | `AC-002`, `AC-008`, `AC-009` |

### Go/No-Go 标准 G2

必须同时满足：

- FIPS Owner 书面确认工具链、密码模块、基础镜像和证据链可用于正式产品；
- POC 路由无未解释契约差异，`go test -race` 通过；
- RSS 相比同场景 Scala 至少降低 50%，吞吐不下降，P95 劣化不超过 10%；
- 证书读取、自签名证书、Controller TLS 和普通/FIPS 模式均可运行；
- 未发现需要修改 Angular 或 Controller 才能继续的架构阻断项。

任一项失败则暂停全面迁移，形成原因、补救成本和继续/终止建议，不进入 M3。

## 8. M3：共享基础能力

| 工作包 | 任务 | 估算 | 验收 |
| --- | --- | ---: | --- |
| M3-W01 HTTP | 完成 Middleware 顺序、限制、安全 Header、`PATH_PREFIX` | 3 | 表驱动 Header/路由测试通过 |
| M3-W02 Client | 完成流式 Proxy、gzip、Header、multipart、取消和连接池 | 3~4 | 故障注入和泄漏测试通过 |
| M3-W03 Session | 完成并发 Store、TTL、原子 Update、登出清理 | 2~3 | race 与容量测试通过 |
| M3-W04 Cache | 完成有界 Cache、key namespace、失效和指标 | 3~4 | 容量、淘汰、隔离测试通过 |
| M3-W05 Static | 完成 embed、普通模式 MD5/FIPS-only SHA-256 版本、重定向、预压缩资源 | 2~3 | `IF-020`~`IF-024` 差分通过，FIPS-only 启动无 MD5 panic |
| M3-W06 Local/Command | 完成 CSV Loader、文件与 Command Runner | 2~3 | 损坏资源、取消、权限和清理测试通过 |
| M3-W07 Test Support | fake clock、fake client、fixture builder、泄漏检查 | 1~2 | 后续领域无需真实 Controller 做单测 |

**退出门禁 G3：** 基础包 API 冻结；核心包覆盖率达到 80%；静态检查、race、基础契约
全部通过；未授权领域代码不能直接访问 Gin Context、环境变量或 `http.DefaultClient`。

## 9. M4：业务域迁移

每个波次执行相同流程：建立 endpoint checklist → 移植模型/Codec → 实现 Service → 注册
Handler → 单元/race/契约测试 → UI/CLI smoke → 代码与需求追踪评审。

### 9.1 Wave A：Sigstore、Federation、Account 基础接口

**任务 ID：** `M4-A01`~`M4-A03`；**估算：** 8~10 PD。

- 完成 Sigstore/Verifier CRUD；
- 完成 Federation member、switch、summary、promote/demote、join/leave/config/deploy；
- 完成 gravatar、eula、rebrand、version、permission-options 等低耦合接口；
- 验证两个 Token 并发切换不同集群，无目标 URL 串线。

**门禁 GA：** 对应路由契约 100% 通过，联邦并发 race test 通过。

### 9.2 Wave B：Workload 与 Device

**任务 ID：** `M4-B01`~`M4-B05`；**估算：** 12~16 PD。

- Workload/container/domain CRUD、分页、process history 和 compliance；
- sniffer 生命周期、PCAP 和 scan report 下载；
- enforcer/controller/scanner/host/config/webhook/remote repository；
- file config 导入导出、transaction/as-standalone Header；
- support log、bench、CSP support 的 Command Runner 与状态机。

**门禁 GB：** 上传下载峰值内存受控；取消请求可终止 I/O/命令；无临时文件残留；对应
UI 页面与 CLI smoke 通过。

### 9.3 Wave C：Group 与 Policy/Scan/Admission

**任务 ID：** `M4-C01`~`M4-C07`；**估算：** 18~22 PD。

- group/service/process/file profile 及 custom check；
- DLP/WAF sensor、group、联邦导入导出；
- response policy、network policy、application/rule/graph/promote；
- scan status/workload/host/platform/config/registry/repo/image/layer/top；
- admission rule/options/state/test/matching/import/export/promote；
- 完成分页缓存、事务 URL、CRUD 后缓存失效和稳定排序。

**门禁 GC：** 所有导入导出及事务 Header 差分通过；重复数据和相同排序 key 的结果稳定；
缓存不存在跨 Token/集群污染。

### 9.4 Wave D：Risk、Dashboard 与 Notification

**任务 ID：** `M4-D01`~`M4-D06`；**估算：** 16~20 PD。

- scanned assets、vulnerability asset、CVE profile 和 assets view；
- compliance、template、filter、profile 和 CIS/NIST 映射；
- dashboard multi-cluster summary、alerts、details、scores、notifications；
- event/incident/violation/audit/threat/security events；
- network session/conversation/endpoint/history/graph/layout/blacklist；
- 对聚合请求增加并发上限、取消传播、稳定排序和大结果内存测试。

**门禁 GD：** 聚合 JSON golden 全量通过；24 小时前置长稳无 Cache/goroutine 增长趋势；
Dashboard 和风险报告 UI E2E 通过。

### 9.5 Wave E：完整 Authentication 与 Account 管理

**任务 ID：** `M4-E01`~`M4-E07`；**估算：** 12~16 PD。

- 用户名/密码与 SUSE/Rancher SSO 登录、自身信息、heartbeat、logout；
- OIDC code/state 登录和 Token 刷新；
- SAML 登录、回调、SLO request/response；
- role、API key、user、license、password profile、auth server CRUD；
- Token 解析、角色数字映射、Gravatar、错误码 14/47/48/50；
- Session 容量、TTL、并发登录、登出和集群状态清理；
- 认证日志脱敏与重定向安全测试。

**门禁 GE：** 普通、OIDC、SAML、SUSE 四种路径在真实测试环境通过；并发登录不覆盖
state；日志扫描无 Token/Cookie/密码；安全团队批准。

### 9.6 M4 整体门禁 G4

- 路由快照无遗漏，所有 endpoint checklist 关闭；
- 全部契约测试通过，任何批准差异均关联独立需求变更；
- 核心包覆盖率不低于 80%，整体 Go 行覆盖率不低于 70%；
- `gofmt`、`go vet`、静态检查、单元测试和 `go test -race ./...` 通过；
- Angular 关键流程和 Python CLI 回归通过。

## 10. M5：发布工程与全量验收

### 10.1 构建与镜像

| 任务 | 内容 | 估算 |
| --- | --- | ---: |
| M5-T01 | 更新 Dockerfile：Angular + Go + Python CLI 多阶段构建 | 3~4 |
| M5-T02 | 更新 Makefile、buildx、release workflow 和缓存 | 2~3 |
| M5-T03 | 更新 entrypoint、OCI Label、许可证、SBOM、provenance | 2 |
| M5-T04 | 构建 amd64/arm64 普通及 FIPS 镜像 | 2~3 |
| M5-T05 | 验证镜像无 Java/JAR，非 root、只读资源与文件权限正确 | 1~2 |

### 10.2 全量测试

| 测试 | 最小场景 | 发布阈值 |
| --- | --- | --- |
| Contract | 全路由成功/失败/gzip/Header/Cookie | 0 个未批准差异 |
| E2E | 登录、Dashboard、资产、策略、扫描、风险、联邦、设置 | 关键路径 100% 通过 |
| Performance | idle、steady、burst、大 Body、下载 | 满足 `NFR-PERF-*` |
| Stability | 24 小时混合流量、Controller 间歇故障 | 无泄漏、崩溃、死锁 |
| Security | FIPS、TLS、CSP、依赖、SBOM、秘密、文件权限 | 0 个阻断问题 |
| Upgrade | Scala → Go，配置/证书/UI/CLI | 除已批准重新登录外无回归 |
| Rollback | Go → Scala 镜像 | 规定时间内恢复服务 |

**退出门禁 G5：** 形成 Release Candidate；性能、安全、FIPS、QA 和 Release Owner 全部
签字；已知问题具备影响、规避方式、责任人和修复版本，不存在 P0/P1 问题。

## 11. M6：蓝绿切换与稳定期

### 11.1 切换步骤

1. 冻结迁移相关发布，确认 Scala 回滚镜像 digest 和配置备份；
2. 部署 Go 绿色实例，执行 `/readyz`、证书、版本、静态资源和 Controller smoke；
3. 执行只读 shadow 对比，禁止副作用请求双写；
4. 在批准窗口把新连接切到 Go，记录切换时间；
5. 连续观察登录成功率、HTTP 4xx/5xx、Controller 错误、P95/P99、RSS/CPU、goroutine、
   Cache 和文件/命令任务；
6. 达到观察门禁后停止 Scala 蓝色实例，但保留镜像和部署配置。

### 11.2 自动回滚条件

以下任一条件持续超过批准窗口即回滚，不在发布窗口现场修改代码：

- Go 5xx 比率显著高于 Scala 基线或超过产品 SLO；
- 登录/SSO、策略变更、扫描或联邦关键路径失败；
- P95、RSS、CPU 或 goroutine 持续超出验收阈值；
- 出现 Token/Cookie 泄露、跨用户数据、FIPS 状态错误或证书问题；
- Controller 请求放大、连接耗尽、文件泄漏或命令无法终止。

回滚动作是将入口切回已验证的 Scala 镜像；不执行数据回滚。由于 Session 为进程本地，
切换和回滚可能要求用户重新登录，此行为必须在发布通知中说明。

### 11.3 稳定期退出

Go 版本稳定运行一个完整发布周期且无未解决 P0/P1 回归后：

- 归档最终性能、兼容、安全和 FIPS 报告；
- 删除 Scala Admin、Pekko/SBT Admin 依赖、JVM 镜像层和 JAR 构建路径；
- 保留必要历史 fixture，更新仓库贡献指南和运维文档；
- 删除行为确认由单独 PR 执行，不与首次 Go 发布合并。

## 12. Definition of Done

每个开发任务必须同时满足：

- 代码实现引用需求和任务 ID，设计无偏离或已有批准的 ADR；
- 单元测试包含成功、边界、错误和取消；共享状态代码包含 race 场景；
- 对应 Scala/Go 契约用例通过，route checklist 更新；
- 错误和日志已检查敏感信息，外部输入有大小/格式/超时限制；
- `gofmt`、`go vet`、静态检查、测试和构建通过；
- 文档、配置、fixture、许可证和依赖清单同步更新；
- 至少一名领域 Reviewer 和一名 Go Reviewer 批准。

## 13. 需求—任务追踪矩阵

| 需求 | 实现任务 | 验证任务 |
| --- | --- | --- |
| `IF-001`~`IF-008` | M3-W01/W02、M4 全部 | M1-T02~T06、G4 |
| `IF-020`~`IF-024` | M3-W05 | Static contract、UI smoke |
| `FR-CFG-*` | M2-T02、M3-W01 | Config unit、shutdown integration |
| `FR-AUTH-*` | M3-W03、M4-E | Auth integration、race、E2E |
| `FR-CTL-*` | M2-T05、M3-W02 | Mock fault、stream、leak tests |
| `FR-DATA-*` | M4-C/M4-D | JSON golden、聚合 unit tests |
| `FR-CACHE-*` | M3-W04、M4 各波次 | 隔离、容量、失效、race tests |
| `FR-LOCAL-*` | M3-W06、M4-D | CSV fixture、启动失败测试 |
| `FR-STATIC-*` | M3-W05、M5-T01 | 静态契约和浏览器测试 |
| `FR-OPS-*` | M3-W06、M4-B | 文件/命令/取消/权限测试 |
| `NFR-PERF-*` | M2-T07、M4-D、M5 | 基准、24h 长稳报告 |
| `NFR-SEC-*` | M2-T03/T04、M4-E、M5 | FIPS、安全和供应链报告 |
| `NFR-REL-*` | M2/T03/M3/M6 | 故障注入、关闭、蓝绿/回滚 |
| `NFR-OBS-*` | M2-T02、M3-W04、M5 | 日志脱敏、metrics、探针测试 |
| `NFR-BUILD-*` | M2-T01、M5-T01~T05 | 多架构构建和镜像检查 |

## 14. 风险台账与触发动作

| 风险 | 触发信号 | 动作 | Owner |
| --- | --- | --- | --- |
| FIPS 不可认证 | M2 无法提供合规证据 | G2 判定 No-Go，评估批准工具链或终止 | Security |
| 路由/Schema 遗漏 | 差分出现无 fixture 接口 | 阻断对应波次，补目录和需求 | QA/Lead |
| JSON 兼容成本过高 | 同域大量 null/order 差异 | 增加 RawMessage 透传，禁止放宽比较 | Domain Lead |
| Session 并发缺陷 | race 或跨用户 case 失败 | 阻断合并和下一波次 | Auth Owner |
| 资源目标未达到 | M2/M5 RSS 或 P95 失败 | profile 后优化；仍失败则 Go/No-Go 复审 | Lead |
| Controller 负载上升 | 请求数/连接数高于基线 | 检查缓存、重试和请求合并；禁止盲目重试 | Client Owner |
| 周期超出 20% | 连续两次里程碑延期 | 重新估算和调序，不削减质量门禁 | PM/Lead |

## 15. 项目报告与评审节奏

- 每周发布一次状态：完成项、下周目标、指标、风险、阻断和估算变化；
- 每个波次举行一次契约/演示评审，展示真实 Scala/Go 差分报告；
- G2、G4、G5 是正式决策会议，结论和签字进入仓库或发布系统；
- 性能结果必须包含硬件、镜像 digest、Commit、数据集、命令和原始结果位置；
- 安全问题通过私有渠道处理，不把漏洞细节或凭据写入公开 Issue。

## 16. 修订记录

| 版本 | 日期 | 说明 |
| --- | --- | --- |
| 0.1 | 2026-08-01 | 基于需求 0.1 与设计 0.1 建立迁移开发计划初稿 |
