#!/bin/bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
stage_input=${STAGE_DIR:-stage}
if [[ $stage_input == /* ]]; then
  stage_dir=$stage_input
else
  stage_dir=$repo_root/$stage_input
fi
version=${VERSION:-interim/master.xxxx}
commit=${COMMIT:-unknown}
build_date=${BUILD_DATE:-unknown}

case $stage_dir in
  ""|/|"$repo_root")
    echo "refusing unsafe STAGE_DIR: $stage_dir" >&2
    exit 1
    ;;
esac

cd "$repo_root/admin/webapp"
cp package-lock.manager.json package-lock.json
npm ci --legacy-peer-deps
npm run build

rsync --archive --delete "$repo_root/admin/webapp/root/" "$repo_root/admin-go/assets/web/root/"

rm -rf -- "$stage_dir"
mkdir -p "$stage_dir/usr/local/bin" "$stage_dir/usr/share/neuvector" "$stage_dir/licenses"
cd "$repo_root/admin-go"
CGO_ENABLED=0 go build -trimpath -buildvcs=false \
  -ldflags "-s -w \
    -X github.com/neuvector/manager/admin-go/internal/buildinfo.Version=$version \
    -X github.com/neuvector/manager/admin-go/internal/buildinfo.Commit=$commit \
    -X github.com/neuvector/manager/admin-go/internal/buildinfo.BuildDate=$build_date" \
  -o "$stage_dir/usr/local/bin/manager" ./cmd/manager

install -m 0755 "$repo_root/cli/cli" "$stage_dir/usr/local/bin/cli"
install -m 0644 "$repo_root/cli/cli.py" "$stage_dir/usr/local/bin/cli.py"
cp -R "$repo_root/cli/prog" "$stage_dir/usr/local/bin/prog"
install -m 0755 "$repo_root/scripts/support" "$stage_dir/usr/local/bin/support"
install -m 0644 "$repo_root/scripts/support.py" "$stage_dir/usr/local/bin/support.py"
install -m 0644 "$repo_root/admin/src/main/resources/IP2LOCATION-LITE-DB1.CSV" "$stage_dir/usr/share/neuvector/"
install -m 0644 "$repo_root/admin/src/main/resources/IP2LOCATION-LITE-DB1.IPV6.CSV" "$stage_dir/usr/share/neuvector/"
install -m 0644 "$repo_root/admin/src/main/resources/CIS_NIST-MASTER.CSV" "$stage_dir/usr/share/neuvector/"
cp "$repo_root"/licenses/* "$stage_dir/licenses/"
