# RW-010 完整发布资格门禁

本目录定义 G5 的最终机器门禁。它不替代 RW-003、RW-004 或 Security/FIPS 的专项评审，
而是确保八类最终报告、缺陷和限制台账、五类 Owner 签字都绑定同一组不可变普通/FIPS
多架构镜像。任何占位符、tag-only 镜像、报告 hash 漂移、缺失覆盖、未关闭 P0/P1 或签字
digest 不一致都会返回非零。

## 1. 冻结候选镜像

从批准 registry 解析普通和 FIPS manifest digest，并分别记录 amd64、arm64 manifest digest。
`candidate.image.reference` 和 `candidate.fips_image.reference` 必须使用 `name@sha256:...`，禁止
使用可变 tag。`built_at` 之后重新构建的镜像是新的候选版本，原测试和签字不得沿用。

可用 registry 已批准的检查工具获取 digest；以下仅展示 Docker CLI 的读取方式：

```bash
docker buildx imagetools inspect --raw NAME@sha256:DIGEST
```

## 2. 生成八类证据

将 `evidence-template.json` 复制到受控证据目录并填写以下报告。报告本身可以保留在 QA 或
发布系统，但门禁执行节点必须能读取；`sha256` 使用带前缀的完整
`sha256:<64 lowercase hex>`。每条记录还要写明隔离环境、执行时间和工单。

| 类别 | 来源和最低要求 |
| --- | --- |
| `contract` | RW-004 真实 Controller gate；全路由、错误、gzip、Header、Cookie |
| `e2e` | QA UI/CLI workflow；八条关键路径全部在 Go backend 通过 |
| `performance` | RW-003 正式 gate；不得使用 `duration_scale` smoke 结果 |
| `stability` | 同一候选镜像 24 小时混合流量、Controller 故障、泄漏判定 |
| `security` | TLS/CSP、依赖、SBOM/provenance、秘密和最终镜像权限检查 |
| `fips` | Security/FIPS 批准的工具链、基础镜像、运行模式及两架构证据 |
| `upgrade` | Scala 到 Go；配置、证书、打包 CLI/UI 和 session 行为 |
| `rollback` | Go 到已冻结 Scala digest；readiness、恢复时间和重新登录行为 |

升级演练必须复用同一配置和只读证书挂载，先用 Scala 验证 UI/CLI，再切换到候选 Go 镜像；
除已登记的进程内 session 失效外不得出现行为回归。回滚演练必须恢复已冻结的 Scala digest，
记录从触发阈值到 readiness 的时间，且不得执行数据回滚。真实凭据、Token、Cookie、私钥、
客户数据和原始响应不得写入本配置或仓库。

## 3. 登记缺陷和限制

`defects` 必须包含资格周期发现的问题及其 `id`、`severity`、`status`。P0/P1 只有状态为
`closed`、`resolved` 或 `verified` 才能通过。每个已接受限制必须填写影响、规避方式、Owner、
目标版本、批准工单和日期；没有限制时使用空数组，不得保留模板示例。

## 4. 计算签字指纹并关闭 G5

第一次运行预期失败，但输出仍包含稳定的 `qualification_digest`：

```bash
python3 tools/migration/release_qualification_gate.py \
  --config /evidence/rw010/evidence.json \
  --output /evidence/rw010/gate-result.pre-sign.json
```

QA、Security/FIPS、Performance、Release 和 Product Owner 审查同一证据包后，各自在对应
approval 中填写：

```json
{
  "approved": true,
  "approved_by": "owner identity",
  "approval_ticket": "G5-123",
  "approved_at": "2026-08-05T03:00:00Z",
  "image_digest": "sha256:...",
  "fips_image_digest": "sha256:...",
  "qualification_digest": "sha256:..."
}
```

修改候选镜像、报告元数据、缺陷或限制会改变资格指纹，使全部旧签字自动失效。填完五类
签字后重新执行；只有输出 `passed: true` 且命令返回 0，RW-010 才能勾选完成。最终归档
`evidence.json`、八类原始报告、`gate-result.json`、镜像 manifest、SBOM、provenance 和签字
工单。归档系统还应保留访问控制和留存策略，仓库只保留无敏感信息的模板与最终摘要。
