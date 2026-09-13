# NeuVector Web 前端概要设计说明书

## 1. 设计目标

本系统采用模块化 Angular 单页应用，为 NeuVector Manager 提供容器安全控制台。设计重点是：

- 以业务域拆分功能并通过延迟加载控制首屏开销。
- 以共享组件统一表格、详情、策略编辑、导入导出和消息反馈。
- 以服务层隔离页面状态、数据格式化和 HTTP 访问。
- 以统一会话、权限和多集群上下文保证不同页面行为一致。
- 兼顾传统 jQuery 插件、图形/图表库与 Angular Material 组件的渐进演进。

## 2. 总体架构

```text
浏览器
  └─ AppModule / AppComponent
      ├─ CoreModule（单例基础设施）
      │   ├─ 认证与超时拦截器
      │   ├─ 菜单、主题、翻译、布局开关
      │   └─ 多集群上下文
      ├─ FrameModule（登录后应用外壳）
      │   ├─ Header / Sidebar / Footer
      │   └─ RouterOutlet
      ├─ RoutesModule（Hash 路由与延迟加载）
      │   └─ 各业务 Feature Module
      └─ NvCommonModule（共享 UI 与领域服务）
          ├─ 公共组件、指令、管道、Formly 字段
          ├─ 领域服务 Services
          ├─ HTTP 服务 API Services
          └─ 类型、常量、校验器、工具函数
                  │
                  └─ NeuVector Manager REST API
```

### 2.1 分层职责

| 层次   | 目录                              | 职责                                                         |
| ------ | --------------------------------- | ------------------------------------------------------------ |
| 启动层 | `websrc/main.ts`、`app.module.ts` | 启动应用、注册 HTTP、动画、翻译和全局对象。                  |
| 核心层 | `websrc/app/core/`                | 只加载一次的认证拦截、超时处理、菜单、主题、翻译和布局状态。 |
| 框架层 | `websrc/app/frame/`               | 页头、侧栏、页脚、全局搜索/通知、集群切换和内容容器。        |
| 路由层 | `websrc/app/routes/`              | 页面级业务模块、功能编排和页面局部状态。                     |
| 共享层 | `websrc/app/common/`              | API、领域服务、类型、常量、公共 UI、管道、指令、校验和工具。 |
| 资源层 | `websrc/assets/`                  | 图片、图标、中英文与伙伴翻译资源。                           |

## 3. 业务模块划分

| 业务域     | 路由                                                          | 主要模块/能力                                       |
| ---------- | ------------------------------------------------------------- | --------------------------------------------------- |
| 身份       | `/login`、`/logout`、`/eula`、`/profile`                      | 登录、SSO、EULA、退出、个人资料。                   |
| 总览       | `/dashboard`                                                  | 安全评分、风险、暴露、事件、资产排行、打印报告。    |
| 网络       | `/graph`                                                      | G6 拓扑、会话、过滤、黑名单、隔离、抓包。           |
| 资产       | `/platforms`、`/domains`、`/hosts`、`/workloads`              | 平台、命名空间、节点、工作负载及其详情。            |
| 镜像与组件 | `/regScan`、`/signature-verifiers`、`/controllers`            | 仓库扫描、签名验证器、Controller/Scanner/Enforcer。 |
| 策略       | `/admission-control`、`/group`、`/policy`、`/response-policy` | 准入、组、网络和响应策略。                          |
| 内容防护   | `/dlp-sensors`、`/waf-sensors`                                | DLP/WAF 传感器、规则和组绑定。                      |
| 风险       | `/scan`、`/cveProfile`、`/bench`、`/cisProfile`               | 漏洞、漏洞配置、合规、合规配置。                    |
| 通知审计   | `/security-event`、`/audit`、`/event`                         | 安全事件、风险报告、系统事件。                      |
| 联邦       | `/multi-cluster`、`/federated-policy`                         | 集群生命周期、切换、同步和联邦策略。                |
| 设置支持   | `/settings`、`/support`                                       | 用户权限、认证、系统配置、诊断和支持信息。          |

页面级模块由根路由动态 `import()`，公共策略和表格组件位于 `routes/components/`，供本地策略页和联邦策略页复用。

## 4. 关键技术设计

### 4.1 技术栈

