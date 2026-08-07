# Overview
NeuVector Security Center Admin Console for the SUSE NeuVector Container Security Platform.

A viewable version of docs can be seen at https://open-docs.neuvector.com

The images are on the NeuVector Docker Hub registry. Use the appropriate version tag for the manager, controller, enforcer, and leave the version as 'latest' for scanner and updater. For example:
+ neuvector/manager:5.0.0
+ neuvector/controller:5.0.0
+ neuvector/enforcer:5.0.0
+ neuvector/scanner:latest
+ neuvector/updater:latest

Note: Deploying from the Rancher Manager 2.6.5+ NeuVector chart pulls from the rancher-mirrored repo and deploys into the cattle-neuvector-system namespace.

## Build, Test, and Package

The root `Makefile` builds the Angular UI and Go Manager introduced by the Admin backend migration.
Local source builds require Go 1.25+, Node.js/npm, and `rsync`; image targets require Docker with
Buildx.

```bash
make test                 # Run all Go tests.
make ui-build             # Install UI dependencies and create the Angular production build.
make manager              # Build the UI and Go Manager binary into bin/manager.
make package              # Create the non-container distribution under stage/.
```

Build and verify the production images locally:

```bash
make build-image VERSION=dev TAG=dev
make verify-image TAG=dev

make build-fips-image VERSION=dev TAG=dev
make verify-fips-image TAG=dev
```

`make test-images` performs non-publishing builds for both `linux/amd64` and `linux/arm64`, including
the normal and FIPS-only targets. Cross-architecture builds require arm64 binfmt/QEMU support; the
release workflow configures it automatically. `runtime-fips` enforces `GODEBUG=fips140=only`, but the
result is not a certified FIPS artifact until the approved toolchain and release evidence are signed
off by the Security/FIPS owner.

`make verify-image` and `make verify-fips-image` run the container as UID/GID `1000:1000` with all
Linux capabilities dropped, a read-only root filesystem, and a bounded `/tmp` tmpfs. The smoke test
also verifies the support executable and the IP geolocation and CIS/NIST data files at their final
runtime paths.

The workflow for merging NeuVector upstream changes into the customized Go Manager branches is
documented in [docs/upstream-sync-workflow.md](docs/upstream-sync-workflow.md).

The `make push-image` and `make push-fips-image` targets publish multi-architecture images with SPDX
SBOM and SLSA provenance attestations. Common overrides include:

- `VERSION` and `TAG`: binary/OCI version metadata and image tag.
- `REPO` and `IMAGE_PREFIX`: destination repository and image-name prefix.
- `TARGET_PLATFORMS`: comma-separated platforms; defaults to `linux/amd64,linux/arm64`.
- `GOPROXY` and `PIP_INDEX_URL`: dependency mirrors for restricted build environments.
- `IMAGE_ARGS`: additional arguments passed to `docker buildx build`.

For example, to use regional dependency mirrors:

```bash
make build-image TAG=dev \
  GOPROXY=https://goproxy.cn,direct \
  PIP_INDEX_URL=https://pypi.tuna.tsinghua.edu.cn/simple
```

# Bugs & Issues
Please submit bugs and issues to [neuvector/neuvector](//github.com/neuvector/neuvector/issues) with a title starting with `[UI] `.

Or just [click here](//github.com/neuvector/neuvector/issues/new?title=%5BUI%5D%20) to create a new issue.

# License

Copyright © 2016-2026 [SUSE](https://www.suse.com/products/rancher/security/). All Rights Reserved

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

[http://www.apache.org/licenses/LICENSE-2.0](http://www.apache.org/licenses/LICENSE-2.0)

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
