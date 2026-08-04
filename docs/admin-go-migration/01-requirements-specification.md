# Admin 后端 Go/Gin 迁移需求规格说明书

| 属性 | 内容 |
| --- | --- |
| 文档状态 | Draft，待架构、产品、安全、QA 共同评审 |
| 适用仓库 | `neuvector/manager` |
| 目标组件 | `admin` Scala/Pekko 后端及其使用的 `common` 代码 |
| 目标实现 | Go + Gin |
| 兼容策略 | 严格透明替换 |
| 最后更新 | 2026-08-01 |

## 1. 目的

本文档定义将 NeuVector Manager 的 Admin 后端由 Scala 3、Pekko HTTP 和 JVM
迁移到 Go/Gin 的业务、接口、质量、安全及交付要求。本文档是迁移范围和验收的
最高优先级基线；设计文档和开发计划不得放宽本文要求。

## 2. 背景与现状

Admin 是 Angular 管理界面与 NeuVector Controller REST API 之间的 BFF。它同时负责：

- 对外提供 263 个语义 HTTP 操作（源码含 262 个显式方法 DSL 出现点），覆盖认证、联邦、
  资产、策略、风险和通知等领域；
- 向 Controller `/v1`、`/v2` 和联邦代理路径发起 HTTPS 请求；
- 转换、聚合、分页、排序部分 Controller JSON 数据；
- 保存登录 Token、SUSE Cookie、当前联邦集群和若干 UI 缓存；
- 提供 Angular 静态资源、版本重定向、预压缩 JavaScript 和安全响应头；
- 加载 IP 地理位置和 CIS/NIST 本地数据；
- 管理服务端 TLS、自签名证书和 FIPS Java 配置。

当前 Scala 与共享代码约 17,600 行，包含约 423 个数据类型和近 500 处 JSON
编解码定义。运行产物是约 100 MB 的 fat JAR，入口设置 `-Xms256m -Xmx2048m`。
现有自动化后端测试仅覆盖两个缓存 suite，REST shell 测试依赖真实环境，因此迁移
首先需要补齐黑盒契约基线。

## 3. 目标、成功标准与非目标

### 3.1 目标

- **OBJ-001**：使用受支持的 Go 版本和 Gin 替换 Scala Admin 后端。
- **OBJ-002**：保持 Angular、CLI、Controller 和部署配置无感知。
- **OBJ-003**：代表性负载下常驻内存 RSS 相比 Scala 基线降低至少 50%。
- **OBJ-004**：移除最终运行镜像中的 JVM、Pekko、Scala 和 fat JAR。
- **OBJ-005**：提高并发安全性、可测试性、构建速度和故障可诊断性。

### 3.2 成功标准

- 所有已登记 API 契约和关键 UI E2E 用例通过；
- 相同工作负载下吞吐不下降，P95 延迟相对基线劣化不超过 10%；
- 普通模式和 FIPS 模式均通过安全与发布验收；
- amd64、arm64 镜像构建、启动、升级和回滚通过；
- Go 版本完成一个发布周期稳定运行后，Scala 运行链路可删除。

### 3.3 非目标

- 不重新设计公开 API，不改变 URL、HTTP 方法或业务语义；
- 不修改 Angular 页面交互或 Controller API；
- 不以迁移为由修复可观察到的历史兼容行为；确需修复时另立变更需求；
- 不新增数据库、消息队列或外部 Session 服务；
- 不重写 `cli/` Python 工具，除非其启动路径因移除 Java 必须调整；
- 不把未被 Admin 实际使用的历史 Cassandra/Kafka 配置迁入 Go。

## 4. 参与角色与文档治理

| 角色 | 职责 |
| --- | --- |
| Product Owner | 确认兼容边界、业务验收与发布窗口 |
| Backend Owner | 批准 API 语义、Controller 调用和迁移实现 |
| UI Owner | 批准 Angular E2E 覆盖和 UI 行为兼容性 |
| Security/FIPS Owner | 批准密码模块、镜像、证书和 FIPS 证据 |
| QA | 维护契约、集成、性能、升级及回滚测试 |
| Release/DevOps | 维护多架构构建、镜像签名和发布门禁 |

