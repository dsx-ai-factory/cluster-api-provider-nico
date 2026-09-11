BINARY_NAME ?= capnico-layout
BUILD_DIR ?= bin
REPO_ROOT ?= $(abspath ../../../../..)
EXTERNAL_PLUGINS_PATH ?= $(REPO_ROOT)/.kubebuilder-plugins
PLUGIN_VERSION ?= v1
PLUGIN_INSTALL_DIR := $(EXTERNAL_PLUGINS_PATH)/$(BINARY_NAME)/$(PLUGIN_VERSION)
GROUP ?= infrastructure
VERSION ?= v1alpha1
RESOURCE ?= true
CONTROLLER ?= true
RUN_MAKE ?= false
DOMAIN ?= cluster.x-k8s.io
MODULE ?= github.com/dsx-ai-factory/cluster-api-provider-nico
PROJECT_NAME ?= cluster-api-provider-nico
LICENSE ?= none
FETCH_DEPS ?= false

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make <target>\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  %-18s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

.PHONY: build
build: ## Build the external Kubebuilder plugin binary.
	go build -o $(BUILD_DIR)/$(BINARY_NAME) .

.PHONY: install
install: build ## Install the plugin under EXTERNAL_PLUGINS_PATH.
	mkdir -p $(PLUGIN_INSTALL_DIR)
	cp $(BUILD_DIR)/$(BINARY_NAME) $(PLUGIN_INSTALL_DIR)/$(BINARY_NAME)

.PHONY: init-project
init-project: install ## Run kubebuilder init with the CAPNICo layout plugin.
	cd $(REPO_ROOT) && EXTERNAL_PLUGINS_PATH=$(EXTERNAL_PLUGINS_PATH) kubebuilder init --plugins go/v4,capnico-layout/v1 --domain $(DOMAIN) --repo $(MODULE) --project-name $(PROJECT_NAME) --license $(LICENSE) --fetch-deps=$(FETCH_DEPS)

.PHONY: create-api
create-api: install ## Run kubebuilder create api with the CAPNICo layout plugin. Requires KIND=<Kind>.
	@test -n "$(KIND)" || (echo "KIND is required, for example: make create-api KIND=NicoCluster"; exit 1)
	cd $(REPO_ROOT) && EXTERNAL_PLUGINS_PATH=$(EXTERNAL_PLUGINS_PATH) kubebuilder create api --group $(GROUP) --version $(VERSION) --kind $(KIND) --resource=$(RESOURCE) --controller=$(CONTROLLER) --make=$(RUN_MAKE)

.PHONY: test
test: ## Run plugin unit tests.
	go test ./...

.PHONY: clean
clean: ## Remove local plugin build outputs.
	rm -rf $(BUILD_DIR)
