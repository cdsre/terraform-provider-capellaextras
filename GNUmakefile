# Resolve GOBIN: prefer explicit $GOBIN, fall back to $GOPATH/bin.
GOBIN := $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN := $(shell go env GOPATH)/bin
endif

default: fmt lint install generate

build:
	go build -v ./...

install: build
	go install -v ./...

lint:
	golangci-lint run

generate:
	cd tools; go generate ./...

fmt:
	gofmt -s -w -e .

test:
	go test -v -cover -timeout=120s -parallel=10 ./...

testacc:
	TF_ACC=1 go test -v -cover -timeout 120m ./...

# dev-override installs the provider binary locally and writes .terraformrc.local
# with a dev_overrides block so Terraform uses the local binary instead of the registry.
#
# Usage:
#   make dev-override
#   export TF_CLI_CONFIG_FILE=$$(pwd)/.terraformrc.local
#   terraform plan   # uses the local provider binary
#
# To stop using the local override:
#   unset TF_CLI_CONFIG_FILE
dev-override: install
	@printf 'provider_installation {\n  dev_overrides {\n    "registry.terraform.io/cdsre/capellaextras" = "%s"\n  }\n\n  # Install all other providers from their origin registries as normal.\n  direct {}\n}\n' "$(GOBIN)" > .terraformrc.local
	@echo ""
	@echo "Provider binary : $(GOBIN)/terraform-provider-capellaextras"
	@echo "CLI config file : $$(pwd)/.terraformrc.local"
	@echo ""
	@echo "Activate the local override:"
	@echo "  export TF_CLI_CONFIG_FILE=$$(pwd)/.terraformrc.local"
	@echo ""
	@echo "Deactivate (revert to registry):"
	@echo "  unset TF_CLI_CONFIG_FILE"

.PHONY: fmt lint test testacc build install generate dev-override