需求使用 `FR-*`、非功能需求使用 `NFR-*`、接口契约使用 `IF-*`、验收条件使用
`AC-*`。任何需求变更必须记录原因、影响、批准人，并同步设计与开发计划中的引用。

## 5. 系统边界与外部依赖

```text
Browser / Python CLI
        |
        | HTTPS, existing Manager API
        v
Go/Gin Admin ---- embedded Angular assets
        |
        | HTTPS, X-Auth-Token / X-R-Sess
        v
NeuVector Controller (/v1, /v2, /v1/fed/cluster/...)
```

外部依赖包括 Controller DNS/端口、Manager 证书和私钥、FIPS 主机状态、Angular
构建产物、IP2Location CSV、CIS/NIST CSV、支持日志脚本以及环境变量。

## 6. 接口兼容需求

### 6.1 通用契约

- **IF-001**：保留所有现有路径、方法、路径前缀、查询参数名称及大小写。
- **IF-002**：需要认证的接口继续读取 `Token` Header，并向 Controller 转换为
  `X-Auth-Token`；缺失 Header 时保持现有拒绝行为。
- **IF-003**：保持 `X-Transaction-Id`、`X-As-Standalone`、`X-Nv-Page`、
  `X-R-Sess`、`X-R-SSO` 和 `R_SESS` Cookie 的转发与生成语义。
- **IF-004**：JSON 字段名、缺失字段、显式 `null`、默认值、数字精度、数组顺序和
  Content-Type 必须与 Scala 可观察行为一致。
- **IF-005**：保持 Controller 成功、重定向、4xx、5xx、超时和连接失败到 Manager
  响应的状态码、Body 与 Header 映射。
- **IF-006**：保持 gzip 请求协商、Controller gzip 解码和静态 `.js.gz` 服务行为。
- **IF-007**：保持 multipart 上传、二进制下载和 50 MB 最大请求体限制；不得将大文件
  无界读入内存。
- **IF-008**：所有接口支持可选 `PATH_PREFIX`，空白值视为未配置，路径片段须安全转义。

### 6.2 API 域目录

下表是迁移契约目录。详细参数和 Body schema 以现有路由、Scala JSON protocol、
Controller 响应及迁移前采集的 golden 样本共同确定；四者冲突时，以生产可观察行为为准。

| 域 | 必须保持的路由族与行为 | 主要风险 |
| --- | --- | --- |
| Authentication | `/auth`、`/heartbeat`、`/self`、`/openId_auth`、`/token_auth_server`、`/token_auth_server_slo`、`/samlslo` | Cookie、回调、重定向、Host/IP、Token 状态 |
| Account | `/gravatar`、`/eula`、`/rebrand`、`/role2`、`/api_key`、`/user`、`/version`、`/token`、`/license`、`/password-profile`、`/server`、`/debug` | 无 Token 公共接口与权限数据转换 |
| Federation | `/fed/member`、`switch`、`summary`、`promote`、`demote`、`join_token`、`join`、`leave`、`config`、`deploy` | Token 对应的当前集群与联邦 URL |
| Dashboard | `/multi-cluster-summary`、`/dashboard/alerts`、`details`、`scores`、`notifications` | 多请求聚合、缓存、排序和评分模型 |
| Device | `/enforcer`、`/single-enforcer`、`/controller`、`/scanner`、`/summary`、`/usage`、`/webhook`、`/config`、`/config-v2`、`/remote_repository`、`/host/*`、`/file/*`、`/debug/*`、`/bench/*`、`/csp-support` | multipart、支持日志、命令调用、下载 |
| Group | `/group-list`、`/group/*`、`/service/*`、`/processProfile`、`/fileProfile`、`/filePreProfile`、`/dlp/*`、`/waf/*` | 导入导出、分页、联邦事务 Header |
| Notification | `/ip-geo`、`/event`、`/incident`、`/violation/*`、`/audit*`、`/threat/*`、`/network/*`、`/security-events*`、`/notification/accept` | 图计算、布局/黑名单缓存、IP 数据 |
| Policy/Scan | `/fed-deploy`、`/conditionOption`、`/unquarantine`、`/responseRule`、`/responsePolicy/*`、`/policy/*`、`/scan/*`、`/admission/*` | 最大路由面、JSON 变换、导入导出 |
| Risk/Compliance | `/scanned-assets`、`/vulasset`、`/risk/cve/*`、`/complianceNIST`、`/compliance/*` | 大型模型、过滤聚合、NIST 本地数据 |
| Sigstore | `/sigstore`、`/verifier` | Root of Trust 与 Verifier CRUD |
| Workload | `/workload/*`、`/container/*`、`/sniffer/*`、`/domain` | 分页缓存、PCAP 下载、兼容性较高 |