| 类别     | 选型                                                        | 用途                                   |
| -------- | ----------------------------------------------------------- | -------------------------------------- |
| 基础框架 | Angular 20、TypeScript 5.8、RxJS 7.8                        | 组件、路由、依赖注入、响应式数据流。   |
| UI       | Angular Material 20、Bootstrap 5、ngx-bootstrap             | 表单、弹窗、页签、布局和反馈。         |
| 表格     | AG Grid 31                                                  | 大型业务列表、排序、过滤、单元格渲染。 |
| 图形     | AntV G6 4.8                                                 | 网络拓扑图。                           |
| 图表     | Chart.js 3、ng2-charts                                      | 仪表盘、风险和报告图表。               |
| 动态表单 | Formly 7                                                    | 配置型表单和自定义字段组件。           |
| 国际化   | ngx-translate 17                                            | 运行时中英文及伙伴文本。               |
| 文件     | file-saver、pako                                            | 下载和压缩数据处理。                   |
| 工程     | Angular CLI/custom webpack、ESLint、Prettier、Karma/Jasmine | 构建、静态检查和单元测试。             |

### 4.2 路由与导航

- 根路由使用 `RouterModule.forRoot(routes, { useHash: true })`。
- 未认证入口为登录；登录后业务页面装载在 `FrameComponent` 的子路由出口。
- 所有主要业务模块均延迟加载；未知路径重定向到登录。
- 菜单数据由 `routes/menu.ts` 声明，`MenuService` 注册并由侧边栏渲染。
- 菜单会依据平台类型、集群角色和组件内权限条件动态裁剪或禁用。

### 4.3 数据访问与状态

```text
页面组件
  ├─ 领域服务：缓存、BehaviorSubject、格式化、业务组合、刷新事件
  └─ HTTP 服务：构造请求、映射响应 DTO
          └─ GlobalVariable.http / HttpClient
                  ├─ AuthInterceptor：令牌、禁止缓存
                  └─ TimeoutInterceptor：认证错误与重登录
```

状态按生命周期分三类：

| 状态范围     | 实现                                            | 示例                                           |
| ------------ | ----------------------------------------------- | ---------------------------------------------- |
| 全局进程态   | `GlobalVariable`、核心服务                      | 当前用户、平台摘要、集群角色、版本、品牌配置。 |
| 跨页面持久态 | Local/Session Storage                           | 令牌、原 URL、集群、主题、网络图过滤设置。     |
| 功能态       | 服务字段、`Subject`/`BehaviorSubject`、组件字段 | 资产缓存、选中详情、刷新通知、配置快照。       |

系统未采用集中式 Redux 状态库；领域服务是主要状态协调单元。这样降低简单 CRUD 的样板代码，代价是全局静态状态和服务状态需要严格管理生命周期。

### 4.4 认证、授权与会话

1. 登录页调用认证服务，解析令牌、角色、全局/域权限并初始化集群摘要。
2. 认证令牌保存在本地存储，业务请求由 `AuthInterceptor` 添加 `token` 请求头。
3. `TimeoutInterceptor` 捕获认证超时、未认证、部分服务不可用和 SSO 特定错误。
4. 拦截器保存当前路径，关闭全部对话框，清理或标记会话并导航到登录/退出。
5. 组件使用权限工具对创建、修改、删除、集群管理等按钮进行显示控制。
6. 切换远程集群时刷新令牌和远程权限，随后广播集群切换/刷新事件。

### 4.5 多集群上下文

`MultiClusterService` 维护当前集群、集群摘要及切换事件。页头负责集群选择；业务页面通过共享全局状态和事件刷新数据。
联邦主集群提供成员管理与联邦策略入口；成员和独立集群按角色隐藏不适用操作。

### 4.6 UI 组合设计

- 页面通常采用“工具栏 + 主表格 + 可调整详情面板”结构。
- AG Grid 单元格渲染器封装状态、严重度、操作按钮、IP/位置等展示。
- Angular Material Dialog 承载增删改、测试连接、导入、规则复核和危险确认。
- Formly 自定义字段封装切换、滑块、单选、多选、编辑表格和提示包装器。
- 统一 Loading Button/Template 表达请求状态；Toastr 在右下角反馈结果。
- 打印组件将页面模型转换为图表和表格，交由浏览器打印为 PDF。

## 5. 核心业务数据流

### 5.1 登录初始化

```text
用户提交凭据/选择 SSO
  → AuthService 调用认证端点
  → 保存 token、角色和权限
  → 获取系统摘要、集群角色、品牌配置
  → 恢复原路由或进入 Dashboard
  → Frame 加载菜单、通知、版本与集群选择器
```

### 5.2 资产扫描与风险查看

```text
资产页加载列表
  → AssetsHttpService 获取平台/节点/工作负载
  → ScanService 读取扫描配置与状态
  → 用户发起扫描
  → 轮询/刷新资产状态
  → 打开详情获取漏洞与合规数据
  → 导出 CSV/打印报告或接受漏洞
```

### 5.3 策略维护

