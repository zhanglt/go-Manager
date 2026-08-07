# 上游同步与定制分支协作流程

本文说明 NeuVector 官方仓库更新后，如何将 `upstream` 的变化安全同步到
`go-manager` 定制仓库，并继续维护 Go Manager 迁移分支。

## 1. 远程和分支约定

本仓库使用以下远程：

| 远程 | 地址 | 用途 |
| --- | --- | --- |
| `upstream` | `https://github.com/neuvector/manager.git` | NeuVector 官方仓库，只用于获取上游更新 |
| `go-manager` | `https://github.com/zhanglt/go-Manager.git` | 定制仓库，用于共享和评审定制开发 |

当前 `origin` 也指向官方仓库，与 `upstream` 重复。执行同步和推送时必须显式写出远程名，
避免把定制分支误推到官方仓库。

推荐的长期分支关系如下：

```text
upstream/main（NeuVector 官方）
          |
          | 定期 merge
          v
go-manager/main（定制集成分支）
          |
          +-- rw-007-ci-gates
          +-- rw-010-rw-011-release-gates
          +-- feature/*
```

`go-manager/main` 和已推送的定制分支属于共享分支。同步上游时默认使用 `merge`，不要通过
`rebase` 改写共享历史。

## 2. 同步前检查

先检查当前分支、跟踪关系和工作区：

```bash
git status --short --branch
git branch -vv
git remote -v
```

同步前工作区应保持干净。需要保留的修改应先提交到独立分支：

```bash
git switch -c wip/save-before-upstream-sync
git add path/to/changed-file
git commit -m "wip: save changes before upstream sync"
```

也可以临时保存修改，包括未跟踪文件：

```bash
git stash push -u -m "before upstream sync"
```

同步完成后使用 `git stash pop` 恢复。不要把本地编译生成的 Manager 二进制误提交到仓库；
是否删除未跟踪文件必须先由文件所有者确认。

## 3. 获取并评估上游更新

获取官方仓库和定制仓库的最新引用：

```bash
git fetch upstream --prune
git fetch go-manager --prune
```

检查上游和定制主线的分叉关系：

```bash
git log --graph --oneline --decorate --boundary \
  upstream/main go-manager/main

git rev-list --left-right --count \
  upstream/main...go-manager/main
```

`git rev-list` 的第一个数字是 `upstream/main` 独有的提交数，第二个数字是
`go-manager/main` 独有的提交数。进一步查看上游新增内容：

```bash
git log --oneline go-manager/main..upstream/main
git diff --stat go-manager/main...upstream/main
```

如果 `go-manager/main` 尚未包含最新的定制开发，应先确认团队当前使用的定制集成分支，
不能仅凭分支名称假定 `go-manager/main` 是最完整的基线。

## 4. 创建同步分支并合并上游

从团队确认的定制集成分支创建一次性同步分支。分支名中的日期使用实际同步日期：

```bash
SYNC_BRANCH=sync/upstream-20260808
git switch -c "$SYNC_BRANCH" go-manager/main
git merge --no-ff upstream/main
```

如果当前最完整的定制基线是其他共享分支，例如发布门禁分支，则从该分支创建：

```bash
SYNC_BRANCH=sync/upstream-20260808
git switch -c "$SYNC_BRANCH" \
  go-manager/rw-010-rw-011-release-gates
git merge --no-ff upstream/main
```

即使没有文本冲突，也必须完成第 6 节的行为验证。上游可能修改 Scala API、Angular 数据模型
或构建依赖，这些变化可能要求 Go 实现和迁移基线同步调整。

## 5. 处理冲突和兼容性变化

查看未解决的冲突：

```bash
git status
git diff --name-only --diff-filter=U
```

逐个解决后暂存并完成合并：

```bash
git add path/to/resolved-file
git commit
```

如果合并方向错误或当前无法安全解决，可以在合并提交生成前取消：

```bash
git merge --abort
```

重点检查以下区域：

- `Makefile`、`package/Dockerfile` 和 `package/entrypoint.sh`：保留 Go Manager 构建、非 root
  运行、普通/FIPS 镜像和验证目标，同时吸收上游安全及依赖更新。
- `.github/workflows/`：保留 Go、Python、契约、镜像 SBOM/provenance 门禁，并评估上游新增的
  required check。
- `admin/webapp/`：保留上游 UI 修复，同时检查 Go 响应速度引发的 Grid Ready 竞态是否重新出现。
- Scala API、模型和 Controller 调用：上游新增或改变路由时，同步更新 `admin-go` 路由、响应
  转换、契约 Manifest 和覆盖基线。
- `admin-go/assets/web/root/`：这是 Angular 生成资源，不应逐个手工解决内容；应先解决源文件，
  再通过 `make manager` 重新构建和同步资源。

处理 API 变化时至少回答以下问题：

1. 上游是否新增、删除或修改 Scala HTTP 路由？
2. 请求字段、响应字段、状态码、Header、Cookie 或 gzip 行为是否改变？
3. Go Manager 是否需要增加兼容逻辑或测试？
4. Angular 是否依赖新的字段名、空值语义或请求时序？
5. 契约 Manifest 和路由覆盖基线是否需要重新生成？

## 6. 分层验证

### 6.1 快速检查

```bash
git diff --check
test -z "$(gofmt -l admin-go)"

cd admin-go
go test ./...
go vet ./...
cd ..
```

### 6.2 并发和迁移工具测试