### 6.3 静态资源契约

- **IF-020**：根路径永久重定向到 `index.html?v=<hash>`，Hash 算法及长度保持兼容。
- **IF-021**：版本不匹配的 `index.html` 请求重定向到当前版本地址。
- **IF-022**：生产模式优先提供预压缩 JavaScript，开发模式提供原始资源。
- **IF-023**：保持 `X-Frame-Options`、HSTS、CSP、`X-Content-Type-Options`、
  `X-XSS-Protection` 和 Cache-Control 的条件规则。
- **IF-024**：`favicon.ico` 保持现有 Not Found 行为。

## 7. 功能需求

### 7.1 配置与生命周期

- **FR-CFG-001**：支持现有环境变量：`CTRL_SERVER_IP`、`CTRL_SERVER_PORT`、
  `MANAGER_SERVER_PORT`、`MANAGER_SSL`、`HTTP_MAX_HEADER_LENGTH`、`PATH_PREFIX`、
  `IS_DEV`、`GRAVATAR_ENABLED`、`ENABLE_GPU` 及 UI 定制变量。
- **FR-CFG-002**：未设置变量时使用与 `application.conf` 等价的默认值。
- **FR-CFG-003**：启动时校验端口、请求限制、证书路径和路径前缀；错误必须脱敏并
  使进程以非零状态退出。
- **FR-CFG-004**：支持 SIGTERM/SIGINT 优雅停止，不再接收新请求，并在可配置期限内
  完成或取消在途请求。
- **FR-CFG-005**：产品版本由构建参数注入，并用于 `/version` 与静态资源 Hash。

### 7.2 认证与会话

- **FR-AUTH-001**：支持用户名/密码、SUSE/Rancher SSO、OIDC 和 SAML 登录流程。
- **FR-AUTH-002**：解析 Controller Token 响应，计算 Gravatar MD5，转换全局和域角色
  数字映射，并保持密码过期/重置字段。
- **FR-AUTH-003**：为 Token 保存用户信息、SUSE Cookie、当前联邦集群；登出时原子清理。
- **FR-AUTH-004**：Token 校验、心跳、登出、SAML SLO 和 OIDC state 处理与当前行为一致。
- **FR-AUTH-005**：并发请求不得造成 Token、Cookie 或集群选择串线；运行 `go test -race`
  必须无数据竞争。
- **FR-AUTH-006**：保留认证错误码 14、47、48、50 的特殊消息和状态映射。

### 7.3 Controller 客户端

- **FR-CTL-001**：复用有界 HTTP Transport 和连接池，不为每个请求创建 Transport。
- **FR-CTL-002**：按 Token 的集群选择计算 `/v1`、`/v2` 或联邦目标 URL。
- **FR-CTL-003**：对每个请求传播 `context.Context`、截止时间和客户端取消。
- **FR-CTL-004**：支持 JSON、multipart、流式上传、流式下载和响应透传。
- **FR-CTL-005**：日志记录方法、目标、状态、耗时和 request ID，但屏蔽 Token、Cookie、
  Authorization、密码和请求中的敏感字段。
- **FR-CTL-006**：保持当前 Controller 证书校验策略作为显式配置，并记录其安全债务；
  不得在代码中隐式关闭校验。

### 7.4 数据处理与缓存

- **FR-DATA-001**：纯代理接口不得无理由反序列化并重编码 JSON。
- **FR-DATA-002**：必须复刻 Dashboard、Policy、Risk、Notification 等领域的聚合、过滤、
  排序、评分、分页和字段派生逻辑。
- **FR-CACHE-001**：提供并发安全的内存缓存接口，覆盖分页、JSON、图布局、黑名单和
  支持日志授权数据。
