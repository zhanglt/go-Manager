# M2 Go/Gin POC 状态报告

| 属性 | 内容 |
| --- | --- |
| 状态 | In Progress，首批垂直切片已实现 |
| 日期 | 2026-08-02 |
| 实现目录 | `admin-go/` |
| 门禁结论 | G0、FIPS、正式性能门禁均未通过 |

## 1. 已实现范围

- 建立 Go 1.25 module，使用 Gin 1.12 和标准库 `slog`、`net/http`；
- 强类型读取和聚合校验现有端口、TLS、Header、`PATH_PREFIX`、UI 定制变量；
- 实现 request ID、脱敏访问日志、panic recovery、50 MiB Body 限制、安全 Header；
- 支持 SIGINT/SIGTERM 优雅退出，以及默认禁用的独立 health Listener；
- 建立 Controller HTTPS Client，默认兼容现有证书校验行为，可显式启用校验；
- 完成 Manager Server TLS 证书加载和缺省回退；现有 PEM chain、PKCS#1/PKCS#8 RSA key
  由标准库加载，证书对缺失时生成仅驻留内存的 RSA-2048/SHA-256 临时自签名证书；
- 实现登录/登出、`/self`、`/heartbeat`、`/eula`、`/gravatar`、`/rebrand`、`/version`。
- 实现 `/fed/switch` 和 cluster-aware Target Resolver，cluster ID 与资源名称按路径段转义；
- 完成 federation member 转换，以及 summary、promote、demote、join token、join、leave、
  config、delete 和 deploy；管理请求固定访问本地 Controller，不受当前切换集群影响；
- 实现 `role2`、`role2/permission-options`、`api_key` 的 GET/POST/PATCH/DELETE 操作。
- 实现 `user` CRUD、`password-profile` 管理和本地 `POST /token` validation；
- 用户列表与单用户查询保持 Scala 的 emailHash、timeout、角色和 Token DTO 转换。
- 完成 EULA 写入、license、server/LDAP/SAML 配置和 server debug 测试接口；
- debug 与 server 配置可能含密码，访问日志和错误日志均不记录 Header 或 Body。
- 完成 device 领域 enforcer、single-enforcer、controller、scanner、system summary、IBM SA
  setup、usage、host、host workload 和 host compliance 的只读查询；
- device 资源查询支持 federation cluster target，IBM SA setup 与 usage 固定访问本地 Controller。
- 完成 webhook CRUD、V1/V2 system config GET/PATCH 和 remote repository CRUD；
- 保持 webhook/config 的 federal/local scope 选路、`X-Nv-Page` 传播和 Scala 可选字段省略行为；
- `PATCH /config` 保留 Scala 未注入 Manager Web Header 的特殊兼容行为，同时透传 Controller Header。
- 完成 `GET /file/config` 的 all/policy 下载分支和 `POST /file/export-config-fed`；响应流式
  透传，联邦导出保持 Spray JSON 的 `Option.None` 字段省略语义；
- 完成 `POST /file/config` transaction 与 multipart 分支；408 重试中的临时 Token 仅保留
  在请求内，multipart 流式转发并保留 `Content-Length`，不缓存文件正文；
- 完成 `POST /file/config-fed` transaction 与旧版按行提取表单正文的兼容分支；目标 URL
  每次按当前 Token/cluster 解析，避免 Scala 共享 base URL 在切换集群后残留；
- 完成 group local/fed 配置导出，以及 transaction 与旧 multipart 文本提取导入；导出
  使用强类型 DTO 校验必填字段，并保持 Spray JSON 的可选字段省略语义；
- 抽取 group/policy 共用的 cluster-aware transfer 代理，集中处理认证 Header、scope、
  transaction、旧表单提取和响应流复制；完成 response policy local/fed 导入导出；
- 扩展 transfer 的结构化 query 支持，完成 risk vulnerability/compliance profile 导入
  导出；vulnerability `option` 必填并安全编码，两个导出 DTO 均校验 `names`；
