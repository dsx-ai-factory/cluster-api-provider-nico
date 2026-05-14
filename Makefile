CONTROLLER_GEN_VERSION ?= v0.20.1
CONTROLLER_GEN ?= go run sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)
IMG ?= controller:latest
RELEASE_TAG ?= $(shell git describe --tags --abbrev=0 2>/dev/null || echo v0.0.0)
RELEASE_DIR ?= out
HELM_CHART_DIR ?= charts/capi-provider-nico
CONTROLLER_IMG ?= $(IMG)

.PHONY: manifests
manifests:
	$(CONTROLLER_GEN) crd:crdVersions=v1 paths=./api/... output:crd:artifacts:config=config/crd/bases
	$(CONTROLLER_GEN) rbac:roleName=manager-role paths=./controllers/... output:rbac:artifacts:config=config/rbac

.PHONY: generate
generate:
	$(CONTROLLER_GEN) object:headerFile= paths=./...

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: test
test:
	go test ./...

.PHONY: build
build:
	go build ./cmd/manager

.PHONY: run
run:
	go run ./cmd/manager

.PHONY: docker-build
docker-build:
	docker build -t $(IMG) .

.PHONY: release-manifests
release-manifests:
	RELEASE_DIR=$(RELEASE_DIR) CONTROLLER_IMG=$(CONTROLLER_IMG) bash hack/release-manifests.sh

.PHONY: helm-chart-manifests
helm-chart-manifests: release-manifests
	mkdir -p $(HELM_CHART_DIR)/files
	cp $(RELEASE_DIR)/infrastructure-components.yaml $(HELM_CHART_DIR)/files/infrastructure-components.yaml
