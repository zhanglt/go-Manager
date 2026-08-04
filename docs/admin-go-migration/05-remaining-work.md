# Admin Go 迁移未完成工作

最后更新：2026-08-04

## 1. 当前基线

Go/Gin 后端已经实现清单中的全部 263 个语义 HTTP 操作。当前契约 Manifest 包含 304 个
场景，覆盖 263/263 个路由，独立 Scala/Go 差分结果为 304/304 PASS。Go 单元测试、race、
vet、严格路由覆盖检查和临时 HTTPS 证书启动验证均已通过。因此，当前没有待迁移的 API
路由。

本文跟踪将兼容性 POC 转化为可发布 Scala/Pekko Admin 替代服务所需的剩余工作。路由覆盖
完成不等于已满足生产发布条件。

## 2. P0 发布阻断项

### RW-001 FIPS 工具链与镜像批准

- [ ] 获取 Security/FIPS Owner 对 Go 编译器、密码模块、基础镜像和证据采集流程的批准。
- [ ] 为全部支持架构构建普通版本和 FIPS 版本。
- [ ] 验证运行时 FIPS 状态，拒绝错误标记或未经批准的二进制。
- [ ] 归档编译器、模块、镜像 digest、SBOM、provenance 和测试证据。

验收：Security/FIPS Owner 签署证据包，发布制品不依赖未经批准的密码实现。

### RW-002 SAML/OIDC 安全闭环

- [x] 对 state、code、临时 Token、redirect URL 和 Cookie 处理进行威胁建模。
- [x] 验证一次性消费、过期、重放拒绝、并发登录隔离和多 Manager 副本行为。
- [x] 确定生产环境采用粘性会话；共享临时状态存储留作取消粘性时的后续替换。
- [x] 验证 Cookie 属性、固定 public redirect origin、Host 处理和 CSRF/session fixation 防护，且日志
  不得泄露凭据。
- [ ] 增加真实 IdP 支持的 SAML/OIDC E2E，覆盖成功、拒绝、超时、重放、登出和并发用户。

验收：安全评审通过，认证 E2E 不存在跨用户 Token 或 Cookie 泄漏。

实现记录（2026-08-04）：移除全局固定 `samlSso` 结果键，改用 256-bit 随机、短期、一次性
handoff capability；OIDC 将 Controller redirect 中的 state 与当前浏览器的 HttpOnly flow
Cookie 绑定并原子消费。Store 有 TTL/容量边界，Cookie 使用 `Secure`、`SameSite=Lax`、prefix
Path，敏感 capability 额外使用 `HttpOnly`；生产 callback origin 固定为 `MANAGER_PUBLIC_URL`。
单元与 race 测试覆盖并发隔离、过期、错误 state、重放、跨实例 fail-closed、Host poisoning 和
prefix。详细威胁模型、状态机、部署要求与残余测试见
[`06-sso-security-design.md`](06-sso-security-design.md)。真实 IdP E2E 与 Security Owner 签字仍是
本项最终关闭条件。

### RW-003 正式性能与稳定性门禁

- [ ] 扩展基准工具，使 Scala 和 Go 使用相同端口、Controller fixture、预热时间、运行时长、
  资源限制和采样间隔。
- [ ] 使用至少三次独立运行测量 idle、steady、burst、50 MiB Body、上传下载、缓存压力和
  Controller 故障场景。
- [ ] 以机器可读格式报告 RSS/HWM、CPU、吞吐、P50/P95/P99、goroutine/thread、FD、启动
  时间和峰值内存。
- [ ] 执行包含 Controller 间歇故障的 24 小时混合流量稳定性测试。
- [ ] 调查持续内存、goroutine、连接、文件或命令子进程增长。

验收：RSS 相比 Scala 至少降低 50%，吞吐不下降，P95 劣化不超过 10%，稳定性测试无泄漏、
崩溃、死锁或无界增长。

### RW-004 经批准的真实 Controller 验证