- **FR-CACHE-002**：容量、淘汰、TTL 和持久性由配置控制；默认容量不得无界增长。
- **FR-CACHE-003**：登录、登出、数据变更和集群切换触发与现有逻辑等价的失效操作。
- **FR-LOCAL-001**：启动时加载 IPv4/IPv6 地理位置与 CIS/NIST CSV；损坏或缺失时输出
  明确错误，不返回部分初始化状态。

### 7.5 静态资源和本地操作

- **FR-STATIC-001**：Angular 生产产物随 Go 服务交付，运行时不依赖 Node.js。
- **FR-STATIC-002**：未知 API 不得错误回退到 Angular 页面；静态路由兼容 SPA 资源路径。
- **FR-OPS-001**：保留支持日志、benchmark、PCAP 和配置导入导出流程的权限与文件语义。
- **FR-OPS-002**：外部命令必须使用参数数组和 Context，不通过拼接 Shell 字符串执行；
  临时文件路径、权限、清理和并发数量必须受控。

## 8. 非功能需求

### 8.1 性能和资源

- **NFR-PERF-001**：在同一节点、数据集、并发模型和测试脚本下建立 Scala 基线。
- **NFR-PERF-002**：空闲及代表性负载稳定阶段 RSS 均至少降低 50%。
- **NFR-PERF-003**：吞吐不得低于 Scala 基线，P95 延迟劣化不得超过 10%。
- **NFR-PERF-004**：连续 24 小时稳定性测试无持续内存增长、goroutine 泄漏或连接泄漏。
- **NFR-PERF-005**：缓存、上传下载和最大 Body 场景必须单独记录峰值内存。

### 8.2 安全与 FIPS

- **NFR-SEC-001**：首个正式版本必须通过 NeuVector/SUSE 适用的 FIPS 构建和运行验收。
- **NFR-SEC-002**：支持现有 PEM 证书链及 PKCS#1、PKCS#8 RSA 私钥。
- **NFR-SEC-003**：证书缺失时生成等价 RSA 2048/SHA-256 自签名证书，包含所需 SAN、
  Key Usage 和 Extended Key Usage。
- **NFR-SEC-004**：禁止在日志、panic、指标标签或错误响应中泄露凭据和个人数据。
- **NFR-SEC-005**：依赖通过漏洞、许可证和 SBOM 扫描；生产构建禁用公开 pprof。
- **NFR-SEC-006**：保留 CSP/HSTS 等响应头，并通过自动化测试校验所有条件分支。

### 8.3 可靠性、可观测性和可维护性

- **NFR-REL-001**：Controller 故障不得导致 Admin 进程退出或无限等待。
- **NFR-REL-002**：所有 goroutine、队列、缓存和并发外部操作必须有明确上限。
- **NFR-REL-003**：服务支持蓝绿部署和上一版本镜像回滚，不执行不可逆数据迁移。
- **NFR-OBS-001**：日志采用结构化格式，至少包含时间、级别、request ID、方法、规范化
  路由、状态和耗时；兼容现有日志采集。
- **NFR-OBS-002**：提供仅内部可访问的 liveness/readiness；readiness 验证启动资源已加载，
  不以 Controller 短暂不可用作为进程死亡条件。
- **NFR-MAINT-001**：核心包单元覆盖率不低于 80%，整体 Go 行覆盖率不低于 70%；
  安全、认证和 URL 选择逻辑必须达到分支覆盖。
- **NFR-MAINT-002**：代码通过 `gofmt`、`go vet`、`go test -race` 和项目选定的静态检查。

### 8.4 构建与平台

- **NFR-BUILD-001**：使用可复现的 Go module 和多阶段容器构建，固定直接依赖版本。
- **NFR-BUILD-002**：正式支持 linux/amd64、linux/arm64，与当前发布 workflow 一致。
- **NFR-BUILD-003**：最终镜像保留 Python CLI 所需运行时，但不得包含 JDK/JRE。
- **NFR-BUILD-004**：版本、Commit、许可证、SBOM、provenance 和现有 OCI Label 保持可用。

## 9. 数据、并发与兼容规则