- 完成 vulnerability profile GET/PATCH 和 entry POST/PATCH/DELETE；新增 entry 仅转发首项，
  profile、entry ID 均作为独立路径段转义；完成 compliance profile 列表、单项和 PATCH；
- 完成 CVE asset GET、assets-view PATCH，以及 compliance asset、template、available filter
  查询；保持 assets-view 外部 PATCH 到 Controller POST 和可选字段省略行为；
- 完成 scanned-assets 和 vulasset 的查询启动与分页读取；校验必填 token/start/row，使用
  结构化 query 安全传递排序、过滤、mtime 和 score type 参数；
- 完成 complianceNIST 本地映射；约 20 KB CSV 首次访问时惰性加载，并保留该 Scala
  路由不附加 Manager Web Security Header 的历史响应行为；
- 完成 Sigstore root of trust 与 verifier 的 GET/POST/PATCH/DELETE，使用强类型 DTO 并支持
  federation cluster target 和路径段安全转义；
- 完成 workload 列表/stats/详情/隔离/monitor/compliance、container 列表/详情/process/
  process history，以及 domain GET/PATCH/POST；
- 保留 monitor 外部 GET 到 Controller PATCH、domain 外部 POST 到 Controller PATCH 和
  `view=pod` 查询转换，所有 workload/domain 资源支持 federation cluster target；
- 完成 sniffer 列表、创建、停止、删除和 PCAP 下载；PCAP 不缓冲响应正文，流式传播
  `Content-Type`、规范化后的 `Content-Disposition` 和客户端取消信号；
- 完成 workload/host scan report，使用共享强类型 DTO 保留可选值语义，响应从 Controller
  直接流式传输；合成 fixture 同时校验转发请求体 SHA-256；
- 完成 `/workload/scanned` V2 DTO 转换和分页时序；分页缓存使用完整 Token、cluster 和
  domain 隔离，并受条目数、字节数、TTL 限制，登出和切换集群时主动失效；
- 完成 `group-list`、group custom check，以及 group 创建、更新和删除；保持 federal/local
  Controller 选路、criteria operator 转换和 learned group 更新字段裁剪行为；
- 完成 `GET /group` 列表、单项、scope 和分页分支的 DTO 转换；分页缓存按完整 Token、
  cluster、scope 和 capability 参数隔离，并与 workload 缓存一起在认证生命周期中失效；
- 完成 service 列表/单项查询、service config 更新、新 service policy 默认值和 system
  request 更新；保持 public PATCH 到 Controller POST 的兼容映射；
- 完成 process profile 的名称查询、scope 列表和更新；federal scope 固定访问本地
  Controller，其他 scope 保持 cluster-aware 选路；
- 完成 file profile 的名称查询、scope 列表、更新和 predefined 查询；更新时剥离外部
  `group` DTO 字段，仅将内部 config 传递给 Controller；
- 完成 DLP sensor CRUD、scope 查询和 local/fed 导入导出；保持 transaction Header、旧
  multipart 文本提取，以及 DLP scope 始终使用当前 cluster target 的 Scala 行为；
- 完成 DLP group 按名称查询和更新；名称作为独立 Controller 路径段编码，更新请求保留
  Scala 的 `config` 包装及可选字段省略语义；
- 完成 WAF sensor CRUD、scope 查询和 local/fed 导入导出，以及 WAF group 查询、更新；
  与 DLP 共用受限 transfer 逻辑，但 WAF 导出固定转发至 `file/waf`，并保留其不含
  `predefine`、`prerules` 的独立 DTO；
- 完成 Docker/Kubernetes benchmark 查询与触发，以及 CSP support 包下载；benchmark POST
  从 Manager 请求正文获取 host ID 并仅将其置入 Controller 路径，CSP 响应保持 Controller
  声明的二进制内容类型；
