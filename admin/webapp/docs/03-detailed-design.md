# NeuVector Web 前端详细设计说明书

## 1. 文档范围与代码基线

本文将概要设计落实到路由、组件、服务、数据对象和接口级别，供开发维护、测试设计和问题定位使用。
目录均相对于 `admin/webapp/websrc/app/`。现有工程约有 404 个组件、109 个模块、70 个服务和 411 个单元测试文件；
本文聚焦稳定的业务边界和关键调用链，不逐一罗列纯展示单元格组件。

## 2. 启动与应用外壳

### 2.1 启动顺序

1. `main.ts` 引导 `AppModule`。
2. `AppModule` 注册 `CoreModule`、`FrameModule`、`NvCommonModule`、`RoutesModule`、翻译加载器和 HTTP 拦截器。
3. `AppModule` 将浏览器 `window` 和 `HttpClient` 注入 `GlobalVariable`，并保存初始 Hash 深链接。
4. `RoutesModule` 注册 Hash 路由，并将静态菜单写入 `MenuService`。
5. 登录成功后 `FrameComponent` 初始化摘要、版本、品牌配置及框架布局。

### 2.2 框架组件

| 组件                                   | 责任                                                  |
| -------------------------------------- | ----------------------------------------------------- |
| `frame/frame.component`                | 登录后布局容器，协调 Header、Sidebar、内容与 Footer。 |
| `frame/header/header.component`        | 用户菜单、角色显示、通知、集群列表/切换、联邦入口。   |
| `frame/header/navsearch`               | 页面导航搜索。                                        |
| `frame/sidebar/sidebar.component`      | 菜单树、折叠/悬浮交互、平台相关菜单裁剪。             |
| `frame/custom-header`、`custom-footer` | 部署方品牌内容和颜色。                                |
| `core/themes`、`switchers`             | 主题与布局开关。                                      |
| `core/translator`                      | 当前语言和翻译初始化。                                |

### 2.3 路由表

| 路由                   | 页面模块                | 核心页面组件                    |
| ---------------------- | ----------------------- | ------------------------------- |
| `/login`、`/eula`      | `routes/pages`          | `LoginComponent`                |
| `/logout`              | `routes/pages`          | `LogoutComponent`               |
| `/profile`             | `settings/profile`      | `ProfileComponent`              |
| `/dashboard`           | `dashboard`             | `DashboardComponent`            |
| `/graph`               | `network-activities`    | `NetworkActivitiesComponent`    |
| `/platforms`           | `platforms`             | `PlatformsComponent`            |
| `/domains`             | `namespaces`            | `NamespacesComponent`           |
| `/hosts`               | `nodes`                 | `NodesComponent`                |
| `/workloads`           | `containers`            | `ContainersComponent`           |
| `/regScan`             | `registries`            | `RegistriesComponent`           |
| `/signature-verifiers` | `signature-verifiers`   | `SignatureVerifiersComponent`   |
| `/controllers`         | `system-components`     | `SystemComponentsComponent`     |
| `/admission-control`   | `admission-rules-page`  | `AdmissionRulesPageComponent`   |
| `/group`               | `groups-page`           | `GroupsPageComponent`           |
| `/policy`              | `network-rules-page`    | `NetworkRulesPageComponent`     |
| `/response-policy`     | `response-rules-page`   | `ResponseRulesPageComponent`    |
| `/dlp-sensors`         | `dlp-sensors-page`      | `DlpSensorsPageComponent`       |
| `/waf-sensors`         | `waf-sensors-page`      | `WafSensorsPageComponent`       |
| `/scan`                | `vulnerabilities`       | `VulnerabilitiesComponent`      |
| `/cveProfile`          | `vulnerability-profile` | `VulnerabilityProfileComponent` |
| `/bench`               | `compliance`            | `ComplianceComponent`           |
| `/cisProfile`          | `compliance-profile`    | `ComplianceProfileComponent`    |
| `/security-event`      | `security-events`       | `SecurityEventsComponent`       |
| `/audit`               | `risk-reports`          | `RiskReportsComponent`          |
| `/event`               | `events`                | `EventsComponent`               |
| `/multi-cluster`       | `multi-cluster`         | `MultiClusterComponent`         |
| `/federated-policy`    | `federated-policy`      | `FederatedPolicyComponent`      |
| `/settings/*`          | `settings`              | 设置首页及子模块                |
| `/support`             | `support`               | `SupportComponent`              |

