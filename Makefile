# Image URL to use all building/pushing image targets
IMG ?= controller:latest
CURL_RETRY := --retry 3 --retry-delay 5 --retry-connrefused
# YEAR defines the year value used for substituting the YEAR placeholder in the boilerplate header.
YEAR ?= $(shell date +%Y)

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= docker

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests

manifests: controller-gen kustomize generate kubebuilder ## Generate CRDs, RBAC, the installer bundle, and Helm chart.
	"$(CONTROLLER_GEN)" crd:crdVersions=v1 paths=./api/... output:crd:artifacts:config=config/crd/bases
	"$(CONTROLLER_GEN)" rbac:roleName=manager-role paths=./controllers/... output:rbac:artifacts:config=config/rbac
	mkdir -p dist
	RELEASE_DIR=dist CONTROLLER_IMG=controller:latest KUSTOMIZE="$(KUSTOMIZE)" bash hack/release-manifests.sh
	cp dist/infrastructure-components.yaml dist/install.yaml
	"$(KUBEBUILDER)" edit --plugins=helm/v2-alpha --manifests=./dist/install.yaml --output-dir=.
	bash hack/helm-chart-fixups.sh

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	"$(CONTROLLER_GEN)" object paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

TEST_PROCS ?= 5

.PHONY: test
test: manifests generate fmt vet setup-envtest ginkgo ## Run tests.
	KUBEBUILDER_ASSETS="$(shell "$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" "$(GINKGO)" --procs=$(TEST_PROCS) --cover --coverprofile=cover.out --skip-package=e2e,hack ./...

.PHONY: test-update
test-update: manifests generate fmt vet setup-envtest ginkgo ## Run tests and update expected fixture goldens.
	TESTUTIL_UPDATE_EXPECTED=true KUBEBUILDER_ASSETS="$(shell "$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" "$(GINKGO)" --procs=$(TEST_PROCS) --cover --coverprofile=cover.out --skip-package=e2e,hack ./...

# TODO(user): To use a different vendor for e2e tests, modify the setup under 'tests/e2e'.
# The default setup assumes Kind is pre-installed and builds/loads the Manager Docker image locally.
# kubectl kuberc is disabled by default for test isolation; enable with:
# - KUBECTL_KUBERC=true
# CertManager is installed by default; skip with:
# - CERT_MANAGER_INSTALL_SKIP=true
KIND_CLUSTER ?= cluster-api-provider-nico-test-e2e

.PHONY: setup-test-e2e
setup-test-e2e: ## Set up a Kind cluster for e2e tests if it does not exist
	@command -v $(KIND) >/dev/null 2>&1 || { \
		echo "Kind is not installed. Please install Kind manually."; \
		exit 1; \
	}
	@case "$$($(KIND) get clusters)" in \
		*"$(KIND_CLUSTER)"*) \
			echo "Kind cluster '$(KIND_CLUSTER)' already exists. Skipping creation." ;; \
		*) \
			echo "Creating Kind cluster '$(KIND_CLUSTER)'..."; \
			$(KIND) create cluster --name $(KIND_CLUSTER) ;; \
	esac

.PHONY: test-e2e
test-e2e: setup-test-e2e manifests generate fmt vet ## Run the e2e tests. Expected an isolated environment using Kind.
	KIND=$(KIND) KIND_CLUSTER=$(KIND_CLUSTER) go test -tags=e2e ./test/e2e/ -v -ginkgo.v
	$(MAKE) cleanup-test-e2e

.PHONY: cleanup-test-e2e
cleanup-test-e2e: ## Tear down the Kind cluster used for e2e tests
	@$(KIND) delete cluster --name $(KIND_CLUSTER)

# Every source file carries an SPDX header; the check runs in
# .github/workflows/license.yml.
# config/ and chart/ hold generated output that `make manifests` and
# Kubebuilder rewrite, so their templates do not carry source-file headers.
#
# addlicense only ever ADDS a header to a file that has none. It cannot detect a
# wrong header, a stale year or a foreign copyright holder, and it skips any
# file whose first bytes already mention a copyright. It also understands a
# fixed set of file extensions, so .tpl, .lua, .json, .toml and extensionless
# files such as Tiltfile are outside its reach.
ADDLICENSE := go run github.com/google/addlicense@v1.1.1
ADDLICENSE_IGNORES := \
	-ignore '**/*.pb.go' \
	-ignore '**/testdata/**' \
	-ignore 'bin/**' \
	-ignore 'chart/**' \
	-ignore 'config/**' \
	-ignore 'vendor/**'