- 完成 support debug log 创建、状态检查和下载；support 命令固定路径执行，enforcer ID
  受白名单约束，文件授权按 Token/cluster 隔离并设置容量、TTL 和下载后清理；
- 完成 event、incident 和 audit 日志查询；三者透明转发至当前 cluster 的 `log/*` 资源，
  而带 Scala 分页缓存或 DTO 聚合的 Notification 路由仍单独保留；
- 完成 audit2 分页和 security-events2；audit2 缓存按完整 Token/cluster 隔离、逐页 PATCH
  验证 Token，security-events2 保持 threat、violation、incident 原始 JSON 字符串顺序；
- 完成 `/ip-geo` IPv4/IPv6 查询；复用现有 IP2Location CSV，首次访问时惰性解析为紧凑
  定长 range 和共享 country table，生产镜像需将数据库置于配置路径；
- 完成 violation 日志、violation top 和 network session 查询；top 保留 client/server 排序
  与固定分页参数，session 保留 Scala 的 `f_workload`、`limit=256` Controller 查询映射；
- 完成 violation track，以及 threat 列表、详情、top 和 track；保留严格的正负两小时时间窗、
  ingress workload 选择、threat 方向反转与 endpoint 名称回退，并对 top 去重、排序和补足五项；
- 完成 conversation 删除、history 查询和 unmanaged endpoint 删除；资源 ID 均作为独立
  path segment 编码；
- 完成 unmanaged endpoint 更新和 global notification accept；endpoint 更新保持当前
  cluster 选路，notification accept 固定本地 Controller，并省略 Spray `Option.None` 字段；
- 完成 response policy CRUD、condition options、unquarantine、fed deploy，以及 network
  policy application/rule CRUD、删除和 scope 更新；保留 public POST 到 Controller PATCH、
  federal 固定本地 Controller 和 Spray 可选字段省略语义；
- 完成 Admission Control 全部 14 个公开操作，包括规则/状态、测试、local/fed 导入导出和
  promote；完成 scan status、host、platform、config 与 registry type 首批 8 个操作；
- 完成 policy promote、workload scan、registry CRUD、repository scan、fed repository、
  image/layer report、scan top 和 V2 registry create/update/test/delete；registry test 事务
  会复用首次选定的 API version 与 cluster，并在 delete 后清理；
- 完成 policy 列表、分页和 graph 聚合，以及 workload scan 汇总/详情查询；保留 scope
  本地选路、分页缓存时序、`Option.toString` edge ID 和父子 workload 漏洞数汇总语义；
- 完成 security events 聚合与 DTO 转换；保留事件方向、`Option.None` 字段省略、嵌套
  details JSON 字符串和 `reported_timestamp` 降序语义；
- 完成 dashboard system alerts 和 score metrics 查询；保留 `X-Nv-Page`、global user
  默认值和可选 domain 查询语义；
- 完成 dashboard score metrics 更新和 notification 聚合；保留 Metrics DTO、事件显示名
  回退、top source/destination 分组和按日期的 critical/warning 汇总语义；
- 完成 dashboard details 六资源并发聚合；保留 domain 过滤、应用统计、漏洞排行、policy
  coverage、auto scan 和 Controller 局部失败降级语义；
- 完成 multi-cluster summary；保留嵌套 cluster 选路、summary 原始 JSON 字符串、有界
  owner 缓存降级和 score 请求失败时的全零回退语义；
- 完成 SAML logout response 的 GET/POST 回调；保留根路径 302、HTML body、Content-Type
  以及不附加常规 Web 安全 Header 的兼容性例外；
- 完成 SAML auth server 配置读取；保留本地 Controller 选路、`X-R-SSO: false` 和可选
  serverName 的 redirect DTO 语义；
- 完成 OpenID 无 state 初始化；保留本地配置读取、serverName/Host 分流和 redirect URL
  DTO 语义；带 state 回调和一次性 Token 校验仍未迁移；