## 3. 公共基础设计

### 3.1 模块与依赖注入

`CoreModule` 通过重复实例检查确保只加载一次。`NvCommonModule` 提供 Material/Bootstrap UI、公共指令、管道、领域服务和 HTTP 服务；
功能模块导入 `NvCommonModule` 或更细粒度公共组件模块。新服务必须明确其状态是应用级还是功能级，避免在延迟模块中产生意外多实例。

### 3.2 HTTP 请求处理

#### AuthInterceptor

- 从 Local Storage 的 `token.token.token` 读取令牌。
- 登录 POST 请求不要求已有令牌。
- 其他请求添加 `token`、`Cache-Control: no-cache`、`Pragma: no-cache` 请求头；虽然全局常量还定义了 `X-Auth-Token`，当前拦截器未使用它。
- 无令牌时导航到登录；SUSE SSO 解析异常时清除令牌并退出。

#### TimeoutInterceptor

- 监听 408、401、特定 503、Rancher 未认证 403，以及 token/self 初始化失败。
- 保存当前路由到 `original_url`，关闭所有 Material Dialog。
- SSO 场景清除令牌并进入退出页；普通场景设置超时标记并调用 `AuthService.timeout()`。
- `/multi-cluster` 和多集群摘要请求对部分 503 有特殊处理，以便主页面仍可恢复。

### 3.3 权限模型

登录响应形成以下前端权限上下文：

```ts
{
  global_permissions: object[],
  remote_global_permissions: object[],
  domain_permissions: object,
  extra_permissions: object[],
  roles: Record<string, number>
}
```

`isAuthorized(userRoles, resource)` 遍历角色维度，任一维度达到资源阈值即通过；对命名空间资源兼容历史授权级别。
各表格/弹窗在创建、修改、删除前检查对应资源权限。后端拒绝仍由 HTTP 错误处理显示。

### 3.4 全局状态键

| 键/字段                                   | 用途                       |
| ----------------------------------------- | -------------------------- |
| `token`                                   | 认证令牌、角色与权限信息。 |
| `cluster`                                 | 当前集群选择。             |
| `original_url`                            | 认证中断前路由。           |
| `external_Ref`                            | 启动时外部深链接。         |
| `local_timeout`                           | 本地超时提示。             |
| `theme`                                   | 用户主题。                 |
| `GlobalVariable.summary`                  | 系统与平台摘要。           |
| `isMaster/isMember/isStandAlone/isRemote` | 当前联邦上下文。           |
| `customPage*`、`customLoginLogo`          | 品牌定制。                 |

### 3.5 公共 UI 组件

| 类别     | 组件/指令                                                                   | 设计用途                             |
| -------- | --------------------------------------------------------------------------- | ------------------------------------ |
| 数据表   | `*-grid`、AG Grid cell renderer                                             | 统一列、过滤、选择、操作和详情联动。 |
| 布局     | `adjustable-div`                                                            | 可拖动主从分栏。                     |
| 加载     | `loading-button`、`loading-template`                                        | 请求进行中禁用和占位。               |
| 筛选     | `quick-filter`、`remote-grid-binding`、`two-way-infinite-scroll`            | 本地/服务端过滤和双向加载。          |
| 文件     | `import-file`、`export-options`、`export-options-modal`                     | 配置和报告导入导出。                 |
| 资产详情 | `container-brief/detail/stats`、`node-brief`、`pod-brief`、`enforcer-brief` | 在多个页面复用实体摘要。             |
| 策略     | `groups`、`network-rules`、`response-rules`、`admission-rules`              | 本地和联邦页面复用策略实现。         |

## 4. 功能模块详细设计

### 4.1 登录、退出和个人资料

**组件与服务**

- `routes/pages/login/login.component`：普通登录、SAML、OIDC、Rancher/SUSE SSO、EULA、原路由恢复。
- `routes/pages/logout/logout.component`：注销及 SSO 单点退出。
- `common/services/auth.service`：认证、刷新令牌、心跳、超时和跨标签页登录/退出事件。
- `settings/profile/profile-form`：个人资料和密码修改。

**主流程**

1. 页面先读取公开密码策略、认证服务器和可能的 SSO 参数。
2. 普通登录向 `auth` 提交凭据；SAML/OIDC 获取服务器跳转地址。
3. 成功响应写入 token、用户权限和 `GlobalVariable.user`。
4. 获取摘要与集群信息，设置 master/member/standalone 标志。
5. 优先恢复 `original_url` 或外部深链接，否则进入 `/dashboard`。

