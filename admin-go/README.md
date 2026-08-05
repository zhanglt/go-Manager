# Admin Go POC

`admin-go/` 是 Scala Admin 后端的 Go/Gin 迁移 POC。它用于验证 API、认证生命周期、静态资源
服务和部署方案，尚不能替代生产 Manager，也尚未通过 FIPS、G0、正式性能和安全门禁。

## 当前覆盖范围

POC 当前覆盖以下领域：

- **认证与管理**：认证生命周期、完整 ExtraAuth、federation cluster、用户、角色、API key、密码策略。
- **Device**：enforcer、controller、scanner、summary、usage、host、workload、compliance、
  file config、webhook、V1/V2 system config、remote repository、Docker/Kubernetes benchmark、
  CSP support package 和 support debug log 生命周期。
- **Workload**：workload/container 查询、隔离、monitor、compliance、分页 scanned、domain、
  scan report、sniffer 生命周期和 PCAP 流式下载。
- **Group**：group-list、custom check、group CRUD、导入导出、service、process/file profile、
  DLP/WAF sensor CRUD、导入导出以及 DLP/WAF group 管理。
- **Policy**：response/network policy 核心操作、全部 14 个 Admission Control 操作、scan、
  registry/repository/image/layer/top、V2 registry test、policy 分页/graph 和 workload scan。
- **Risk**：CVE asset、assets-view、vulnerability profile/entry、scanned-assets/vulasset、
  compliance profile/asset/template/filter。
- **Notification**：IP geolocation、event、incident、audit/audit2、violation、threat、network
  session、conversation/history/endpoint、network graph/layout/blacklist、security-events、
  global notification accept；audit2 分页逐页验证 Token。
- **Dashboard**：system alerts、details 聚合、score metrics、notification 和 multi-cluster summary；
  details 对六类 Controller 资源并发读取，并保留局部失败降级。

实现保持了 Scala 的关键兼容行为，包括 Controller target 锁定、cluster-aware transfer、分页
缓存容量/TTL/认证生命周期约束、可选字段省略、事件方向和时间戳排序。缓存与 Scala 磁盘
Ehcache 不同，进程重启后不会保留。

生产 Angular build 已嵌入 Go binary，支持版本 Hash 跳转、`PATH_PREFIX`、预压缩 JavaScript、
静态 Content-Type 和页面深层链接 fallback；已注册 API 路由始终优先。

## 本地运行

在仓库根目录执行以下命令可完成 UI 构建、静态资源同步和 Go 编译：

```bash
make manager MANAGER_BINARY="$PWD/admin-go/bin/manager"
```

也可以在本目录中单独编译和启动：

```bash
go test ./...
go build -trimpath -o bin/manager ./cmd/manager

MANAGER_SSL=off \
MANAGER_SERVER_PORT=18444 \
MANAGER_SUPPORT_COMMAND="$(realpath ../scripts/support)" \
./bin/manager
```

默认 Controller 地址为 `https://127.0.0.1:10443/v1`。常用环境变量如下：

### Controller 与 Manager

- `CTRL_SERVER_IP`、`CTRL_SERVER_PORT`：Controller 地址和端口。
- `CTRL_TLS_VERIFY=false`：保持当前 Controller 证书兼容模式；生产环境应按安全评审设置。
- `CTRL_REQUEST_TIMEOUT=60s`：Controller 请求总超时。
- `MANAGER_SERVER_PORT`、`MANAGER_SSL`：Manager 监听端口和 TLS 开关。
- `MANAGER_CERT_FILE`、`MANAGER_KEY_FILE`：HTTPS 证书和私钥。两者为普通文件时加载 PEM
  chain 与 PKCS#1/PKCS#8 RSA key，否则按 Scala 兼容语义生成仅驻留内存的 RSA-2048/SHA-256
  临时自签名证书。生产部署必须挂载受信任证书。
- `MANAGER_SHUTDOWN_TIMEOUT=30s`：收到 SIGTERM/SIGINT 后的退出期限。
- `MANAGER_INTERNAL_ADDR`：非空时启用独立的 `/livez`、`/readyz` 和 `/metrics` HTTP listener；
  该 listener 不提供 TLS 或认证，必须由 NetworkPolicy 隔离，禁止暴露到用户入口。
- `HTTP_MAX_HEADER_LENGTH=32k`、`MANAGER_MAX_BODY_BYTES=50m`：请求 Header 与 Body 上限。
- `MANAGER_MAX_CONNECTIONS=1024`：公开和内部 listener 各自允许的并发连接上限。
- `MANAGER_READ_HEADER_TIMEOUT=10s`、`MANAGER_READ_TIMEOUT=2m`、
  `MANAGER_WRITE_TIMEOUT=15m`、`MANAGER_IDLE_TIMEOUT=2m`：HTTP 读头、完整读取、响应写入和
  keep-alive 空闲超时。较长的写入期限为流式下载保留空间，但仍保证连接最终释放。
- `PATH_PREFIX`：静态资源和 API 的路径前缀。
- `IS_DEV=true`：使用未压缩 JavaScript；其他值使用生产 `.js.gz`。

### SAML/OIDC

- `MANAGER_PUBLIC_URL`：浏览器可访问的 Manager HTTPS origin，例如
  `https://manager.example:8443`。生产环境必须配置；回调地址只由该值和 `PATH_PREFIX` 生成，
  不信任请求中的 `Host`。不得包含路径、凭据、查询或 fragment。
- `MANAGER_SSO_STATE_TTL=5m`：OIDC state、浏览器关联值和一次性登录结果的有效期。
- `MANAGER_SSO_MAX_PENDING=1024`：每个进程允许的待完成 OIDC 流程及待领取登录结果上限。

