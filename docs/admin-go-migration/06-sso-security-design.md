# SAML/OIDC 安全设计与验证

最后更新：2026-08-04

## 1. 范围与安全目标

RW-002 覆盖浏览器、Go Manager、Controller 和外部 IdP 之间的 SAML/OIDC 登录交接。保护资产包括
OIDC `state`/`code`、SAML assertion、Controller Token、临时登录结果、Cookie 和回调地址。
目标是阻止跨用户结果覆盖、重放、session fixation、Host header poisoning 和日志泄漏；任何
过期、缺失、错误实例或不匹配状态均 fail closed。

## 2. 威胁与控制

| 威胁 | 控制 |
| --- | --- |
| 固定临时键导致并发用户串线 | 每次成功回调生成 256-bit 随机 handoff ID，以该 ID 独立存储结果 |
| handoff 被重复使用 | `PATCH` 按 Cookie ID 原子 `Take`，首次读取后立即删除，成功和失败均清 Cookie |
| OIDC CSRF、state/code 重放 | 发起登录时解析 Controller redirect 中的 `state`，绑定随机 HttpOnly flow Cookie；回调恒定时间比较并原子消费 |
| state 或结果长期驻留/内存耗尽 | 两类 Store 均有 5 分钟默认 TTL、1024 默认容量及惰性过期清理；OIDC 参数限制为 4096 bytes |
| Cookie 窃取或 fixation | capability Cookie 使用随机值、`HttpOnly; Secure; SameSite=Lax; Path=<prefix>/`；旧 marker 仅供 Angular 检测，不承载权限 |
| Host header poisoning/open redirect | 生产回调 origin 固定来自 `MANAGER_PUBLIC_URL`；URL 配置拒绝凭据、路径、query、fragment 和非 HTTPS |
| Controller 返回畸形认证结果 | 非 200、无 OIDC state、无 Token 或空 Token 均拒绝，不创建 handoff |
| 凭据进入日志 | access log 只记录 method/path/status/时延；代码不记录 assertion、state、code、Cookie 或 Token |

SAML POST callback 本身由 Controller/IdP 验证 assertion；Manager 不解析或记录 assertion。Manager
侧随机 handoff 只负责把已验证结果安全交给发起该浏览器后续 `PATCH` 的客户端。

## 3. 状态机

OIDC 流程：

1. 浏览器请求 `GET /openId_auth?serverName=openId1`。
2. Manager 使用固定 public callback URL 请求 Controller，解析 redirect URL 中的 `state`，保存
   `flow ID -> state`，并设置短期 `nv_oidc_flow` HttpOnly Cookie。
3. IdP 回调必须同时携带非空 `code`、原 `state` 和 flow Cookie。Manager 原子移除 flow；缺失、
   错配、重放或过期返回 `400`，且不向 Controller转发 code。
4. Controller 验证成功后，Manager 保存 `handoff ID -> login result`，设置 `temp` marker 与
   `nv_sso_handoff`，并重定向至 `<PATH_PREFIX>/`。
5. Angular 发起 PATCH；Manager 原子领取结果、清除两个 Cookie并返回登录 JSON。再次 PATCH 返回
   `401`。

SAML 从 Controller 验证成功开始执行相同的第 4、5 步。随机 ID 使用 `crypto/rand`，不会出现在
响应正文或日志中。

## 4. 多副本与配置

当前 Store 为进程内实现，不承诺无粘性跨副本交接。生产负载均衡器必须按浏览器 Cookie 对整个
SSO 链路启用粘性会话，粘性时长至少覆盖 `MANAGER_SSO_STATE_TTL`。请求落到其他副本时返回
`400/401`，不会扫描或共享其他用户状态。后续如取消粘性，须以支持 TTL、容量限制和原子
get-and-delete 的共享 Store 替换，并重新执行并发与故障测试。

生产必需配置：

```bash
MANAGER_SSL=on
MANAGER_PUBLIC_URL=https://manager.example:8443
MANAGER_SSO_STATE_TTL=5m
MANAGER_SSO_MAX_PENDING=1024
```

IdP 注册的 SAML/OIDC callback 必须与 `MANAGER_PUBLIC_URL + PATH_PREFIX` 精确一致。密钥、client
secret 和 IdP metadata 仍由 Controller 管理，不进入 Manager 配置。

## 5. 验证与残余风险

Go 自动化测试覆盖随机 Cookie 属性、一次性领取、无/错误 Cookie、两个浏览器并发隔离、TTL
过期、OIDC state 错配与重放、跨实例拒绝、恶意 Host、固定 public URL 和 prefix 回调。验证命令：

```bash
cd admin-go
go test ./...
go test -race ./...
go vet ./...
```

发布前仍需在获批的真实 SAML 与 OIDC IdP 环境执行成功、用户拒绝、IdP 超时、重复 callback、
并发用户、SAML SLO/OIDC logout 和负载均衡器重调度测试，并由 Security Owner 签署结果。浏览器
恶意扩展或已发生的同源 XSS 不在 Cookie 关联机制可独立消除的范围内，仍依赖 CSP、依赖治理与
前端安全测试。