**关键接口**：`auth`、`token_auth_server`、`token_auth_server_slo`、`openId_auth`、`eula`、`heartbeat`、`self`、`password-profile/public`。

### 4.2 仪表盘

**组件结构**

`DashboardComponent` 组合 `security-risk-panel`、`exposure-panel`、`security-events-panel`、
`top-security-events-panel`、`top-vulnerable-assets-panel`、`policy-mode-panel`、
`application-protocols-panel` 和 `exposed-service-pod-grid`。打印使用 `DashboardPrintableReportComponent`。

**数据流**

- `DashboardService` 订阅刷新和集群切换事件并组织面板数据。
- `DashboardHttpService` 分别请求评分、详情、通知、摘要和系统告警。
- 域用户或命名空间报告通过 `domain` 参数限制查询。
- 评分改进弹窗调整 Metrics 后调用 `POST dashboard/scores` 计算预测结果，不直接等同于策略生效。

**关键接口**：`dashboard/scores`、`dashboard/details`、`dashboard/notifications`、`dashboard/alerts`、`summary`。

### 4.3 网络拓扑

**组件结构**

- `NetworkActivitiesComponent`：图数据、布局、节点/边事件、会话、隔离和刷新控制。
- `graph.service`：G6 图节点、边和布局辅助。
- `advanced-filter`、`blacklist`、`legend`：图设置。
- `group-info`、`host-info`、`namespace-info`、`pod-info`、`edge-details`：选中对象详情。
- `active-session`：会话表；`sniffer`：抓包配置、状态和 PCAP 下载。

**处理逻辑**

1. 读取本地高级过滤与黑名单。
2. 请求网络图并过滤隐藏域、组、端点和非托管端点。
3. 将返回节点转换为 G6 类型，对缺失关系补边，并按风险设置样式和箭头。
4. 支持域/集群聚合、展开、聚焦和局部重新布局。
5. 选中端点后按当前、实时或历史模式读取会话，并管理自动刷新定时器。
6. 隔离动作调用策略接口；删除通信快照调用 `DELETE network/conversation?from=&to=`。

**关键接口**：`network/graph`、`network/graph/layout`、`network/graph/blacklist`、`network/session`、`network/history`、`network/conversation`、`network/endpoint`、`sniffer`、`sniffer/pcap`、`unquarantine`。

### 4.4 平台、命名空间、节点和容器

#### 平台

`PlatformsComponent` 使用 `PlatformsGridComponent` 展示平台；选中项进入 `PlatformDetailsComponent`，详情组合基本信息和漏洞表。
`PlatformsService` 对 `AssetsHttpService.getPlatform()` 的数据进行缓存和刷新。

#### 命名空间

`NamespacesComponent` 与 `NamespacesGridComponent` 提供列表，`NamespaceDetailsComponent` 展示域信息。
非 Kubernetes 平台侧边栏移除此入口。变更通过 `GET/PATCH/POST domain` 完成。

#### 节点

`NodesComponent` 加载节点和扫描配置，`NodesGridComponent` 负责列表；`NodeDetailsComponent` 包含：

1. 节点详情 `NodeDetailComponent`。
2. 合规 `ComplianceGridComponent`。
3. 漏洞 `VulnerabilitiesGridComponent`。
4. 节点容器 `ContainersGridComponent`。

用户可切换自动扫描、对单节点发起扫描并导出扫描报告。

#### 容器

`ContainersComponent` 加载已扫描工作负载，执行系统容器/节点过滤和父子关系格式化。
`ContainerDetailsComponent` 包含详情、合规、漏洞、当前/历史进程和运行统计五个页签。
容器页支持自动/手工扫描、CVSS v2/v3 切换、接受漏洞、CSV 和打印报告。

**关键接口**：`host`、`host/workload`、`container`、`workload`、`workload/scanned`、`container/process`、`container/processHistory`、`scan/config`、`scan/host`、`scan/workload`、`scan/platform`、`host/compliance`、`workload/compliance`、`host/scan-report`、`workload/scan-report`。

### 4.5 镜像仓库扫描

**组件结构**