# A file that must always be inspected. If a typo in ADDLICENSE_IGNORES ever
# excludes first-party source, the check would pass having inspected nothing --
# so assert this one file is not among the skipped before trusting the result.
LICENSE_CANARY := cmd/main.go

.PHONY: license
license: ## Add a license header to any source file that lacks one.
	$(ADDLICENSE) -f ./hack/boilerplate.addlicense.txt $(ADDLICENSE_IGNORES) .

# Expects a clean working directory.
.PHONY: file-license-check
file-license-check: license ## Verify every source file carries a license header.
	@if $(ADDLICENSE) -v -f ./hack/boilerplate.addlicense.txt $(ADDLICENSE_IGNORES) . 2>&1 \
		| grep -q "skipping: $(LICENSE_CANARY)"; then \
		echo "ADDLICENSE_IGNORES excludes $(LICENSE_CANARY). The check would report success without inspecting first-party source."; \
		exit 1; \
	fi
	@untracked=$$(git ls-files --others --exclude-standard); \
	if [ -n "$$untracked" ]; then \
		echo "Untracked files present; git diff cannot see them. Commit or remove:"; \
		echo "$$untracked"; \
		exit 1; \
	fi
	@if ! git diff --exit-code -- . > /dev/null 2>&1; then \
		echo "License headers are missing. Run 'make license' locally and commit the result."; \
		echo "Files without a license header:"; \
		git diff --name-only -- .; \
		exit 1; \
	fi

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter
	"$(GOLANGCI_LINT)" run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	"$(GOLANGCI_LINT)" run --fix

.PHONY: lint-config
lint-config: golangci-lint ## Verify golangci-lint linter configuration
	"$(GOLANGCI_LINT)" config verify

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -o bin/manager ./cmd

.PHONY: binary
binary: ## Compile every package and the manager binary. This is what CI runs.
	@# Deliberately no `manifests generate fmt vet` prerequisites. Those rewrite
	@# generated files and need controller-gen, which makes them a poor fit for a
	@# check that should answer one question: does this compile? Use `make build`
	@# when you want the generated files refreshed as well.
	@#
	@# Both lines earn their place. `./...` catches a package that no binary
	@# imports; the second catches a link failure in the thing that ships.
	go build ./...
	go build -o bin/manager ./cmd

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd

.PHONY: run-fake
run-fake: ## Run the self-contained fake NICo endpoint on :8090.
	go run ./cmd/fake

# If you wish to build the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	$(CONTAINER_TOOL) build -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	$(CONTAINER_TOOL) push ${IMG}

##@ Local development

# These settings are scoped so another Cluster API development environment can
# run alongside CAPNICo without inheriting or colliding with them.
CAPNICO_TILT_PORT ?= 10352
CAPNICO_KUBECONFIG ?= $(HOME)/.kube/capnico.kubeconfig
CAPNICO_CLUSTER_NAME ?= capnico
CAPNICO_TILT_ENV = KUBECONFIG="$(CAPNICO_KUBECONFIG)"

.PHONY: capi-init
capi-init: clusterctl ## Install Cluster API core and the kubeadm providers into the local cluster.
	@set -e; \
	if KUBECONFIG="$(CAPNICO_KUBECONFIG)" kubectl get deployment -n capi-system capi-controller-manager >/dev/null 2>&1; then \
		echo "  Cluster API $(CAPI_VERSION) already installed"; \
	else \
		KUBECONFIG="$(CAPNICO_KUBECONFIG)" "$(CLUSTERCTL)" init \
			--core cluster-api:$(CAPI_VERSION) \
			--bootstrap kubeadm:$(CAPI_VERSION) \
			--control-plane kubeadm:$(CAPI_VERSION); \
	fi

.PHONY: tilt-up
tilt-up: manifests kustomize ## Create the local cluster, install Cluster API, and start Tilt.
	KUBECONFIG="$(CAPNICO_KUBECONFIG)" ctlptl apply -f ctlptl.yaml
	$(MAKE) capi-init
	@echo
	@echo "  Local cluster ready. In another terminal, run:"
	@echo "    export KUBECONFIG=$(CAPNICO_KUBECONFIG)"
	@echo
	$(CAPNICO_TILT_ENV) tilt up --port $(CAPNICO_TILT_PORT)

