# syntax=docker/dockerfile:1.7

ARG NODE_BUILDER_IMAGE=node:22-alpine@sha256:c610fcdfb1d5b4740dd70c284ed3cb16bb857e0f7166196e36a5501df7a3aa32
ARG GO_BUILDER_IMAGE=golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2
ARG BCI_BASE_IMAGE=registry.suse.com/bci/bci-base:15.7@sha256:f210b01e65ad0d6485cdac7236475101613be1206740e3dc97b53007aaffa1e1
ARG BCI_MICRO_IMAGE=registry.suse.com/bci/bci-micro:15.7@sha256:44f5c047210188eb290414c52f883b763efa1e234c2a15c325f03e83f0984fa6

FROM --platform=$BUILDPLATFORM ${NODE_BUILDER_IMAGE} AS web-builder
WORKDIR /src/admin/webapp
COPY admin/webapp/package.json ./
COPY admin/webapp/package-lock.manager.json ./package-lock.json
RUN --mount=type=cache,target=/root/.npm \
    npm ci --legacy-peer-deps
COPY admin/webapp/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM ${GO_BUILDER_IMAGE} AS go-builder
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=interim/master.xxxx
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
ARG GOPROXY=https://proxy.golang.org,direct
WORKDIR /src/admin-go
COPY admin-go/go.mod admin-go/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    GOPROXY="$GOPROXY" go mod download
COPY admin-go/ ./
COPY --from=web-builder /src/admin/webapp/root/ ./assets/web/root/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    go build -trimpath -buildvcs=false \
      -ldflags="-s -w \
        -X github.com/neuvector/manager/admin-go/internal/buildinfo.Version=${VERSION} \
        -X github.com/neuvector/manager/admin-go/internal/buildinfo.Commit=${COMMIT} \
        -X github.com/neuvector/manager/admin-go/internal/buildinfo.BuildDate=${BUILD_DATE}" \
      -o /out/manager ./cmd/manager

FROM ${BCI_MICRO_IMAGE} AS micro
FROM ${BCI_BASE_IMAGE} AS python-rootfs
ARG PIP_INDEX_URL=https://pypi.org/simple
COPY --from=micro / /chroot/
COPY package/requirements.txt /chroot/requirements.txt
RUN --mount=type=cache,target=/var/cache/zypp \
    --mount=type=cache,target=/chroot/root/.cache/pip \
    for attempt in 1 2 3 4 5; do \
      zypper --gpg-auto-import-keys refresh && break; \
      test "$attempt" -lt 5 || exit 1; \
      sleep $((attempt * 2)); \
    done && \
    zypper --gpg-auto-import-keys --installroot /chroot --non-interactive \
      install --no-recommends \
      bash ca-certificates python313 python313-pip iproute2 lsof procps grep awk && \
    cp /etc/resolv.conf /chroot/etc/resolv.conf && \
    chroot /chroot /usr/bin/python3.13 -m pip install \
      --retries 10 --timeout 120 --index-url "$PIP_INDEX_URL" -r /requirements.txt && \
    ln -sf /usr/bin/python3.13 /chroot/usr/bin/python3 && \
    ln -sf /usr/bin/python3.13 /chroot/usr/bin/python && \
    zypper --installroot /chroot clean --all && \
    rm -rf /chroot/requirements.txt /chroot/var/cache/zypp /chroot/var/log/*

FROM micro AS runtime
ARG VERSION=unknown
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
WORKDIR /
COPY --from=python-rootfs /chroot/ /
COPY --from=go-builder --chmod=0755 /out/manager /usr/local/bin/manager
COPY --chmod=0755 cli/cli /usr/local/bin/cli
COPY cli/cli.py /usr/local/bin/cli.py
COPY cli/prog/ /usr/local/bin/prog/
COPY --chmod=0755 scripts/support /usr/local/bin/support
COPY scripts/support.py /usr/local/bin/support.py
COPY admin/src/main/resources/IP2LOCATION-LITE-DB1.CSV /usr/share/neuvector/IP2LOCATION-LITE-DB1.CSV
COPY admin/src/main/resources/IP2LOCATION-LITE-DB1.IPV6.CSV /usr/share/neuvector/IP2LOCATION-LITE-DB1.IPV6.CSV
COPY admin/src/main/resources/CIS_NIST-MASTER.CSV /usr/share/neuvector/CIS_NIST-MASTER.CSV
COPY licenses/ /licenses/
COPY --chmod=0755 package/entrypoint.sh /entrypoint.sh

ENV LANG=C.UTF-8 \
    PYTHONUNBUFFERED=1 \
    IP_GEO_IPV4_DB=/usr/share/neuvector/IP2LOCATION-LITE-DB1.CSV \
    IP_GEO_IPV6_DB=/usr/share/neuvector/IP2LOCATION-LITE-DB1.IPV6.CSV \
    CIS_NIST_DB=/usr/share/neuvector/CIS_NIST-MASTER.CSV

LABEL org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.description="SUSE Security Manager" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.source="https://github.com/neuvector/manager" \
      org.opencontainers.image.title="SUSE Security Manager" \
      org.opencontainers.image.vendor="SUSE Security" \
      org.opencontainers.image.version="${VERSION}" \
      org.opensuse.reference="neuvector/manager:${VERSION}" \
      io.artifacthub.package.logo-url="https://avatars2.githubusercontent.com/u/19367275" \
      io.artifacthub.package.readme-url="https://raw.githubusercontent.com/neuvector/manager/${VERSION}/README.md" \
      name="manager" \
      vendor="SUSE Security" \
      neuvector.image="neuvector/manager" \
      neuvector.rev="${COMMIT}" \
      neuvector.role="manager"

RUN echo 'manager:x:1000:1000::/nonexistent:/bin/bash' >> /etc/passwd && \
    echo 'manager:x:1000:' >> /etc/group
USER 1000:1000
EXPOSE 8443
ENTRYPOINT ["/entrypoint.sh"]

# This target enforces Go's FIPS-only runtime mode. Certification still depends on the approved
# toolchain, base image, target platform, and release evidence owned by the Security/FIPS team.
FROM runtime AS runtime-fips
ENV GODEBUG=fips140=only
LABEL neuvector.fips-mode="required"

FROM runtime AS final