- `RegistriesTableComponent`：仓库配置和状态。
- `AddRegistryDialogComponent`：基于仓库类型构造动态配置表单。
- `TestSettingsDialogComponent`：连接测试和错误明细。
- `RegistryDetailsComponent`：详情/概览页签。
- `RegistryDetailsTableComponent`：仓库、镜像和标签结果。
- `RegistryDetailsDialogComponent`：单镜像漏洞、模块、层和合规信息。

**主流程**

1. 读取 `scan/registry/type` 和系统配置，确定可用仓库类型及表单字段。
2. 用户录入凭据后调用 `scan/registry/test`，按事务标识查询/清理测试结果。
3. 保存配置后刷新仓库表，用户可启动、停止或删除扫描。
4. 进入仓库详情后读取 repo/image 数据；按需请求镜像层和模块信息。
5. 对漏洞可调用配置文件接口接受，随后刷新对应镜像视图。

**关键接口**：`scan/registry/type`、`scan/registry/test`、`scan/registry`、`scan/registry/repo`、`scan/registry/image`、`scan/registry/layer`、`scanned-assets`、`scan/registry/fed-repo`。

### 4.6 系统组件与诊断

`SystemComponentsComponent` 使用三个页签组合 Controller、Scanner、Enforcer 表格，并通过通信服务同步选中详情。
Controller/Enforcer 详情可展示 `ContainerStatsComponent`；Enforcer 服务还能请求使用报告、生成调试包、轮询生成状态并下载 ArrayBuffer。

**关键接口**：`controller`、`scanner`、`enforcer`、`usage`、`file/debug`、`file/debug/check`、`debug`、`single-enforcer`。

### 4.7 组与安全策略

#### 组

`GroupsPageComponent` 是 `GroupsComponent` 的页面壳。组列表支持本地/联邦范围和详情选择。
`GroupDetailsComponent` 将成员、脚本、进程配置、文件访问、网络、DLP 等页签组合起来。

#### 网络规则

`NetworkRulesComponent` 依赖 `NetworkRulesService` 完成规则列表格式化、条件编辑、排序及 CRUD。
临时新增规则从常量种子 `1000000` 分配前端 ID，保存后以后端 ID 为准。规则区分学习、用户、联邦、系统和只读状态。

#### 进程和文件规则

`ProcessProfileRulesService`、`FileAccessRulesService` 将 API 数据转换为网格行，处理允许/拒绝进程和监控变更/阻断访问规则。
更改组基线或策略模式由 `ServiceModeService` 统一提交。

#### 响应规则

`ResponseRulesService` 将条件对象与标签/字符串相互转换，按 Kubernetes/非 Kubernetes 环境限制事件和动作选项；
支持 webhook、日志抑制、隔离及解除隔离，并处理本地/联邦导入导出。

#### 准入控制

`AdmissionRulesComponent` 管理准入状态、例外/拒绝规则、条件选项和顺序。
匹配测试通过上传 Kubernetes 资源内容调用 `admission/matching-test`，配置评估使用 `admission/test`。

#### DLP/WAF

`DlpSensorsComponent`、`WafSensorsComponent` 及对应服务维护传感器规则和组绑定；同一组件接受 scope/source 参数复用于联邦页。

**关键接口**：`group`、`group-list`、`group/custom_check`、`service`、`service/all`、`processProfile`、`fileProfile`、`filePreProfile`、`policy`、`policy/rule`、`policy/application`、`responsePolicy`、`responseRule`、`conditionOption`、`admission/*`、`dlp/sensor`、`dlp/group`、`waf/sensor`、`waf/group`。

### 4.8 漏洞

**组件结构**

- `VulnerabilitiesComponent`：风险/资产视图切换、刷新、CSV/PDF 操作。
- `VulnerabilityChartsComponent`：汇总图表。
- `VulnerabilityItemsComponent`：选中漏洞及明细协调。
- `VulnerabilityItemsTableComponent`：远程查询、排序、分页和筛选。
- `VulnerabilityItemsDetailsComponent`：CVE 与受影响资产详情。
- `VulnerabilityProfileComponent`：接受项配置文件及导入导出。

**查询设计**

1. 风险汇总调用 `risk/cve`。
2. 高容量查询先向 `vulasset` 提交 `VulnerabilityQuery` 获得查询会话。
3. 后续请求携带 `token/start/row/orderby/qf/scoretype` 分页获取结果。
4. 资产视图调用 `risk/cve/assets-view`，节点/工作负载报表分别调用扫描报告接口。
5. CSV 按固定字段顺序输出，CVE 链接可生成超链接文本；PDF 使用风险或资产打印模块。