- Session 与缓存均为进程本地状态；首版维持现有单实例/粘性会话假设，不引入分布式状态。
- 所有共享 Map 必须封装，禁止 Handler 直接读写；复合操作必须具备原子性。
- Token 只作为不透明字符串；不得解析出合同未要求的安全结论。
- 请求级 Controller base URL 只在调用栈中传递，不写入 Token 共享状态。
- Go `encoding/json` 与 Spray JSON 的差异必须通过 golden test 消除，禁止仅凭 struct tag
  推断已兼容。
- 缓存可以更换实现，但命中、失效和用户可观察结果必须保持一致。

## 10. 测试与验收条件

| 验收编号 | 条件 | 证据 |
| --- | --- | --- |
| AC-001 | 路由清单与代码扫描结果一致，无遗漏公开接口 | 自动路由清单、评审记录 |
| AC-002 | Scala 与 Go 对同一请求返回兼容结果 | 差分契约报告 |
| AC-003 | 登录、OIDC、SAML、SUSE SSO、登出和超时通过 | 集成/E2E 报告 |
| AC-004 | 联邦切换与多用户并发无状态串线 | race、并发测试报告 |
| AC-005 | gzip、重定向、安全头、`PATH_PREFIX` 与静态资源一致 | HTTP golden tests |
| AC-006 | 上传、下载、50 MB Body、慢客户端和取消请求通过 | 集成及资源报告 |
| AC-007 | Controller 401/408/503、断连、证书异常均符合映射 | 故障注入报告 |
| AC-008 | RSS、吞吐、P95 和 24 小时长稳达到目标 | 可复现基准报告 |
| AC-009 | 普通与 FIPS 镜像通过安全审批 | 扫描、FIPS 证据、签字 |
| AC-010 | amd64/arm64 构建、升级、蓝绿和回滚成功 | 发布演练记录 |

## 11. 风险与约束

| 风险 | 级别 | 控制措施 |
| --- | --- | --- |
| 缺少正式 API schema，历史行为隐含在代码中 | 高 | 路由扫描、流量样本、Scala/Go 差分测试 |
| Go FIPS 工具链或密码模块不满足产品认证 | 高 | POC 阶段前置验证，作为 Go/No-Go 门禁 |
| JSON 空值、数字、顺序差异导致 UI 回归 | 高 | 透传优先、golden fixtures、E2E |
| 认证和联邦状态并发错误 | 高 | 封装 Session、race test、混合并发场景 |
| 一次性切换面过大 | 中 | 按域开发、整体蓝绿发布、保留回滚镜像 |
| 新缓存语义与 Ehcache 不一致 | 中 | 明确容量/TTL/失效，命中与压力测试 |
| 镜像仍含 Python，体积降幅低于预期 | 中 | 分别衡量 Java 移除和完整镜像收益 |

## 12. 需求追踪入口

设计文档必须逐组映射本文件中的需求；开发计划必须为每项需求指定实现任务和测试任务。
最低追踪关系如下：

| 需求组 | 设计章节 | 开发阶段 |
| --- | --- | --- |
| `IF-*`、`FR-CTL-*` | 路由、Handler、Controller Client | 契约平台及全部业务波次 |
| `FR-AUTH-*` | Session、认证时序、安全 | 基础设施及认证波次 |
| `FR-CACHE-*`、`FR-DATA-*` | Cache、Codec、领域 Service | 各业务域波次 |
| `NFR-PERF-*` | 并发、流式 I/O、容量 | POC、性能与长稳阶段 |
| `NFR-SEC-*` | TLS、FIPS、日志、供应链 | POC 门禁及发布工程 |
| `NFR-BUILD-*` | 构建、镜像、平台 | 发布工程阶段 |

## 13. 待评审项

以下事项不改变当前需求，但必须在需求冻结前由责任角色给出证据或批准：

1. 确认 SUSE/NeuVector 认可的 Go FIPS 工具链、基础镜像和验证流程；
2. 从目标部署采集真实 Scala RSS、CPU、延迟、吞吐和峰值请求基线；
3. 确认生产部署是否始终为单 Manager 副本或具备粘性会话；
4. 确认 Controller 跳过证书校验属于必须兼容行为还是已有安全例外；
5. 确认支持日志脚本、临时目录和容器权限在 Go 镜像中的最终约束。