.PHONY: tilt-down
tilt-down: ## Stop Tilt and delete the local kind cluster.
	- $(CAPNICO_TILT_ENV) tilt down
	- pkill -f "tilt up --port $(CAPNICO_TILT_PORT)"
	KUBECONFIG="$(CAPNICO_KUBECONFIG)" ctlptl delete -f ctlptl.yaml --ignore-not-found

.PHONY: kubeconfig
kubeconfig: ## Print the export line for the local cluster's kubeconfig.
	@echo "export KUBECONFIG=$(CAPNICO_KUBECONFIG)"

# PLATFORMS defines the target platforms for the manager image be built to provide support to multiple
# architectures. (i.e. make docker-buildx IMG=myregistry/mypoperator:0.0.1). To use this option you need to:
# - be able to use docker buildx. More info: https://docs.docker.com/build/buildx/
# - have enabled BuildKit. More info: https://docs.docker.com/develop/develop-images/build_enhancements/
# - be able to push the image to your registry (i.e. if you do not set a valid value via IMG=<myregistry/image:<tag>> then the export will fail)
# To adequately provide solutions that are compatible with multiple platforms, you should consider using this option.
PLATFORMS ?= linux/arm64,linux/amd64,linux/s390x,linux/ppc64le
.PHONY: docker-buildx
docker-buildx: ## Build and push docker image for the manager for cross-platform support
	# copy existing Dockerfile and insert --platform=${BUILDPLATFORM} into Dockerfile.cross, and preserve the original Dockerfile
	sed -e '1 s/\(^FROM\)/FROM --platform=\$$\{BUILDPLATFORM\}/; t' -e ' 1,// s//FROM --platform=\$$\{BUILDPLATFORM\}/' Dockerfile > Dockerfile.cross
	- $(CONTAINER_TOOL) buildx create --name cluster-api-provider-nico-builder
	$(CONTAINER_TOOL) buildx use cluster-api-provider-nico-builder
	- $(CONTAINER_TOOL) buildx build --push --platform=$(PLATFORMS) --tag ${IMG} -f Dockerfile.cross .
	- $(CONTAINER_TOOL) buildx rm cluster-api-provider-nico-builder
	rm Dockerfile.cross

.PHONY: build-installer
build-installer: manifests generate kustomize ## Generate a consolidated YAML with CRDs and deployment.
	mkdir -p dist
	cd config/manager && "$(KUSTOMIZE)" edit set image controller=${IMG}
	"$(KUSTOMIZE)" build config/default > dist/install.yaml

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" apply -f -; else echo "No CRDs to install; skipping."; fi

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -; else echo "No CRDs to delete; skipping."; fi

.PHONY: deploy
deploy: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && "$(KUSTOMIZE)" edit set image controller=${IMG}
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" apply -f -

.PHONY: undeploy
undeploy: kustomize ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -

##@ Release

RELEASE_TAG ?= $(shell git describe --tags --abbrev=0 2>/dev/null || echo v0.0.0)
RELEASE_DIR ?= out
CONTROLLER_IMG ?= $(IMG)
RELEASE_REMOTE ?= origin

.PHONY: release-manifests
release-manifests: manifests kustomize ## Generate release manifests.
	RELEASE_DIR=$(RELEASE_DIR) CONTROLLER_IMG=$(CONTROLLER_IMG) KUSTOMIZE="$(KUSTOMIZE)" bash hack/release-manifests.sh