```text
策略页加载规则与选项
  → 服务将条件对象转换为标签/表格模型
  → 用户新增、编辑、排序或删除
  → 表单校验与权限检查
  → POST/PATCH/DELETE 策略接口
  → 刷新规则并显示操作结果
```

### 5.4 联邦切换

```text
用户选择成员集群
  → 请求 fed/switch 或刷新认证信息
  → 更新 selectedCluster、token 和远程权限
  → 广播 switch/refresh 事件
  → 各页面按新集群上下文重新取数
```

## 6. 接口概要设计

接口统一使用 JSON，文件导入导出及调试包使用 Blob/ArrayBuffer。服务按领域拆分：

| HTTP 服务              | 主要资源                                                      |
| ---------------------- | ------------------------------------------------------------- |
| `AuthHttpService`      | 许可证、用户、角色、API Key、个人资料、认证服务器、密码策略。 |
| `AssetsHttpService`    | 平台、域、主机、工作负载、进程、组件、扫描配置和报告。        |
| `PolicyHttpService`    | 组、服务、网络/响应规则、DLP/WAF 及导入导出。                 |
| `RisksHttpService`     | 漏洞查询、受影响资产、合规、漏洞/合规配置文件。               |
| `DashboardHttpService` | 评分、详情、通知、摘要和系统告警。                            |
| `EventsHttpService`    | 系统事件和风险报告。                                          |
| `GraphHttpService`     | 网络通信快照操作；复杂图数据由页面相关服务访问。              |
| `ConfigHttpService`    | 系统/联邦配置、调试包、CSP 支持和远程仓库。                   |
| `CommonHttpService`    | 版本、系统摘要、头像和品牌定制。                              |

端点集中定义在 `PathConstant`，降低散落字符串，但部分复杂功能仍在领域服务或组件中直接组织请求。

## 7. 构建与部署设计

### 7.1 构建流程

1. `npm ci` 按锁文件安装依赖。
2. `prebuild` 合并 common 与 partner 国际化文件。
3. ESLint 检查/修复后执行 Angular production build。
4. custom webpack 对输出进行压缩和打包处理。
5. 构建结果输出到 `admin/webapp/root/`，随后由 Manager 镜像/服务静态托管。

### 7.2 构建配置

- 生产构建启用内容哈希和优化，保留 vendor chunk。
- 开发构建启用 source map 和命名 chunk。
- 静态资产包括 favicon、图片和翻译资源。
- SCSS 支持公共样式 include path，并加载少量历史 jQuery/System Prepare 脚本。

## 8. 质量属性设计

| 属性     | 设计措施                                                      |
| -------- | ------------------------------------------------------------- |
| 性能     | 路由延迟加载、服务端分页/无限滚动、拓扑过滤、构建预算。       |
| 安全     | 统一令牌头、超时退出、组件权限控制、后端最终鉴权、密码控件。  |
| 可维护性 | 领域模块、路径别名、集中常量、共享业务组件、严格 TypeScript。 |
| 可测试性 | 服务与组件依赖注入、Karma/Jasmine、同目录规格文件。           |
| 国际化   | 模板使用翻译键，common/partner 资源在构建前合并。             |
| 可扩展性 | 联邦/本地组件复用、Formly 字段扩展、单元格渲染器扩展。        |
| 可恢复性 | 统一错误提示、认证恢复、原 URL 保存、刷新入口。               |

## 9. 主要设计风险

1. `GlobalVariable` 和本地存储承担较多全局状态，需防止集群切换后遗留旧数据。
2. 根路由缺少统一认证守卫，安全性依赖拦截器和后端鉴权；深链接体验也依赖初始化时序。
3. 部分页面和服务体量较大，尤其网络拓扑，修改时需要重点回归资源释放和渲染性能。
4. Angular Material、Bootstrap、jQuery 和多个历史插件并存，升级时存在样式和生命周期冲突风险。
5. 动态 `any` 数据仍较多，API 演进需要通过类型补强和契约测试降低运行时错误。
6. 浏览器生成 PDF、超大 CSV 和拓扑渲染的容量受客户端设备限制。
7. 常量中定义了 `X-Auth-Token`，但现有拦截器实际发送 `token` 头；调整认证头前必须与后端契约联动验证。

## 10. 设计约束

- 新页面应继续采用 Feature Module 延迟加载并复用 `NvCommonModule`。
- 新接口路径应优先加入 `PathConstant`，请求/响应结构应定义 TypeScript 类型。
- 新的跨组件状态应优先使用领域服务和 Observable，避免继续扩张静态全局变量。
- 所有修改操作必须同时具备前端显示控制和后端权限校验。
- 新文本必须进入国际化资源，不应硬编码在模板中。
- 新行为必须补充对应 `*.spec.ts`，并通过 lint、format、test 和 production build。