- [ ] 从经批准的 Controller 环境采集脱敏 fixture 或测试数据。
- [ ] 执行完整成功和错误路径契约测试，包括 gzip、Header、Cookie、联邦、大响应和有状态事务。
- [ ] 记录并批准每个预期差异，禁止引入大范围 Body/Header 忽略规则。
- [ ] 使用 Go 后端执行关键 UI 和 CLI 工作流。

验收：不存在未解释的 Scala/Go 差异，QA 批准关键路径 E2E 报告。

## 3. P1 工程工作

### RW-005 Angular 静态资源服务

- [x] 由 Go Manager 提供生产 Angular build。
- [x] 保留 `PATH_PREFIX`、SPA fallback、index redirect、资源 Content-Type、缓存策略和现有
  API/静态路由优先级。
- [x] 普通模式支持兼容的 version/MD5，FIPS-only 模式改用 SHA-256；支持构建产物中现有的
  预压缩 gzip。当前 build 不包含 Brotli 文件，因此不声明 Brotli 支持。
- [x] 增加 `/`、深层链接、缺失资源、prefix 模式、Scala gzip 兼容行为和安全 Header 测试。

验收：生产 UI 和深层链接不再依赖 Scala 服务即可加载，静态资源行为通过兼容测试。

完成记录（2026-08-03）：Angular production build 作为只读 `go:embed` 文件系统打入 Manager；
`IS_DEV=true` 返回原始 JavaScript，生产模式无条件返回对应 `.js.gz` 以兼容 Scala。SPA
fallback 仅接受无扩展名的 HTML 页面导航，缺失静态文件继续返回 404。单元测试覆盖
redirect、prefix、MIME、HEAD/Range、缓存、安全 Header、路径穿越和 API 优先级；真实 build
smoke 校验及全量 Go test/race/vet/build 作为本项验收证据。

#### Angular 路由初始化竞态审计（2026-08-04）

Go 后端响应速度高于原 Scala 服务后，暴露出部分页面在 AG Grid `onGridReady` 之前发起请求、
并在响应回调中直接访问尚未初始化的 `gridApi` 的前端竞态。该问题表现为计数已更新但表格
持续显示 `Loading...`，不是 Go API 响应数据或路由映射错误。

已审计 `admin/webapp/websrc/app/routes/` 下同时使用 AG Grid 和异步请求的组件，并完成以下修正：

- Network Rules、Groups、Response Rules：首次加载统一由 `onGridReady` 触发，并为后续刷新增加
  Grid Ready 守卫；
- Group DLP/WAF 及其配置弹窗：等待 Grid API 可用后再查询和写入 `rowData`；
- DLP Sensors、WAF Sensors：移除依赖固定 `200ms` 的主表、规则和 Pattern 联动，改由三个 Grid
  各自的 Ready 事件同步当前选择；
- Admission Rules：成功与错误响应均在 `onGridReady` 中设置行数据和空表 Overlay；
- Signature Verifiers：等待 Signature、Verifier 两个 Grid 均就绪后首次加载，并移除响应后的
  固定延时；
- Network Activities / Edge Details：IP 映射请求延后至 Grid Ready，响应后确定性写入并选择首行。

同时复核了 API Keys、Roles、Users、Compliance、Process、Vulnerabilities、Registry、
Controller、Enforcer 和 Scanner 等 Grid；这些组件通过 `[rowData]` 输入绑定、`ngOnChanges`
守卫或 Ready 回调设置数据，不存在本次同类竞态。

验证证据：相关 TypeScript Prettier check、`npm run lint:check`、Angular production build、
`git diff --check`、`go test ./...` 和新二进制构建均通过。production build hash 为
`20788b662a59ba4d`；新 build 已同步至 `admin-go/assets/web/root`，临时 Go Manager smoke 验证
`/index.html` 返回兼容重定向，新的 Network Activities chunk 返回 `200` 和 gzip 内容。
带真实 Controller 数据的登录后页面遍历仍归入 RW-004 UI E2E 验收。

### RW-006 生产容器与打包

- [x] 使用 Angular + Go + Python CLI 多阶段构建替换 Scala/JAR manager stage。
- [x] 更新 `Makefile`、Dockerfile、entrypoint、release workflow、OCI label 和构建缓存。
- [x] 按评审后的路径和权限包含 IP geolocation、CIS/NIST、support command、CLI、CA 证书和许可证；
  Manager TLS 证书继续由部署以只读文件挂载。