## 14. 附录 A：API 操作矩阵

本矩阵从现有 Pekko Router 静态提取，是 M0 自动路由清单的人工核对基线。`Token` 是除
注明公共接口外的通用 Header；表中的 `tx` 表示 `X-Transaction-Id`，`standalone`
表示 `X-As-Standalone`。请求和响应 Body 的完整 golden schema 在 M0/M1 采集。

### A.1 Authentication 与 Account

| 路径 | 方法 | 关键输入/行为 |
| --- | --- | --- |
| `/auth` | POST, DELETE | 登录；登出需要 Token |
| `/heartbeat` | PATCH | Token 校验 |
| `/self` | GET | `isOnNV?`, `isRancherSSOUrl?`, `R_SESS?` |
| `/openId_auth` | GET, PATCH | `code?`, `state?`, `serverName?` |
| `/token_auth_server` | GET, PATCH, POST | SAML server、callback、登录 |
| `/samlslo` | GET, POST | SAML SLO response |
| `/token_auth_server_slo` | GET | SAML logout |
| `/gravatar` | GET | 公共接口 |
| `/eula` | GET, POST | GET 为公共接口，`isSSO?`；POST 需要 Token |
| `/rebrand` | GET | 公共接口 |
| `/role2/permission-options` | GET | 权限选项 |
| `/role2` | GET, POST, PATCH, DELETE | `name?`；删除要求 `name` |
| `/api_key` | GET, POST, DELETE | `name?`；删除要求 `name` |
| `/user` | GET, POST, PATCH, DELETE | `name?`, `userId` |
| `/version` | GET | Manager 版本 |
| `/token` | POST | Token 校验/转换 |
| `/license` | GET, POST | 查询、申请 License |
| `/license/update` | POST | 更新 License |
| `/password-profile/public` | GET | 公开字段密码策略，仍需 `Token` |
| `/password-profile/user` | POST | 用户锁定/解锁 |
| `/password-profile` | GET, PATCH | 密码策略 |
| `/server` | GET, POST, PATCH, DELETE | LDAP/Auth Server 管理 |
| `/debug` | POST | 认证配置测试 |

### A.2 Federation、Dashboard、Sigstore

| 路径 | 方法 | 关键输入/行为 |
| --- | --- | --- |
| `/fed/member` | GET | 联邦成员信息 |
| `/fed/switch` | GET | `id?`，更新 Token 当前集群 |
| `/fed/summary` | GET | `id` |
| `/fed/promote`, `/fed/demote` | POST | 联邦角色变更 |
| `/fed/join_token` | GET | 获取加入 Token |
| `/fed/join`, `/fed/leave` | POST | 加入/离开联邦 |
| `/fed/config` | PATCH | 联邦配置 |
| `/fed` | DELETE | `id` |
| `/fed/deploy` | POST | 部署联邦规则 |
| `/multi-cluster-summary` | GET | `clusterId?` |
| `/dashboard/alerts` | GET | 系统告警 |
| `/dashboard/details` | GET | `isGlobalUser?`, `domain?` |
| `/dashboard/scores` | GET, POST | `isGlobalUser?`, `domain?`；查询/计算评分 |
| `/dashboard/notifications` | GET | `domain?` |
| `/sigstore` | GET, POST, PATCH, DELETE | 删除要求 `rootOfTrustName` |
| `/verifier` | GET, POST, PATCH, DELETE | `rootOfTrustName`, 删除另需 `verifierName` |

### A.3 Device 与 Workload