- 完成 SAML logout URL 查询；保留本地 Controller 选路、认证 Token 和 logout redirect DTO
  传递语义；

`/auth` 已实现请求包装、客户端 IP、Rancher `R_SESS` 传播、Token DTO 转换、角色数字映射、
email MD5 和登录时间。并发安全的有界 Session Store 保存 Rancher Session，登出时先清除
本地状态。日志不记录 Query、Token、Cookie、密码或请求/响应 Body。

## 2. 验证结果

```text
go test ./...: PASS
go test -race ./...: PASS
go vet ./...: PASS
Python migration tool tests: 14/14 PASS
Scala/Go differential contract: 304/304 PASS
Route contract coverage: 263/263 (100%)
HTTPS ephemeral certificate startup: PASS
```

差分使用同一 HTTPS fixture Controller 和真实 Scala fat JAR，对比认证生命周期、公开
接口、完整 federation cluster 管理、role/API key CRUD、user CRUD、密码策略及首批
device 查询与配置接口、Sigstore/Verifier CRUD，以及 workload/container/domain、
sniffer/PCAP、workload/host scan report，以及 file config 下载、导入和 federation config
导入导出接口，以及完整 group/service/process profile/file profile/DLP/WAF sensor 和
DLP/WAF group、Device benchmark/CSP support、Notification 日志/network、response policy、
network policy core、Admission Control、首批 scan，以及 risk vulnerability/compliance
profile 垂直切片。
切换后 cluster-aware 用例命中联邦成员 URL，而 federation 管理、IBM SA setup、usage 和
federal webhook/config 查询仍固定命中本地 Controller；同时覆盖 EULA、license、server
和 debug 的剩余 ExtraAuth 行为。
除 `Date` 和 `Server` 两个预先批准的非确定 Header 外，状态、Header 和 Body 全部一致。
登录和 self 只忽略动态 `/login_timestamp` 值，字段存在性和其他内容仍参与比较。
Scala smoke 将同一进程同时作为左右端；`scan-registry-test-delete` 首次请求会按 Scala
语义清理事务 target，第二次重放因此返回 500。两个独立进程的 Scala/Go 差分中该操作
为 200/200，不属于实现差异。

同一台开发机的 HTTP/mock 空闲采样中，Go POC 的 RSS/HWM 为 10,528 KiB、9 个线程、7 个
FD，静态二进制为 22,100,535 bytes。该数据只证明 POC 有明显的资源优化潜力；它与既有
Scala 本机参考未使用完全相同的采样时长和正式镜像，不能据此宣告 RSS 门禁通过。

## 3. 尚未完成

剩余工作的任务编号、优先级、依赖和验收条件统一维护在
[`05-remaining-work.md`](05-remaining-work.md)；本节仅保留状态摘要。

- 已实现并由 Manifest 覆盖 263/263 个语义 HTTP 操作；静态 UI 资源不属于本轮后端迁移范围；
- SAML/OIDC 带 state 回调、一次性 Token 和完整 Cookie 生命周期仍需独立安全验收；
- 尚未选定和验证 FIPS Go 工具链、密码模块及基础镜像；
- 尚未执行正式容器内 RSS、吞吐、P95/P99、故障和大文件基准；
- 当前 fixture 为合成数据，不能替代批准的测试 Controller 样本。

## 4. 下一批工作

1. 通过 Security/FIPS Owner 确认 Go 工具链和镜像，关闭 G0 前置项；
2. 对 SAML/OIDC 登录跳转与 Cookie 生命周期开展独立安全设计和迁移；
3. 从路由清单选择下一组完整垂直切片，并先对每个参数分流、DTO 和 Controller 选路
   完成 Scala/Go 差分验证；
4. 将 100% 路由覆盖的 M2 Manifest 纳入 CI，并保持 strict coverage 门禁；
5. 构建最小镜像后执行同条件资源与延迟基准，验证 RSS 至少降低 50%。