- [x] 以 UID/GID `1000:1000` 运行，并验证只读根文件系统。
- [x] 验证最终镜像不包含 Java runtime、SBT cache、Scala class 或 manager JAR。
- [x] 构建并 smoke-test 全部支持架构（`linux/amd64`、`linux/arm64`）的普通和 FIPS-only 镜像。

验收：签名的多架构镜像可正常启动并通过 smoke、安全、权限、SBOM、provenance 和镜像内容
检查。

完成记录（2026-08-03）：`package/Dockerfile` 使用 Node、Go 和 SUSE BCI builder 生成约
85--86 MB 的 BCI Micro runtime，`Makefile` 提供本地目录打包、普通/FIPS 构建、跨架构
cache-only 验证、push 和内容验证目标。builder/runtime 镜像固定多架构 digest；Go、npm、pip
和 ZYpp 使用 BuildKit cache，网络安装支持重试与可配置 proxy。release workflow 初始化 QEMU/buildx，并对 amd64/arm64
普通及 FIPS-only target 执行缓存构建；发布目标启用 SBOM 和 provenance。四个架构/模式组合
均已实际启动，验证 non-root、只读 rootfs、HTTP redirect、Python CLI import、OCI label、数据文件、
许可证和无 Java/JAR/class。FIPS smoke 发现并修复了 MD5 panic：普通模式保持兼容 MD5，
`fips140=only` 使用 SHA-256 cache/Gravatar identifier。

本地 OCI evidence 还验证 attestation manifest 同时包含 `https://spdx.dev/Document` SBOM 和
`https://slsa.dev/provenance/v1` provenance predicate。

Angular `npm ci` 当前仍报告 36 个既有依赖告警（3 low、8 moderate、23 high、2 critical）。
本项未进行可能破坏 UI 兼容性的大版本升级；依赖漏洞门禁和修复由 RW-007 跟踪，发布前必须
完成风险确认或升级。

本项完成不表示镜像已签名发布或取得 FIPS 认证；Security/FIPS Owner 的工具链批准、不可变 digest、
签名、SBOM/provenance 归档及 Release Candidate 签字仍分别由 RW-001 和 RW-010 跟踪。

### RW-007 持续集成门禁

- [ ] 将 `go test ./...`、`go test -race ./...`、`go vet ./...` 和 Python 迁移工具测试加入 PR CI。
- [ ] 使用 `--strict` 执行路由覆盖检查，并要求保持 263/263。
- [ ] 验证格式、生成基线一致性、`git diff --check`、依赖许可证、漏洞、secret、SBOM 和
  provenance。
- [ ] 对不适合每个 PR 执行的完整契约、性能 smoke 和稳定性测试增加 scheduled job。

验收：测试、race、覆盖率、安全或生成制品回归会阻止合并。

### RW-008 可观测性与运行限制

- [ ] 提供经过批准的 request rate、status、latency、Controller failure、cache capacity/
  eviction、goroutine 和长时间文件/命令操作指标，禁止使用凭据作为 label。
- [ ] 仅在公开 Listener、TLS、配置和本地数据可用后返回 ready。
- [ ] 定义 session、cache、request body、header、connection、timeout、debug file 和命令时长
  的生产限制。
- [ ] 为 5xx、登录失败、P95/P99、RSS、goroutine 和 Controller 可用性增加告警与 dashboard。

验收：运维可在用户可见故障阈值前发现饱和、泄漏、认证回归和 Controller 故障。

### RW-009 本地文件与 support command 加固

- [x] 确认最终 `/usr/local/bin/support` 接口、容器 capability、临时目录、输出 ownership 和
  清理策略。
- [x] 为 support command 子进程增加取消和超时控制。
- [x] 测试损坏资源、权限失败、超大文件、中断下载、子进程失败和关闭清理。
- [x] 确认发布镜像中的 IP geolocation 和 CIS/NIST 数据路径。

