# Admin Go POC

该目录是 Scala Admin 后端透明迁移的 Go/Gin POC。它目前实现认证生命周期、完整
ExtraAuth、federation cluster 管理、用户/角色/API key/密码策略管理，不能替代生产
Manager。device 领域已覆盖 enforcer、controller、scanner、summary、usage 和 host 的
只读查询、file config 下载/transaction/multipart 导入和 federation config 导入导出，
以及 webhook、V1/V2 system config 和 remote repository 管理。当前 POC
还实现了 Sigstore root of trust 与 verifier CRUD；不代表已通过 FIPS、G0 或性能门禁。
workload 领域当前覆盖 workload/container 基础查询、隔离、monitor、compliance、分页
scanned 和 domain 配置，以及 workload/host scan report、sniffer 生命周期和 PCAP 流式下载。
device 领域还覆盖 Docker/Kubernetes benchmark 查询与触发、CSP support 包下载，以及
Token/cluster 隔离、限时授权和下载后清理的 support debug log 生命周期。
group 领域覆盖导入导出、group-list、custom check、group CRUD、service、process/file profile，
以及 DLP/WAF sensor CRUD/导入导出和 DLP/WAF group 查询、更新。
policy 领域当前覆盖 response policy CRUD/import/export、network policy 的核心操作、
Admission Control 全部 14 个公开操作，以及 scan status/host/platform/config、registry
CRUD/repository/image/layer/top 和 V2 registry test；事务请求会锁定 Controller target；
同时覆盖 policy 列表分页、policy graph 和 workload scan 查询/汇总；group 与 policy 共用
受限的 cluster-aware transfer 代理，分页缓存受统一容量、TTL 和认证生命周期约束。
risk 领域当前覆盖 CVE asset 查询和 assets-view、vulnerability profile 的查询、更新、
entry 管理和导入导出，scanned-assets/vulasset 查询与分页，以及 compliance
asset/template/filter 和 profile 管理。
notification 领域当前覆盖 IP geolocation、event、incident、audit/audit2、violation 日志查询、
violation top/track、threat 列表/详情/top/track、network session、conversation/history/endpoint
操作、network graph/layout/blacklist、security-events/security-events2，以及 global notification accept。network graph
保留 Scala 的节点/边转换、GPU 开关和用户布局/黑名单状态；其缓存按 Token、cluster、user
隔离，并受统一容量和 TTL 限制（与 Scala 磁盘 Ehcache 不同，重启后不保留）。security-events 保留
Scala 的事件方向、可选字段省略、details JSON 字符串和时间戳排序语义；audit2 分页缓存逐页验证 Token；
IP 数据库首次请求时惰性加载为紧凑 range，避免增加进程启动期常驻内存。
dashboard 领域当前覆盖 system alerts、details 聚合、score metrics 查询/更新和 notification 聚合，并保留
Controller 页面来源 Header、global user 默认值、domain 查询、事件显示名回退、top 分组和
按日期的 critical/warning 汇总语义；details 对六类 Controller 资源并发读取，并保留局部
失败降级；multi-cluster summary 支持嵌套 cluster 选路、并发 summary/score 获取、有界
summary 降级缓存和 score 请求失败时的全零回退。
生产 Angular build 已嵌入 Go binary，并支持版本 Hash 跳转、`PATH_PREFIX`、预压缩
JavaScript、静态 Content-Type 和页面深层链接 fallback；已注册 API 路由始终优先。

## 本地运行

```bash
go test ./...
go build -trimpath -o bin/manager ./cmd/manager
MANAGER_SSL=off MANAGER_SERVER_PORT=18444 \
  MANAGER_SUPPORT_COMMAND="$(realpath ../scripts/support)" ./bin/manager
```

默认连接 `https://127.0.0.1:10443/v1`。常用兼容配置包括
`CTRL_SERVER_IP`、`CTRL_SERVER_PORT`、`MANAGER_SERVER_PORT`、`MANAGER_SSL`、
`HTTP_MAX_HEADER_LENGTH`、`PATH_PREFIX` 和 `IS_DEV`。`IS_DEV=true` 时 JavaScript 使用
未压缩的嵌入资源；其他值均使用生产 `.js.gz`。POC 新增：

- `CTRL_TLS_VERIFY=false`：保持当前 Controller 证书兼容模式；正式环境需按安全评审设置；
- `CTRL_REQUEST_TIMEOUT=60s`：Controller 请求总超时；
- `MANAGER_SHUTDOWN_TIMEOUT=30s`：收到 SIGTERM/SIGINT 后的退出期限；
- `MANAGER_INTERNAL_ADDR`：非空时启用独立的 `/livez`、`/readyz` HTTP Listener；
- `MANAGER_SESSION_MAX_ENTRIES=10000`：限制进程内 Token Session 数量；
- `MANAGER_CACHE_MAX_ENTRIES=1000`：限制分页缓存条目数；
- `MANAGER_CACHE_MAX_BYTES=64m`：限制分页响应缓存和单次解码的字节预算；
- `MANAGER_CACHE_TTL=5m`：设置分页缓存的存活时间；
- `MANAGER_SUPPORT_COMMAND=/usr/local/bin/support`：固定 support executable；必须是不可被
  group/world 写入的普通可执行文件；