| 路径 | 方法 | 关键输入/行为 |
| --- | --- | --- |
| `/enforcer`, `/controller` | GET | `id?` |
| `/single-enforcer` | GET | `id` |
| `/scanner`, `/summary`, `/ibmsa_setup`, `/usage` | GET | 设备状态 |
| `/webhook` | POST, PATCH, DELETE | `scope?`, `name` |
| `/config` | GET, PATCH | `scope?` |
| `/config-v2` | GET, PATCH | `scope?`, GET 另含 `source?` |
| `/remote_repository` | POST, PATCH, DELETE | 删除要求 `name` |
| `/host` | GET | `id?` |
| `/host/scan-report` | POST | 报告下载 |
| `/host/workload`, `/host/compliance` | GET | `id` |
| `/file/config` | GET, POST | `id` 或 multipart；POST 使用 `tx`, `standalone` |
| `/file/export-config-fed` | POST | 联邦配置导出 |
| `/file/config-fed` | POST | multipart、`tx`, `standalone` |
| `/debug` | GET, POST | 支持日志状态/创建 |
| `/debug/check` | GET | 支持日志就绪检查 |
| `/bench/docker`, `/bench/kubernetes` | GET, POST | `id` |
| `/csp-support` | POST | CSP 支持文件下载 |
| `/workload` | GET, POST | `id?`；查询/隔离更新 |
| `/workload/scan-report` | POST | 报告下载 |
| `/workload/scanned` | GET | `start?`, `limit?` |
| `/workload/workload-by-id` | GET | `id?` |
| `/workload/monitor` | GET | `id`, `monitor`，保持历史方法 |
| `/workload/compliance` | GET | `id` |
| `/container` | GET | `id?` |
| `/container/process`, `/container/processHistory` | GET | `id` |
| `/sniffer` | GET, POST, PATCH, DELETE | `id` 按操作要求提供 |
| `/sniffer/pcap` | GET | `id`，二进制下载 |
| `/domain` | GET, PATCH, POST | namespace/domain 配置 |

### A.4 Group、DLP 与 WAF

| 路径 | 方法 | 关键输入/行为 |
| --- | --- | --- |
| `/group-list` | GET | `scope?`, `f_kind?` |
| `/group/custom_check` | GET, PATCH | GET 要求 `name` |
| `/group/export`, `/group/export-fed` | POST | local/fed 导出 |
| `/group/import`, `/group/import-fed` | POST | `tx`，导入配置 |
| `/group` | GET, POST, PATCH, DELETE | GET 含分页/过滤参数；删除含 `name`, `scope?` |
| `/service/all` | PATCH | 系统请求 |
| `/service` | GET, POST, PATCH | `name?`, `with_cap?` |
| `/processProfile` | GET, PATCH | GET 以 `name` 或 `scope?` 区分；PATCH 含 `scope?` |
| `/fileProfile` | GET, PATCH | GET 以 `name` 或 `scope?` 区分；PATCH 含 `scope?` |
| `/filePreProfile` | GET | `name` |
| `/dlp/sensor` | GET, POST, PATCH, DELETE | `name?`, `scope?`；删除要求 `name` |
| `/dlp/sensor/export`, `/dlp/sensor/export-fed` | POST | 配置导出 |
| `/dlp/sensor/import`, `/dlp/sensor/import-fed` | POST | `tx`，配置导入 |
| `/dlp/group` | GET, PATCH | GET 要求 `name` |
| `/waf/sensor` | GET, POST, PATCH, DELETE | `name?`, `scope?`；删除要求 `name` |
| `/waf/sensor/export`, `/waf/sensor/export-fed` | POST | 配置导出 |
| `/waf/sensor/import`, `/waf/sensor/import-fed` | POST | `tx`，配置导入 |
| `/waf/group` | GET, PATCH | GET 要求 `name` |

### A.5 Notification 与 Network

| 路径 | 方法 | 关键输入/行为 |
| --- | --- | --- |
| `/ip-geo` | PATCH | IP 数组到国家信息 |
| `/event`, `/incident` | GET | 日志查询 |
| `/violation` | GET | 违规日志 |
| `/violation/top` | GET | `category` |
| `/violation/track` | POST | 跟踪违规 |
| `/audit` | GET | Audit 日志 |
| `/audit2` | GET | `start?`, `limit?` |
| `/threat` | GET | `id?` |
| `/threat/track` | POST | 跟踪 Threat |
| `/threat/top` | GET | Top Threat |
| `/network/session` | GET | `id` |
| `/network/conversation` | DELETE | `from`, `to` |
| `/network/endpoint` | PATCH, DELETE | 删除要求 `id` |
| `/network/history` | GET | `from`, `to` |
| `/network/graph` | GET, POST | GET 要求 `user`；POST 保存布局 |
| `/network/graph/layout` | GET | `user` |
| `/network/graph/blacklist` | GET, POST | GET 要求 `user` |
| `/security-events`, `/security-events2` | GET | 安全事件聚合 |
| `/notification/accept` | POST | 接受通知 |