**核心模型**：`Vulnerability`、`VulnerabilityQuery`、`VulnerabilitiesQueryData`、`VulnerabilityProfile`、`VulnerabilityProfileEntry`。

### 4.9 合规

`ComplianceComponent` 组合汇总图表和合规项明细。`ComplianceItemsComponent` 协调图表、表格、过滤和详情；
`ComplianceService` 汇集平台、节点、工作负载和 NIST 映射并生成 CSV/PDF 数据。

`ComplianceProfileComponent` 分为资产与模板两个域：

- 资产页维护资产到合规模板的绑定。
- 模板页维护类别、法规及检查项过滤。
- 支持使用后端提供的可用过滤选项，及配置文件导入、导出。

**关键接口**：`risk/compliance`、`risk/complianceNIST`、`risk/compliance/profile`、`risk/compliance/template`、`risk/compliance/available_filter` 及 profile import/export。

### 4.10 事件与报告

#### 安全事件

`SecurityEventsComponent` 维护时间范围、标签、分页窗口和可见事件。`SecurityEventsService` 负责查询、转换和打印数据。
详情按事件类型委托 `ThreatDetailsComponent`、`ViolationDetailsComponent`、`IncidentDetailsComponent`。
高级筛选、日期滑块、报文查看、网络规则复核和进程规则复核均使用独立对话框。

#### 系统事件

`EventsComponent` 复用 `EventsGridComponent`，通过 `EventsService.getEventsByLimit(start, limit)` 增量读取并格式化事件行。

#### 风险报告

`RiskReportsComponent` 使用 `RiskReportGridComponent`，按报告类别格式化数据；打印组件生成柱状图、饼图和详情表，CSV 服务生成下载内容。

**关键接口**：`security-events2`、`threat`、`event`、`audit`、`notification/accept`。

### 4.11 多集群与联邦策略

**多集群组件**

- `MultiClusterComponent`：根据联邦角色初始化页面，提供提升、加入和刷新入口。
- `MultiClusterGridComponent`：集群列表和状态。
- `MultiClusterDetailsComponent`：成员详情与操作。
- `PromotionModalComponent`：独立集群提升为主集群。
- `JoiningModalComponent`：加入现有联邦。
- `TokenModalComponent`：显示和复制加入令牌。

`MultiClusterService` 通过 BehaviorSubject 保存选中集群和摘要，使用浏览器事件广播切换、刷新、管理成员和集群名变化。
并发获取多集群摘要时应遵循实现常量限制 8。

**联邦策略组件**

`FederatedPolicyComponent` 复用组、网络、响应、准入、DLP、WAF、进程、文件和联邦配置公共组件；
`FedGroupDetailsComponent` 聚合单个联邦组的策略详情。所有请求传入联邦 scope/source，界面按主集群和联邦权限开放编辑。

**关键接口**：`fed/member`、`fed/deploy`、`fed/summary`、`fed/promote`、`fed/demote`、`fed/join_token`、`fed/join`、`fed/leave`、`fed/config`、`fed/switch`、`multi-cluster-summary`。

### 4.12 系统设置

`SettingsComponent` 是卡片导航页，子路由如下：

| 子路由          | 组件                     | 详细能力                                         |
| --------------- | ------------------------ | ------------------------------------------------ |
| `users`         | `UsersComponent`         | 用户、API Key、角色、密码策略四个页签。          |
| `configuration` | `ConfigurationComponent` | 常规配置、支持包、CSP 支持、导入导出、远程仓库。 |
| `ldap`          | `LdapComponent`          | LDAP 服务器、连接测试、默认角色和组/域映射。     |
| `saml`          | `SamlComponent`          | SAML SSO、证书/端点和角色映射。                  |
| `openid`        | `OpenidComponent`        | OIDC Issuer/Client 配置和角色映射。              |

**用户和角色**

`UsersGridComponent` 执行用户 CRUD、解锁和密码重置；`RolesGridComponent` 读取权限选项并配置角色；
`ApikeysGridComponent` 创建和删除 API Key；`PasswordProfileComponent` 维护密码策略。

**系统配置**

`ConfigFormComponent` 读取 `config-v2` 后构建配置表单，提交时转换为 `ConfigPatch`；
`ExportFormComponent` 和 `ImportFileComponent` 负责系统/联邦配置文件；`RemoteRepositoryFormComponent` 管理 GitHub/Azure DevOps 目标。