SSO 登录结果使用随机 `nv_sso_handoff` Cookie 一次性交付；该 Cookie 设置 `HttpOnly`、
`Secure` 和 `SameSite=Lax`。Angular 可见的 `temp` 仅是无权限 marker，不包含 Token 或关联 ID。
当前临时状态保存在进程内，因此多副本部署必须对从 SSO 发起到 `PATCH /token_auth_server` 或
`PATCH /openId_auth` 的完整链路启用粘性会话。未命中原实例会安全返回 `401`，不会回退到其他
用户的结果。生产环境必须保持 `MANAGER_SSL=on`；HTTP 模式仅用于本地开发。

### 缓存、数据与 Support

- `MANAGER_SESSION_MAX_ENTRIES=10000`：进程内 Token Session 上限。
- `MANAGER_CACHE_MAX_ENTRIES=1000`、`MANAGER_CACHE_MAX_BYTES=64m`、`MANAGER_CACHE_TTL=5m`：
  分页响应缓存的条目数、字节数和存活时间。
- `MANAGER_SUPPORT_COMMAND=/usr/local/bin/support`：固定 support executable，必须是不可被
  group/world 写入的普通可执行文件。
- `MANAGER_SUPPORT_TEMP_DIR=/tmp/neuvector-support`：专用输出目录，权限不得宽于 `0700`，且
  必须由当前 UID 所有。
- `MANAGER_SUPPORT_TIMEOUT=10m`、`MANAGER_SUPPORT_MAX_FILE_BYTES=64m`、
  `MANAGER_SUPPORT_MAX_CONCURRENT=2`：Support 收集超时、文件大小和并发上限。
- `IP_GEO_IPV4_DB`、`IP_GEO_IPV6_DB`：IP2Location CSV 路径；生产镜像必须提供两个文件。
- `CIS_NIST_DB`：CIS-to-NIST CSV 路径；生产镜像默认查找
  `/usr/share/neuvector/CIS_NIST-MASTER.CSV`。

Support 收集不需要 Linux capability。Token 与 Rancher session 仅通过子进程环境传递，不出现在
命令行或日志；结果使用 `0600` 原子写入，并在下载完成、中断、失败、超时、替换或 Manager
关闭时删除。生产启动会完整加载并校验 IP 与 CIS/NIST 数据库；任一数据文件缺失或损坏时不会
绑定服务端口，也不会报告 ready。

### 可观测性

`/metrics` 使用 Prometheus 文本格式，覆盖规范化路由的请求量、状态与延迟、登录失败、
Controller 请求/失败/可用性、缓存容量/字节/驱逐、session 容量、goroutine、RSS，以及 support
命令的运行数、结果、耗时和下载字节。标签仅包含有界的 method、route、status、cache、reason
和 outcome；不会包含 Token、凭据、用户名、原始 URL/query、cluster ID 或文件名。

可部署的告警规则和 Grafana dashboard 位于 `deploy/observability/`。初始告警覆盖 5xx、登录失败、
P95/P99、Controller 可用性、缓存/session 饱和、RSS、goroutine 和超时 support 命令。发布前应按
容器内存限制、Scala 性能基线及 RC 压测结果复核阈值。

## 构建与打包

在仓库根目录运行：

```bash
make build-image VERSION=dev TAG=dev
make verify-image TAG=dev

make build-fips-image VERSION=dev TAG=dev
make verify-fips-image TAG=dev
make test-images
make package
```

`make test-images` 对 `linux/amd64,linux/arm64` 执行不发布的跨架构构建。发布目标通过
Buildx 生成 SBOM 和 provenance；`runtime-fips` 设置 `GODEBUG=fips140=only`，但只有
Security/FIPS Owner 批准工具链、基础镜像和证据后才能作为认证 FIPS 制品发布。

运行时使用 UID/GID `1000:1000`，证书通过只读文件挂载。网络受限时可覆盖依赖源：

```bash
make build-image \
  GOPROXY=https://goproxy.cn,direct \
  PIP_INDEX_URL=https://pypi.tuna.tsinghua.edu.cn/simple
```

`make package` 默认输出传统目录制品到 `stage/`，只包含 Manager binary、CLI、support command、
数据文件和许可证，不生成 Scala/JAR 制品。

普通模式对 index cache key 和 `emailHash` 使用 Scala 兼容 MD5；FIPS-only 模式改用 SHA-256。
这两个字段只用于 cache busting/Gravatar ID，不得用于认证、签名或完整性判断。

## 版本信息注入

构建时可以注入与 Scala 相同的版本值：

```bash
go build -trimpath \
  -ldflags "-X github.com/neuvector/manager/admin-go/internal/buildinfo.Version=<version>" \
  -o bin/manager ./cmd/manager
```

正式发布必须使用 Security/FIPS Owner 批准的 Go 工具链和构建参数。

## 契约验证

先启动 fixture Controller、Scala Manager 和 Go Manager，再运行：

```bash
export MANAGER_TEST_TOKEN=fixture-token-1234567890

python3 tools/migration/contract_runner.py \
  --manifest docs/admin-go-migration/baseline/contract-manifest.m2-poc.json \
  --left-url http://127.0.0.1:18443 \
  --right-url http://127.0.0.1:18444 \
  --output /tmp/manager-m2-contract.json
```

报告只保存响应长度、Hash、脱敏 Header 和差异路径，不保存响应正文。当前合成 fixture 的独立
Scala/Go 差分为 `304/304`，Manifest 覆盖 `263/263` 个语义路由（100%），没有未知、歧义或
重复 case ID。这些结果只证明当前 POC 范围的兼容性，不能替代真实 Controller 样本、正式
性能和安全门禁。