.PHONY: release-rc
release-rc: ## Tag and push an RC. Usage: make release-rc VERSION=v0.1.0-rc.1
	@version="$(VERSION)"; \
	[[ "$$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+-rc\.[1-9][0-9]*$$ ]] || { echo "VERSION must look like v0.1.0-rc.1" >&2; exit 1; }; \
	git tag -s "$$version" -m "Release candidate $$version"; \
	git push "$(RELEASE_REMOTE)" "$$version"

.PHONY: promote-rc
promote-rc: ## Promote an RC to a final release. Usage: make promote-rc VERSION=v0.1.0-rc.1
	@rc="$(VERSION)"; \
	[[ "$$rc" =~ ^v[0-9]+\.[0-9]+\.[0-9]+-rc\.[1-9][0-9]*$$ ]] || { echo "VERSION must look like v0.1.0-rc.1" >&2; exit 1; }; \
	release="$${rc%-rc.*}"; \
	commit="$$(git rev-parse "$$rc^{commit}")"; \
	git tag -s "$$release" "$$commit" -m "Release $$release"; \
	git push "$(RELEASE_REMOTE)" "$$release"

.PHONY: changelog
changelog: ## Preview release notes from changes since the latest stable tag.
	@tools/changelog

##@ Helm Deployment

## Helm binary to use for deploying the chart
HELM ?= helm
## Namespace to deploy the Helm release
HELM_NAMESPACE ?= capnico-system
## Name of the Helm release
HELM_RELEASE ?= capi-provider-nico
## Path to the Helm chart directory
HELM_CHART_DIR ?= ./chart
## Additional arguments to pass to helm commands
HELM_EXTRA_ARGS ?=

.PHONY: install-helm
install-helm: ## Install the latest version of Helm.
	@command -v $(HELM) >/dev/null 2>&1 || { \
		echo "Installing Helm..." && \
		curl $(CURL_RETRY) -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-4 | bash; \
	}

.PHONY: helm-deploy
helm-deploy: install-helm ## Deploy manager to the K8s cluster via Helm. Specify an image with IMG.
	$(HELM) upgrade --install $(HELM_RELEASE) $(HELM_CHART_DIR) \
		--namespace $(HELM_NAMESPACE) \
		--create-namespace \
		--set manager.image.repository=$${IMG%:*} \
		--set manager.image.tag=$${IMG##*:} \
		--wait \
		--timeout 5m \
		$(HELM_EXTRA_ARGS)

.PHONY: helm-uninstall
helm-uninstall: ## Uninstall the Helm release from the K8s cluster.
	$(HELM) uninstall $(HELM_RELEASE) --namespace $(HELM_NAMESPACE)

.PHONY: helm-status
helm-status: ## Show Helm release status.
	$(HELM) status $(HELM_RELEASE) --namespace $(HELM_NAMESPACE)

.PHONY: helm-history
helm-history: ## Show Helm release history.
	$(HELM) history $(HELM_RELEASE) --namespace $(HELM_NAMESPACE)

.PHONY: helm-rollback
helm-rollback: ## Rollback to previous Helm release.
	$(HELM) rollback $(HELM_RELEASE) --namespace $(HELM_NAMESPACE)

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p "$(LOCALBIN)"

## Tool Binaries
KUBECTL ?= kubectl
KIND ?= kind
KUSTOMIZE ?= $(LOCALBIN)/kustomize
KUBEBUILDER ?= $(LOCALBIN)/kubebuilder
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GINKGO ?= $(LOCALBIN)/ginkgo
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint
CRANE ?= $(LOCALBIN)/crane
CLUSTERCTL ?= $(LOCALBIN)/clusterctl

## Tool Versions
KUSTOMIZE_VERSION ?= v5.8.1
KUBEBUILDER_VERSION ?= v4.15.0
CONTROLLER_TOOLS_VERSION ?= v0.20.1
CRANE_VERSION ?= v0.21.9
GINKGO_VERSION ?= $(call gomodver,github.com/onsi/ginkgo/v2)
CAPI_VERSION ?= $(call gomodver,sigs.k8s.io/cluster-api)

#ENVTEST_VERSION is the version of controller-runtime release branch to fetch the envtest setup script (i.e. release-0.20)
ENVTEST_VERSION ?= $(shell v='$(call gomodver,sigs.k8s.io/controller-runtime)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_VERSION manually (controller-runtime replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?([0-9]+)\.([0-9]+).*/release-\1.\2/')

#ENVTEST_K8S_VERSION is the version of Kubernetes to use for setting up ENVTEST binaries (i.e. 1.31)
ENVTEST_K8S_VERSION ?= $(shell v='$(call gomodver,k8s.io/api)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_K8S_VERSION manually (k8s.io/api replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?[0-9]+\.([0-9]+).*/1.\1/')

GOLANGCI_LINT_VERSION ?= v2.12.2
.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: kubebuilder
kubebuilder: $(KUBEBUILDER) ## Download Kubebuilder locally if necessary.
$(KUBEBUILDER): $(LOCALBIN)
	$(call go-install-tool,$(KUBEBUILDER),sigs.k8s.io/kubebuilder/v4,$(KUBEBUILDER_VERSION))

.PHONY: crane
crane: $(CRANE) ## Download crane locally if necessary.
$(CRANE): $(LOCALBIN)
	$(call go-install-tool,$(CRANE),github.com/google/go-containerregistry/cmd/crane,$(CRANE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest ## Download the binaries required for ENVTEST in the local bin directory.
	@echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."
	@"$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path || { \
		echo "Error: Failed to set up envtest binaries for version $(ENVTEST_K8S_VERSION)."; \
		exit 1; \
	}

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: ginkgo
ginkgo: $(GINKGO) ## Download the Ginkgo CLI locally if necessary.
$(GINKGO): $(LOCALBIN)
	$(call go-install-tool,$(GINKGO),github.com/onsi/ginkgo/v2/ginkgo,$(GINKGO_VERSION))

.PHONY: clusterctl
clusterctl: $(CLUSTERCTL) ## Download clusterctl locally if necessary.
$(CLUSTERCTL): $(LOCALBIN)
	$(call go-install-tool,$(CLUSTERCTL),sigs.k8s.io/cluster-api/cmd/clusterctl,$(CAPI_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))
	@test -f .custom-gcl.yml && { \
		echo "Building custom golangci-lint with plugins..." && \
		$(GOLANGCI_LINT) custom --destination $(LOCALBIN) --name golangci-lint-custom && \
		mv -f $(LOCALBIN)/golangci-lint-custom $(GOLANGCI_LINT); \
	} || true

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f "$(1)-$(3)" ] && [ "$$(readlink -- "$(1)" 2>/dev/null)" = "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f "$(1)" ;\
GOBIN="$(LOCALBIN)" go install $${package} ;\
mv "$(LOCALBIN)/$$(basename "$(1)")" "$(1)-$(3)" ;\
} ;\
ln -sf "$$(realpath "$(1)-$(3)")" "$(1)"
endef

define gomodver
$(shell go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' $(1) 2>/dev/null)
endef

##@ Third-party notices

# go-licenses is pinned here rather than taken from PATH so CI and a laptop
# generate the same file. It installs into ./bin, not the shared GOBIN.
GO_LICENSES_VERSION ?= v2.0.1

# go-licenses embeds go/packages, and must be built with the *same* toolchain
# that `go list` actually runs inside this module. A module's `go` directive can
# make Go switch toolchains (GOTOOLCHAIN=auto), and on a mismatch go-licenses
# reports every stdlib package as having no module info and emits nothing at
# all. `go install pkg@version` deliberately ignores the current module, so the
# toolchain has to be pinned explicitly rather than inherited.
GO_TOOLCHAIN := $(shell go version | awk '{print $$3}')

bin/go-licenses:
	GOTOOLCHAIN=$(GO_TOOLCHAIN) GOBIN=$(CURDIR)/bin \
		go install github.com/google/go-licenses/v2@$(GO_LICENSES_VERSION)

CHART_LICENSE_DIR ?= chart

# The chart is a distributed artefact in the same way the container image is, so
# it carries the same attribution. The image gained these files earlier; the
# chart was missed.
#
# Copied rather than symlinked: `helm package` does not follow symlinks, and a
# consumer who unpacks the chart should find the real text. Copying means they
# can drift, so this hangs off `notices` -- the target that rewrites the file
# that drifts -- and `notices-check` compares both copies.
.PHONY: chart-licenses
chart-licenses: ## Copy the license files into the Helm chart.
	mkdir -p "$(CHART_LICENSE_DIR)"
	cp LICENSE NOTICE THIRD_PARTY_NOTICES.md "$(CHART_LICENSE_DIR)/"

.PHONY: notices
notices: bin/go-licenses ## Regenerate THIRD_PARTY_NOTICES.md.
	@bash hack/generate-notices.sh
	@# After the generator, not before: as a prerequisite this would copy the
	@# previous file and leave the chart stale the moment the root one changed.
	@$(MAKE) --no-print-directory chart-licenses

.PHONY: notices-check
notices-check: ## Verify THIRD_PARTY_NOTICES.md matches the dependency tree.
	@echo "- Checking THIRD_PARTY_NOTICES.md is up to date..."
	@# Cheap gate first, before spending minutes regenerating. git diff reports
	@# nothing for an untracked path, so an uncommitted notices file would
	@# otherwise pass silently.
	@git ls-files --error-unmatch THIRD_PARTY_NOTICES.md >/dev/null 2>&1 \
		|| { echo "ERROR: THIRD_PARTY_NOTICES.md is not tracked. Run 'make notices' and commit it."; exit 1; }
	@$(MAKE) notices
	@$(MAKE) --no-print-directory chart-licenses
	@git diff --exit-code -- THIRD_PARTY_NOTICES.md "$(CHART_LICENSE_DIR)/THIRD_PARTY_NOTICES.md" \
		|| { echo "ERROR: THIRD_PARTY_NOTICES.md is stale. Run 'make notices' and commit the change."; exit 1; }
