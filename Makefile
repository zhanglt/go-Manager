.PHONY: test ui-build sync-web manager package buildx-machine test-image test-images \
	build-image build-fips-image push-image push-fips-image verify-image verify-fips-image

RUNNER ?= docker
IMAGE_BUILDER := $(RUNNER) buildx
MACHINE ?= neuvector
DEFAULT_PLATFORMS := linux/amd64,linux/arm64
TARGET_PLATFORMS ?= linux/amd64,linux/arm64
BUILDX_ARGS ?= --sbom=true --attest type=provenance,mode=max
BUILD_ACTION ?= --load
IMAGE_TARGET ?= final
IMAGE_ARGS ?=
GOPROXY ?= https://proxy.golang.org,direct
PIP_INDEX_URL ?= https://pypi.org/simple

COMMIT := $(shell git rev-parse --short HEAD)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
ifeq ($(VERSION),)
	CHANGES := $(shell git status --porcelain --untracked-files=no)
	ifneq ($(CHANGES),)
		DIRTY := -dirty
	endif
	VERSION := $(COMMIT)$(DIRTY)
	GIT_TAG := $(shell git tag -l --contains HEAD | head -n 1)
	ifneq ($(GIT_TAG),)
		ifeq ($(DIRTY),)
			VERSION := $(GIT_TAG)
		endif
	endif
endif

ifeq ($(TAG),)
	TAG := $(VERSION)
	ifneq ($(DIRTY),)
		TAG := dev
	endif
endif

REPO ?= neuvector
IMAGE_PREFIX ?=
IMAGE ?= $(REPO)/$(IMAGE_PREFIX)manager:$(TAG)
FIPS_IMAGE ?= $(REPO)/$(IMAGE_PREFIX)manager-fips:$(TAG)
MANAGER_BINARY ?= $(CURDIR)/bin/manager

test:
	cd admin-go && go test ./...

ui-build:
	cp admin/webapp/package-lock.manager.json admin/webapp/package-lock.json
	cd admin/webapp && npm ci --legacy-peer-deps && npm run build

sync-web: ui-build
	rsync --archive --delete admin/webapp/root/ admin-go/assets/web/root/

manager: sync-web
	mkdir -p "$(dir $(MANAGER_BINARY))"
	cd admin-go && CGO_ENABLED=0 go build -trimpath -buildvcs=false \
		-ldflags "-s -w -X github.com/neuvector/manager/admin-go/internal/buildinfo.Version=$(VERSION) \
		-X github.com/neuvector/manager/admin-go/internal/buildinfo.Commit=$(COMMIT) \
		-X github.com/neuvector/manager/admin-go/internal/buildinfo.BuildDate=$(BUILD_DATE)" \
		-o "$(MANAGER_BINARY)" ./cmd/manager

package:
	VERSION="$(VERSION)" COMMIT="$(COMMIT)" BUILD_DATE="$(BUILD_DATE)" package/build_manager.sh

buildx-machine:
	$(IMAGE_BUILDER) inspect $(MACHINE) >/dev/null 2>&1 || \
		$(IMAGE_BUILDER) create --name=$(MACHINE) --platform=$(DEFAULT_PLATFORMS)
	$(IMAGE_BUILDER) inspect --bootstrap $(MACHINE) >/dev/null

test-image: buildx-machine
	$(MAKE) build-image BUILD_ACTION="--platform=$(TARGET_PLATFORMS) --output=type=cacheonly"

test-images: test-image
	$(MAKE) build-image IMAGE_TARGET=runtime-fips IMAGE=$(FIPS_IMAGE) \
		BUILD_ACTION="--platform=$(TARGET_PLATFORMS) --output=type=cacheonly"

build-image: buildx-machine
	$(IMAGE_BUILDER) build -f package/Dockerfile --builder $(MACHINE) \
		--target $(IMAGE_TARGET) $(IMAGE_ARGS) \
		--build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) --build-arg GOPROXY=$(GOPROXY) \
		--build-arg PIP_INDEX_URL=$(PIP_INDEX_URL) \
		-t "$(IMAGE)" $(BUILD_ACTION) .
	@echo "Built $(IMAGE) target $(IMAGE_TARGET)"

build-fips-image:
	$(MAKE) build-image IMAGE_TARGET=runtime-fips IMAGE=$(FIPS_IMAGE)

push-image: buildx-machine
	$(IMAGE_BUILDER) build -f package/Dockerfile --builder $(MACHINE) \
		--target final $(IMAGE_ARGS) $(BUILDX_ARGS) \
		--build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) --build-arg GOPROXY=$(GOPROXY) \
		--build-arg PIP_INDEX_URL=$(PIP_INDEX_URL) \
		--platform=$(TARGET_PLATFORMS) \
		-t "$(IMAGE)" --push .
	@echo "Pushed $(IMAGE)"

push-fips-image: buildx-machine
	$(IMAGE_BUILDER) build -f package/Dockerfile --builder $(MACHINE) \
		--target runtime-fips $(IMAGE_ARGS) $(BUILDX_ARGS) \
		--build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) --build-arg GOPROXY=$(GOPROXY) \
		--build-arg PIP_INDEX_URL=$(PIP_INDEX_URL) \
		--platform=$(TARGET_PLATFORMS) \
		-t "$(FIPS_IMAGE)" --push .
	@echo "Pushed $(FIPS_IMAGE)"

verify-image:
	tools/migration/verify_manager_image.sh "$(IMAGE)"

verify-fips-image:
	tools/migration/verify_manager_image.sh --fips "$(FIPS_IMAGE)"
