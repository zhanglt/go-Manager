# RW-010 本地发布资格演练（2026-08-05）

## 结论

当前结论为 **NO-GO / FAIL-CLOSED**。本轮已验证发布构建、镜像结构、证明材料检查器、
合成契约工具、缩时稳定性工具和部分依赖安全工具，但本地 OCI 制品不是已推送、签名和批准的
正式 Release Candidate。八类正式证据和五类 Owner 签字尚未齐全，不得勾选 RW-010。

## 本地候选制品

构建输入为 `VERSION=rw010-local-rc`、`COMMIT=73818a6b`、
`BUILD_DATE=2026-08-05T04:00:00Z`，平台为 `linux/amd64,linux/arm64`。两个 OCI 均包含 SPDX
SBOM 和 SLSA provenance v1；以下 digest 只标识本地 OCI index，不是 registry 中的正式 RC：

| 制品 | OCI index digest | amd64 manifest | arm64 manifest |
| --- | --- | --- | --- |
| 普通 | `sha256:473066bb3a4022ca8a48240123b1371735dd8c90ee1dc2e27366771b001b2710` | `sha256:ca39e3181a403a9cb72050eb88437572a8d64f0e498e164ca23d0f894be8f26c` | `sha256:8ea9442fa331522f4110dc76197b6b4e86fe12027a8248efda060f06fcf3bf3b` |
| FIPS | `sha256:b7117a30d33c0b23beb728ce5dc0ca30a9666c961f9b42e6830d3023064134c2` | `sha256:e865bc7ea5b60250fabd080b18eacee1d067c79c9b4ecb95cec1614409e3faf9` | `sha256:9071373026ad669a7b86fdb8f6e2668d9ae439377ec8c561df52ef39210022ae` |

amd64 普通和 FIPS runtime 验证均通过：UID/GID `1000:1000`、non-root、只读 rootfs、
`cap-drop ALL`、运行数据与 CLI 完整、无 JVM/JAR/class，并完成 HTTP smoke。FIPS 镜像还验证了
`GODEBUG=fips140=only` 和 FIPS label。普通/FIPS 镜像大小分别为 86,404,610 和 86,404,954
bytes。

生产 Dockerfile 已对齐 `go.mod` 的 Go 1.26.5 多架构 builder digest，并使用 ZYpp
`--gpg-auto-import-keys` 安全处理 SUSE BCI 签名 key 轮换。根 `sbt assembly` 的 common 资源合并
策略也已修复并验证通过。

## 已执行检查

| 检查 | 本地结果 | 发布含义 |
| --- | --- | --- |
| Go test/race/vet/gofmt | PASS | 代码质量 smoke 通过 |
| Angular lint/format、生产依赖 audit | PASS，runtime 依赖 0 漏洞 | 开发工具链仍有 34 个既有 audit 告警，需继续跟踪 |
| 路由覆盖 | PASS，263/263；manifest 308 cases | 合成覆盖完整，不等同真实 Controller E2E |
| Scala/Go 合成契约 | FAIL，300/308；39 个未解释差异 | 8 个既知差异尚无 QA 批准 |
| 同版本契约复验 | `m2-authenticated-version-get` PASS | 额外第 9 个失败仅由版本标签不同造成，未扩大 normalization |
| 性能 smoke | FAIL | Scala burst 三轮触发 Pekko pool `max-open-requests=32` 溢出并大量返回 500 |
| 稳定性 smoke | PASS，86.4 秒、0 请求错误、进程存活 | 仅验证工具链；正式 gate 因非 24 小时且未落实资源限制而 FAIL |
| Go license | PASS | 固定许可证白名单通过 |
| `govulncheck` | PASS，0 个可达漏洞 | 另有 3 个 imported-package、21 个 required-module 非可达漏洞待持续跟踪 |
| Python `pip-audit` | PASS | `package/requirements.txt` 无已知漏洞 |
| gitleaks | PASS | 对仅含源码与当前改动的 83 MB 快照未发现泄漏 |

稳定性演练首次发现 `performance_gate.py` 未生成 `processes_growth`，导致判定阶段抛出
`KeyError`。现已补齐 child-process growth/slope 汇总和回归测试；重跑后 smoke gate 正常通过。

## 正式阻断项

- 合成契约仍有 8 项未批准差异：4 项 SSO capability/Cookie、2 项 debug 授权和 2 项错误响应。
- 尚无代表性真实 Controller、真实 IdP 和浏览器/CLI 八条关键路径 E2E 报告。
- 尚无固定 2 CPU、2 GiB、4096 FD 环境下九场景三轮正式性能报告；Scala burst smoke 当前失败。
- 尚无同一不可变候选上的 24 小时稳定性、Controller 故障和资源泄漏正式报告。
- 尚无 Scala 到 Go upgrade 及 Go 到冻结 Scala digest 的 rollback 演练，配置、证书、CLI、UI、
  session、readiness 和恢复时间均未形成证据。
- SUSE 构建日志中的 `/chroot` RPM `NOKEY` warning，以及 47 个标记为 vendor unsupported 的 BCI
  包，尚未获得 Security/Release 审批；Go FIPS 工具链和基础镜像也未获 Security/FIPS Owner 签字。
- 本地 OCI 尚未推送到批准 registry、签名并冻结为正式普通/FIPS immutable RC digest。
- QA、Security/FIPS、Performance、Release、Product 五类签字均未提供；分支 required checks 的
  外部保护设置也未完成。

只有上述阻断全部关闭，并由 `release_qualification_gate.py` 对八类真实报告和五类签字输出
`passed: true`，才能将 RW-010 改为完成。