**关键接口**：`user`、`role2`、`role2/permission-options`、`api_key`、`password-profile`、`server`、`config-v2`、`file/config`、`file/config-fed`、`file/export-config-fed`、`webhook`、`remote_repository`、`csp-support`。

### 4.13 签名验证器与支持

`SignatureVerifiersComponent` 以两个表格管理签名和验证器，使用新增/编辑对话框及操作按钮渲染器；
验证器支持私有、公有和 rootless keypair 等属性。导入导出由签名服务调用文件接口。
`SupportComponent` 展示部署环境可用的支持信息和外部入口。

**关键接口**：`sigstore`、`verifier`、`signature/import`、`signature/export`、`csp-support`、`version`。

## 5. 核心数据模型

类型统一从 `common/types/index.ts` 导出，主要领域如下：

| 领域     | 代表模型                                                 | 关键关系                                                |
| -------- | -------------------------------------------------------- | ------------------------------------------------------- |
| 用户权限 | `User`、`Self`、`Role`、`Permission`、`Apikey`           | 用户绑定全局角色和域角色；角色包含资源权限。            |
| 集群     | `Cluster`、`ClusterSummary`、联邦类型                    | 当前集群决定数据源与可编辑范围。                        |
| 资产     | `Platform`、`Domain`、`Host`、`Workload`                 | 平台包含节点/工作负载；工作负载归属节点和域。           |
| 组件     | `Controller`、`Scanner`、`Enforcer`                      | 组件关联主机、版本、连接状态和统计。                    |
| 扫描风险 | `Vulnerability`、`ScanConfig`、`WorkloadCompliance`      | 扫描报告关联资产、漏洞和合规项。                        |
| 策略     | `Group`、`Service`、`NetworkRule`、`ResponseRule`        | 规则以组/服务为端点，包含条件、动作、scope 和配置来源。 |
| 事件     | `EventItem`、`Audit`、安全事件模型                       | 事件关联源/目标资产、规则、动作和时间。                 |
| 设置     | `ConfigV2Response`、`ServerPatchBody`、`PasswordProfile` | 表单模型转换为后端配置补丁。                            |

新增模型应避免 `any`，对列表响应宜定义 `{ items, pagination }` 或明确的包装类型，并在 HTTP 服务中完成解包。

## 6. 导入、导出与报告设计

| 类型     | 输入/输出     | 处理方式                                               |
| -------- | ------------- | ------------------------------------------------------ |
| 策略配置 | YAML/配置文件 | `ImportFileComponent` 上传，导出使用 Blob 或远程仓库。 |
| 系统配置 | 配置文件      | 支持本地、联邦和带模式参数的导出。                     |
| CSV      | 文本文件      | 服务将展示模型转换为固定列，`file-saver` 下载。        |
| PDF      | 浏览器打印    | 专用 printable component 生成图表和表格。              |
| PCAP     | 二进制        | 抓包接口返回可下载地址/Blob。                          |
| 调试包   | ArrayBuffer   | 先生成、轮询检查，完成后下载。                         |

导出大量资产时采用分页累积。所有导出必须保留筛选上下文，并对空结果、超限、后端错误提供反馈。

## 7. 错误处理与边界状态

1. HTTP 服务将可恢复错误交给调用组件；组件使用 Toastr、Snackbar 或对话框显示结果。
2. 认证类错误由拦截器统一处理，业务组件不得重复弹出登录框。
3. 列表需区分加载中、空数据、过滤无结果和加载失败。
4. 对象详情加载失败不应清空主列表；刷新后若原对象不存在，应关闭详情或重新选择。
5. 删除、隔离、离开/降级联邦等操作必须确认，失败后保持原界面状态。
6. 文件下载错误若以二进制返回 JSON，应先使用 `TextDecoder` 还原错误消息。
7. 存在未保存表单时，组件实现 `ComponentCanDeactivate` 并由 `PendingChangesGuard` 询问。

## 8. 性能与资源管理

- 漏洞查询使用查询令牌和服务端分页；事件采用限制条数/无限滚动。
- 拓扑图先过滤后渲染，聚合域/集群并按设备能力决定图形能力。
- 自动刷新必须在 `ngOnDestroy` 中停止；RxJS 订阅应使用显式取消信号或 Subscription 聚合。
- 图表、G6 实例、窗口/文档事件和 jQuery handler 必须在销毁时解绑。
- 图片和模块使用路由级懒加载；生产产物启用内容哈希和压缩。