完成证据（2026-08-03）：Manager 使用固定绝对命令、owner-only `0700` 目录、`0600` 原子输出、
10 分钟硬超时、进程组取消和默认并发 2；凭据通过环境传入而不进入 argv。Go 故障测试覆盖
启动/退出/超时/替换/关闭、损坏 gzip、不安全权限、超限文件、目录失败和中断下载。RW-006
镜像证据已确认 support、两个 IP2Location CSV 及 `/usr/share/neuvector/CIS_NIST-MASTER.CSV`；
镜像验证脚本进一步强制 UID/GID `1000:1000`、`--cap-drop ALL`、只读 rootfs 和 `/tmp` tmpfs。

验收：文件和命令操作有界、可取消、相互隔离，且不会残留凭据、子进程或临时文件。

## 4. P2 发布与切换工作

### RW-010 完整发布资格验证

- [ ] 对 Release Candidate 镜像执行 contract、E2E、performance、stability、security、FIPS、
  upgrade 和 rollback 测试。
- [ ] 验证 Scala 到 Go 的配置、证书、CLI、UI 和 session 切换行为。
- [ ] 获取 QA、Security/FIPS、Performance、Release 和 Product Owner 签字。
- [ ] 为每个已接受限制记录影响、规避方式、责任人和目标版本。

验收：不存在 P0/P1 缺陷，全部 G5 发布责任人批准同一个不可变镜像 digest。

### RW-011 蓝绿切换与回滚

- [ ] 保留已验证的 Scala 镜像 digest 和配置备份。
- [ ] 将 Go 部署为绿色实例，执行 readiness、证书、版本、静态 UI、Controller 和只读 shadow
  检查。
- [ ] 在批准窗口切换新流量，禁止重复发送有副作用请求。
- [ ] 观察登录成功率、错误率、延迟、RSS/CPU、goroutine、cache、文件和命令指标。
- [ ] 演练自动回滚阈值并记录恢复时间。

验收：批准的观察窗口内未触发回滚条件，或回滚能在规定恢复时间内恢复 Scala 服务。

### RW-012 Scala 下线

- [ ] 在 Go 首个稳定发布周期内继续保留 Scala/Pekko 回滚能力。
- [ ] 归档最终兼容、性能、安全、FIPS、切换和回滚证据。
- [ ] 通过独立评审变更删除 Scala Admin、Pekko/SBT 依赖、JVM 镜像层和 JAR 构建路径。
- [ ] 保留契约 fixture，并更新贡献、构建、部署和运维文档。

验收：Go 已稳定运行一个完整发布周期且无未解决 P0/P1 回归，Scala 删除变更经过独立评审并
具备回滚证据。

## 5. 需要外部决策的事项

- [ ] Security/FIPS Owner：批准 Go/FIPS 工具链、密码模块、基础镜像和证据流程。
- [ ] QA/DevOps：提供代表性 Controller 数据集、负载模型、目标硬件和基准窗口。
- [x] Architecture/Product：首版采用进程内临时状态，生产 SSO 全链路使用粘性会话；取消粘性时
  必须引入支持原子消费的共享 Store。
- [ ] Security：确定 Controller 证书跳过验证是否继续作为兼容性例外。
- [ ] Release/Operations：确认 support command capability、临时存储、non-root 策略和回滚阈值。

## 6. 推荐执行顺序

1. 实现同条件 Scala/Go 基准工具，并在外部性能环境准备期间生成可重复的本地对比制品。
2. 完成 Angular 静态资源服务后，实施 RW-006 多阶段构建以自动刷新嵌入快照。
3. 增加强制 Go、Python 和严格路由覆盖 CI 门禁。
4. 在真实 IdP 与生产负载均衡环境验证 SAML/OIDC，并完成 Security Owner 评审。
5. 构建生产多阶段镜像，再完成 FIPS、真实 Controller、性能和安全资格验证。
6. 执行蓝绿切换，并在达到稳定发布退出条件前保留 Scala。

进度更新必须保留机器可读路由和契约报告。只有取得对应验收证据后才能勾选完成，不能仅以
代码实现作为完成依据。
