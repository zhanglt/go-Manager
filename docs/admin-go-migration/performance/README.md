# RW-003 性能与稳定性门禁

本目录定义 Scala/Pekko 与 Go/Gin Manager 的可复现发布门禁。工具只写指标、状态码计数、
制品摘要和脱敏后的配置元数据，不保存请求或响应正文，也不应在配置中写入真实 Token。

## 正式执行

在固定的专用节点或相同 cgroup/容器限制中准备 release Scala JAR 与 Go 二进制，确认
`formal-gate.json` 中的制品路径、CPU/内存/FD 限制与运行节点一致，并在外层限制实际生效后
将 `resource_limits.enforced` 改为 `true`、记录真实的 `enforced_by`，然后执行：

```bash
python3 tools/migration/performance_gate.py run \
  --config docs/admin-go-migration/performance/formal-gate.json \
  --output /evidence/rw003/performance-report.json \
  --keep-logs /evidence/rw003/logs
```

默认顺序为：每个实现对 idle、steady、burst、50 MiB Body、50 MiB 上传、50 MiB 下载、
缓存压力和 Controller 间歇故障各执行三次独立运行；随后仅对候选 Go Manager 执行一次
24 小时混合流量。Scala 和 Go 顺序占用同一个 `18443` 端口，使用同一个 HTTPS Controller
fixture、预热时间、场景时长、并发、RPS 和采样间隔，避免同时运行造成资源争用。

正式节点必须在 runner 外层落实 `formal-gate.json` 记录的 2 CPU、2 GiB 内存和 4096 FD
限制；脚本记录并强制检查 `enforced` 声明，但不会以 `RLIMIT_AS` 模拟容器内存，因为 JVM
的虚拟地址预留会使该方式产生错误结果。证据包应另存节点规格、容器/cgroup 配置、镜像
digest 和执行命令；未确认限制时保留默认 `false`，报告必定 FAIL。

报告内嵌 gate 结论，也可在归档后重新判定：

```bash
python3 tools/migration/performance_gate.py check \
  --report /evidence/rw003/performance-report.json \
  --output /evidence/rw003/gate-result.json
```

任一条件不满足时命令返回非零：Go RSS 超过 Scala 的 50%、burst 吞吐低于 Scala、任一
负载 P95 超过 Scala 的 110%、出现非预期请求错误、进程退出，或 24 小时结束时 RSS、
goroutine、FD 增长超限。正式性检查还要求九个场景齐全、短场景至少三次、长稳达到
86,400 秒且 `duration_scale=1`，因此短时 smoke 不可能产生可发布的 PASS 结论。

## 开发演练

`--duration-scale` 仅用于快速验证工具和 fixture，例如：

```bash
python3 tools/migration/performance_gate.py run \
  --config docs/admin-go-migration/performance/formal-gate.json \
  --output /tmp/manager-performance-smoke.json \
  --scenario idle --scenario steady --runs 3 --duration-scale 0.01
```

演练报告会因缺少正式场景或时长而返回非零，这是预期行为。不要把演练数值复制到发布
证据。大 Body 和 multipart Body 在内存中按场景构造，不写临时上传文件；50 MiB 下载由
fixture 按长度生成，也不在仓库中保存大型二进制。

## 指标与泄漏判定

每次采样聚合 Manager 进程树的 RSS、HWM、CPU 时间、thread、FD 和子进程数；Go 的内部
metrics listener 额外提供 goroutine。负载结果包含吞吐、P50/P95/P99、响应字节数、状态码、
传输故障和非预期错误。24 小时报告保留一分钟粒度原始采样并计算每小时线性趋势及首尾
增量，便于复核持续内存、goroutine、连接/FD 和命令子进程增长。

Controller 故障 fixture 按成功、503、主动断连、250 ms 延迟循环。主动断连计入
`transport_errors`，但作为该场景的预期注入不计入 gate 的 `errors`；其他场景的传输故障
仍会直接阻断。正式评审除机器判定外还必须检查进程日志中是否存在 panic、崩溃、死锁、
OOM、连接池耗尽或持续重试风暴。