- `MANAGER_SUPPORT_TEMP_DIR=/tmp/neuvector-support`：专用输出目录；启动时创建并校验为当前
  UID 所有、权限不宽于 `0700`；
- `MANAGER_SUPPORT_TIMEOUT=10m`：单次收集的硬超时；超时或 Manager 退出时终止整个进程组；
- `MANAGER_SUPPORT_MAX_FILE_BYTES=64m`：gzip 文件及解压校验的最大字节数；
- `MANAGER_SUPPORT_MAX_CONCURRENT=2`：全局并发收集上限，超限请求返回 HTTP 429；
- `IP_GEO_IPV4_DB`、`IP_GEO_IPV6_DB`：可选的 IP2Location CSV 路径；默认依次查找源码
  resources 和 `/usr/share/neuvector/`；生产镜像必须提供这两个数据文件；
- `CIS_NIST_DB`：可选的 CIS-to-NIST CSV 路径；默认查找源码 resources 和
  `/usr/share/neuvector/CIS_NIST-MASTER.CSV`；
- `MANAGER_CERT_FILE`、`MANAGER_KEY_FILE`：HTTPS 证书和私钥路径；两者均为普通文件时
  加载现有 PEM chain 和 PKCS#1/PKCS#8 RSA key，否则按 Scala 兼容语义生成仅驻留内存的
  RSA-2048/SHA-256 临时自签名证书。生产部署必须提供受信任证书，不能依赖该回退。

POC 构建可注入与 Scala 相同的版本值，例如：

```bash
go build -trimpath \
  -ldflags "-X github.com/neuvector/manager/admin-go/internal/buildinfo.Version=<version>" \
  -o bin/manager ./cmd/manager
```

正式发布构建必须改用 Security/FIPS Owner 批准的 Go 工具链和构建参数。

## 生产镜像与本地打包

生产镜像由仓库根目录的多阶段 Dockerfile 构建；Angular、Go binary 和 Python CLI 都在
隔离的 builder stage 中生成，最终 SUSE BCI Micro 镜像不包含 JDK、SBT 或 Manager JAR：

```bash
make build-image VERSION=dev TAG=dev
make verify-image TAG=dev
make build-fips-image VERSION=dev TAG=dev
make verify-fips-image TAG=dev
```

`make test-images` 对 `linux/amd64,linux/arm64` 执行不发布的跨架构构建。发布目标通过
buildx 生成 SBOM 和 provenance；`runtime-fips` target 设置 `GODEBUG=fips140=only`，但只有
Security/FIPS Owner 批准工具链、基础镜像和证据后才能作为认证的 FIPS 制品发布。运行时以
UID/GID `1000:1000` 启动，证书应通过部署配置挂载并使用 `MANAGER_CERT_FILE` 和
`MANAGER_KEY_FILE` 指向只读文件。网络受限环境可通过 `GOPROXY` 覆盖 Go module proxy，
并通过 `PIP_INDEX_URL` 覆盖 Python package index，例如：

```bash
make build-image \
  GOPROXY=https://goproxy.cn,direct \
  PIP_INDEX_URL=https://pypi.tuna.tsinghua.edu.cn/simple
```

support 收集不需要 Linux capability；镜像验证以 `--cap-drop ALL`、只读 rootfs 和
`/tmp` tmpfs 启动。Token 与 Rancher session 仅通过子进程环境传递，不出现在命令行或日志；
结果使用 `0600` 原子写入，下载完成或中断、失败、超时、替换及 Manager 关闭时均删除。

普通模式继续为 index cache key 和 `emailHash` 返回 Scala 兼容的 MD5；FIPS-only 模式不能
调用 MD5，因此对这两个非安全标识使用 SHA-256。前端只将其作为 cache busting/Gravatar ID，
不得把这些字段用于认证、签名或完整性判断。

需要生成传统目录制品时可运行 `make package`，默认输出到 `stage/`。该目录只包含 Manager
binary、CLI、support command、数据文件和许可证，不再生成 Scala/JAR 制品。

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

报告只保存响应长度、Hash、脱敏 Header 和差异路径，不保存响应正文。
当前合成 fixture 的独立 Scala/Go 全量差分为 304/304；Manifest 覆盖清单中的
263/263 个语义路由（100%），且无未知、歧义或重复 case ID。这些结果只证明当前合成
POC 范围的兼容性，不能替代真实 Controller 样本、正式性能和安全门禁。