## 9. 国际化、主题与可访问性

- 页面文案使用 `| translate`，键位于 `assets/i18n/en-common.json`、`zh_cn-common.json` 和 partner 文件。
- `merge-i18n.js` 在构建前合并 common 与 partner 资源，伙伴值可覆盖/扩展公共值。
- 图表、状态和枚举应使用翻译后的可见文本，不直接显示后端枚举。
- 主题由 `ThemesService` 和 SCSS 主题文件控制；品牌头尾颜色由后端配置注入。
- 图标按钮应提供 `aria-label`/tooltip；表单必须关联 label 和错误说明；颜色不得是唯一的状态表达。

## 10. 测试设计

### 10.1 单元测试

| 对象      | 必测内容                                               |
| --------- | ------------------------------------------------------ |
| HTTP 服务 | 方法、端点、HTTP 动词、参数、响应解包和错误分支。      |
| 领域服务  | 数据转换、过滤、排序、CSV、Subject 广播和边界输入。    |
| 页面组件  | 初始化、权限分支、刷新、选择详情、弹窗结果和销毁清理。 |
| 表单/弹窗 | 默认值、校验、编辑回填、提交载荷和取消行为。           |
| 拦截器    | 登录豁免、令牌头、401/408/503/SSO 403 分支。           |
| 多集群    | 三种集群角色、远程权限刷新和切换事件。                 |

### 10.2 集成与回归重点

1. 普通登录、SAML、OIDC、Rancher/SUSE SSO 和超时恢复。
2. 不同角色/命名空间权限下的页面和按钮矩阵。
3. 资产扫描、状态刷新、详情和报告下载闭环。
4. 六类策略 CRUD、排序、导入导出以及本地/联邦 scope 隔离。
5. 漏洞远程分页、高级筛选、接受项和大数据导出。
6. 网络拓扑的大图渲染、会话自动刷新、隔离和抓包。
7. 集群提升、加入、切换、同步、离开、移除和降级。

### 10.3 验证命令

```bash
cd admin/webapp
npm run lint:check
npm run format:check
npm test -- --watch=false
npm run build
```

## 11. 需求到实现追踪

| 需求域    | 路由/组件                          | 服务/API                                   | 主要测试位置                             |
| --------- | ---------------------------------- | ------------------------------------------ | ---------------------------------------- |
| FR-AUTH   | `routes/pages`、`settings/profile` | `AuthService`、`AuthHttpService`、拦截器   | 同目录 `*.spec.ts`                       |
| FR-DASH   | `routes/dashboard`、风险面板组件   | `DashboardService`、`DashboardHttpService` | `routes/dashboard/**/*.spec.ts`          |
| FR-NET    | `routes/network-activities`        | 图服务、`GraphHttpService`                 | `routes/network-activities/**/*.spec.ts` |
| FR-ASSET  | 资产、仓库、组件模块               | Assets/Scan/Registries/组件服务            | 对应模块 `*.spec.ts`                     |
| FR-POLICY | 策略页面和共享策略组件             | Policy 与各策略领域服务                    | `routes/components/**/*.spec.ts`         |
| FR-RISK   | 漏洞、合规及配置文件模块           | `RisksHttpService` 和报告服务              | 对应风险模块 `*.spec.ts`                 |
| FR-EVENT  | 安全事件、事件、风险报告           | SecurityEvents/Events/RiskReports 服务     | 对应通知模块 `*.spec.ts`                 |
| FR-FED    | 多集群、联邦策略                   | `MultiClusterService`、配置/策略服务       | `routes/multi-cluster/**/*.spec.ts`      |
| FR-SET    | `routes/settings`                  | Settings/Auth/Config 服务                  | `routes/settings/**/*.spec.ts`           |

## 12. 维护规则

1. 新增用户可见能力时，同步更新需求编号、概要模块表和本详细设计追踪表。
2. 修改接口时，同时修改 `PathConstant`、请求/响应类型、HTTP 服务测试及受影响页面测试。
3. 修改权限时，记录资源权限、集群角色、scope 和只读行为四个维度。
4. 新增导出时明确格式、最大数据量、筛选语义、文件名和公式注入防护。
5. 新增定时刷新或全局事件时明确创建、暂停、切换集群和销毁行为。