```bash
cd admin-go
go test -race ./...
cd ..

PYTHONDONTWRITEBYTECODE=1 \
  python3 -m unittest discover -s tools/migration -p 'test_*.py' -v
```

### 6.3 路由清单和严格契约覆盖

将生成结果写入临时目录，避免直接覆盖已批准基线：

```bash
SYNC_TMP_DIR=$(mktemp -d)

python3 tools/migration/inventory_admin_routes.py \
  --json "$SYNC_TMP_DIR/routes.json" \
  --markdown "$SYNC_TMP_DIR/routes.md"

python3 tools/migration/contract_coverage.py \
  --routes "$SYNC_TMP_DIR/routes.json" \
  --manifest docs/admin-go-migration/baseline/contract-manifest.m2-poc.json \
  --output "$SYNC_TMP_DIR/contract-coverage.json" \
  --strict

cmp docs/admin-go-migration/baseline/routes.json "$SYNC_TMP_DIR/routes.json"
cmp docs/admin-go-migration/baseline/routes.md "$SYNC_TMP_DIR/routes.md"
cmp docs/admin-go-migration/baseline/contract-coverage.json \
  "$SYNC_TMP_DIR/contract-coverage.json"
```

如果 `cmp` 失败，先评审路由变化，补齐 Go 实现和契约场景，再更新并提交基线。不要为了让 CI
通过而直接接受未解释的生成差异。

### 6.4 Angular 和嵌入资源

```bash
cd admin/webapp
cp package-lock.manager.json package-lock.json
npm ci --legacy-peer-deps
npm run lint:check
npm run format:check
npm run build
cd ../..

make manager
```

`make manager` 会重新构建 Angular，并用 `rsync --delete` 更新嵌入 Go binary 的静态资源。
执行前应确保生成资源中的已有定制已由 Angular 源文件表达。

### 6.5 Scala 和镜像验证

上游包含 Scala 代码、依赖或 API 行为变化时执行：

```bash
sbt compile
sbt test
```

涉及构建、运行时、依赖或静态资源时继续执行：

```bash
make build-image VERSION=upstream-sync TAG=upstream-sync
make verify-image TAG=upstream-sync

make build-fips-image VERSION=upstream-sync TAG=upstream-sync
make verify-fips-image TAG=upstream-sync
```

API 行为发生变化时，还应按照
`docs/admin-go-migration/baseline/README.md` 执行 Scala/Go 完整契约测试。缩时 smoke 不能替代
真实 Controller、正式性能、稳定性、安全或 FIPS 发布证据。

## 7. 推送同步分支并发起评审

所有适用验证通过后，将同步分支推到定制仓库：

```bash
SYNC_BRANCH=sync/upstream-20260808
git push -u go-manager "$SYNC_BRANCH"
```

在 `go-manager` 仓库创建 PR，将同步分支合入团队确认的定制集成分支。PR 至少记录：

- 同步的 `upstream/main` 完整提交 SHA 和提交范围。
- 使用的定制基线分支及其完整提交 SHA。
- 冲突文件、解决方式和保留的定制行为。
- 新增、删除或改变的 API 路由和契约场景。
- Go、race、vet、Python、Angular、Scala 和镜像验证结果。
- 仍存在的差异、风险、负责人和后续任务。

不要绕过 PR 直接更新共享集成分支。同步 PR 合入后，其他定制分支应从新的集成分支继续更新。

## 8. 更新其他共享定制分支

同步 PR 合入 `go-manager/main` 后，逐个更新仍在维护的共享分支：

```bash
git fetch go-manager --prune
git switch rw-010-rw-011-release-gates
git pull --ff-only go-manager rw-010-rw-011-release-gates
git merge --no-ff go-manager/main
```

解决冲突并完成与第 6 节相匹配的验证，然后推送：

```bash
git push go-manager rw-010-rw-011-release-gates
```

其他共享分支使用相同流程。`git pull --ff-only` 用于确保本地分支不会在拉取阶段意外产生合并；
真正的集成通过后续显式 `git merge --no-ff go-manager/main` 完成。

只有尚未共享的个人功能分支才可以选择 rebase：

```bash
FEATURE_BRANCH=feature/example
git switch "$FEATURE_BRANCH"
git rebase go-manager/main
git push --force-with-lease go-manager "$FEATURE_BRANCH"
```

禁止对 `go-manager/main`、`release/*` 或已有协作者基于其开发的分支执行强制推送。

## 9. 同步失败和回退原则

- 合并尚未提交：使用 `git merge --abort` 返回合并前状态。
- 已推送同步分支但尚未合入：修复后继续推送同步分支，或关闭 PR；不要影响共享主线。
- 同步 PR 已合入共享分支：通过新的 revert PR 撤销对应 merge commit，不要重写共享历史。
- 已构建但未发布的镜像：使用独立测试 tag，禁止覆盖已批准的不可变发布 digest。
- 已进入发布或蓝绿切换：遵循 RW-010/RW-011 证据和回滚流程，保留已验证的 Scala 镜像及配置。

## 10. 推荐同步频率

- 每周至少检查一次 `upstream/main`。
- 上游发布分支、依赖安全更新或 API 兼容修复出现时立即评估。
- 发布候选冻结前必须记录并审核尚未同步的上游提交。
- 每次同步只处理一个明确的上游提交范围，避免同时混入无关功能开发。

同步完成的标准不是“Git 合并成功”，而是上游行为已经被 Go Manager、Angular、契约基线、
构建镜像和发布门禁共同吸收，并经过定制仓库 PR 审核。