### A.6 Policy、Scan 与 Admission

| 路径 | 方法 | 关键输入/行为 |
| --- | --- | --- |
| `/fed-deploy` | POST | 联邦规则部署 |
| `/conditionOption` | GET | `scope?` |
| `/unquarantine` | POST | 取消隔离 |
| `/responseRule` | GET | `id` |
| `/responsePolicy` | GET, POST, PATCH, DELETE | `scope?`, `id?` |
| `/responsePolicy/export`, `/responsePolicy/export-fed` | POST | 配置导出 |
| `/responsePolicy/import`, `/responsePolicy/import-fed` | POST | `tx`，配置导入 |
| `/policy` | GET, PATCH, DELETE | `scope?`, `start?`, `limit?`, `id?` |
| `/policy/application` | GET | Application 列表 |
| `/policy/rule` | GET, POST, PATCH | GET 要求 `id` |
| `/policy/graph` | GET | Policy graph |
| `/policy/promote` | POST | Policy promote |
| `/scan/status` | GET | Scanner 状态 |
| `/scan/workload`, `/scan/host`, `/scan/platform` | GET, POST | `id?`/`platform?`, `show?`；POST 触发扫描 |
| `/scan/config` | GET, POST | Scan 配置 |
| `/scan/registry` | GET, POST, PATCH, DELETE | `name?`；删除要求 `name` |
| `/scan/registry/test` | POST, DELETE | `tx`, `name` |
| `/scan/registry/repo` | GET, POST, DELETE | `name` |
| `/scan/registry/fed-repo` | GET | `fed_repo` |
| `/scan/registry/image` | GET | `name`, `imageId`, `show?` |
| `/scan/registry/type` | GET | Registry 类型 |
| `/scan/registry/layer` | GET | `name`, `imageId`, `show?` |
| `/scan/top` | GET | Top 扫描结果 |
| `/admission/rules` | GET | `scope?` |
| `/admission/rule` | POST, PATCH, DELETE | 删除含 `scope?`, `id` |
| `/admission/options` | GET | `scope?` |
| `/admission/state` | GET, PATCH | Admission 状态 |
| `/admission/test` | GET | Test 配置 |
| `/admission/matching-test` | POST | 匹配测试 |
| `/admission/export`, `/admission/export-fed` | POST | 配置导出 |
| `/admission/import`, `/admission/import-fed` | POST | `tx`，配置导入 |
| `/admission/promote` | POST | Promote 配置 |

### A.7 Risk 与 Compliance

| 路径 | 方法 | 关键输入/行为 |
| --- | --- | --- |
| `/scanned-assets` | GET, POST | GET 含过滤、分页、排序参数 |
| `/vulasset` | GET, POST | GET 含过滤、分页、排序参数 |
| `/risk/cve` | GET | `show?` |
| `/risk/cve/assets-view` | PATCH | `queryToken?` |
| `/risk/cve/profile` | GET, PATCH | CVE Profile |
| `/risk/cve/profile/entry` | POST, PATCH, DELETE | `name`, `profile_name`, `entry_id` |
| `/risk/cve/profile/export` | POST | Profile 导出 |
| `/risk/cve/profile/import` | POST | `tx`, `option` |
| `/risk/complianceNIST` | POST | CIS/NIST 映射 |
| `/risk/compliance` | GET | Compliance 聚合 |
| `/risk/compliance/template` | GET | Template 列表 |
| `/risk/compliance/available_filter` | GET | 可用过滤器 |
| `/risk/compliance/profile` | GET, PATCH | `name?` |
| `/risk/compliance/profile/export` | POST | Profile 导出 |
| `/risk/compliance/profile/import` | POST | `tx` |

## 15. 修订记录

| 版本 | 日期 | 说明 |
| --- | --- | --- |
| 0.1 | 2026-08-01 | 根据现有 Scala 实现和已确认迁移策略建立初稿 |
