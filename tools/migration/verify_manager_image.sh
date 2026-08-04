#!/bin/bash
set -euo pipefail

fips=false
if [[ ${1:-} == "--fips" ]]; then
  fips=true
  shift
fi
image=${1:?usage: verify_manager_image.sh [--fips] IMAGE}

user=$(docker image inspect --format '{{.Config.User}}' "$image")
entrypoint=$(docker image inspect --format '{{json .Config.Entrypoint}}' "$image")
if [[ $user != "1000:1000" || $entrypoint != '["/entrypoint.sh"]' ]]; then
  echo "unexpected runtime identity: user=$user entrypoint=$entrypoint" >&2
  exit 1
fi

version=$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.version"}}' "$image")
source=$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.source"}}' "$image")
role=$(docker image inspect --format '{{index .Config.Labels "neuvector.role"}}' "$image")
if [[ -z $version || $source != "https://github.com/neuvector/manager" || $role != "manager" ]]; then
  echo "missing or invalid image labels: version=$version source=$source role=$role" >&2
  exit 1
fi

docker run --rm --cap-drop ALL --entrypoint /bin/bash "$image" -c '
  set -euo pipefail
  test "$(id -u)" = 1000
  test -x /usr/local/bin/manager
  test -x /usr/local/bin/cli
  test -x /usr/local/bin/support
  test -r /usr/share/neuvector/IP2LOCATION-LITE-DB1.CSV
  test -r /usr/share/neuvector/IP2LOCATION-LITE-DB1.IPV6.CSV
  test -r /usr/share/neuvector/CIS_NIST-MASTER.CSV
  test -r /licenses/server-licenses.md
  test -r /etc/ssl/ca-bundle.pem
  python3 -c "import click, requests, prettytable"
  ! command -v java >/dev/null 2>&1
  test ! -e /usr/lib64/jvm
  test ! -e /root/.sbt
  test ! -e /usr/local/bin/admin-assembly-1.0.jar
'

# Inspect all image-owned paths as root; the application itself is tested above as UID 1000.
docker run --rm --cap-drop ALL --user 0:0 --entrypoint /bin/bash "$image" -c '
  set -euo pipefail
  test -z "$(find / -xdev -type f \( -name "*.jar" -o -name "*.class" \) -print -quit)"
'

if $fips; then
  fips_mode=$(docker image inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$image" | sed -n 's/^GODEBUG=//p')
  fips_label=$(docker image inspect --format '{{index .Config.Labels "neuvector.fips-mode"}}' "$image")
  if [[ $fips_mode != "fips140=only" || $fips_label != "required" ]]; then
    echo "FIPS target does not enforce fips140=only" >&2
    exit 1
  fi
fi

container_id=$(docker run --detach --read-only --cap-drop ALL --tmpfs /tmp:rw,nosuid,nodev,size=64m \
  --env MANAGER_SSL=off --env MANAGER_SERVER_PORT=8443 \
  --publish 127.0.0.1::8443 "$image")
cleanup() {
  docker rm --force "$container_id" >/dev/null 2>&1 || true
}
trap cleanup EXIT

host_port=$(docker port "$container_id" 8443/tcp | sed -n 's/.*://p')
for _ in $(seq 1 50); do
  status=$(curl --silent --output /dev/null --write-out '%{http_code}' "http://127.0.0.1:$host_port/" || true)
  if [[ $status == "301" ]]; then
    break
  fi
  sleep 0.1
done
if [[ ${status:-} != "301" ]]; then
  docker logs "$container_id" >&2
  echo "manager image smoke failed with HTTP status ${status:-unavailable}" >&2
  exit 1
fi

architecture=$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$image")
size=$(docker image inspect --format '{{.Size}}' "$image")
echo "verified $image ($architecture, $size bytes): non-root, read-only rootfs, labels, required assets, no JVM/JAR/class, HTTP smoke passed"
