# Terraform Provider Capella Extras

This provider is created to add extra functionality to the Capella Terraform Provider. It allows me to create 
functionality that is not currently available in the provider. But without being constrained by the providers release
cycles. Or having to drive for alignment with the provider devs of the main couchbase capella provider.

## Requirements

- [Terraform](https://developer.hashicorp.com/terraform/downloads) >= 1.14
- [Go](https://golang.org/doc/install) >= 1.24

## Building The Provider

1. Clone the repository
1. Enter the repository directory
1. Build the provider using the Go `install` command:

```shell
go install
```

## Adding Dependencies

This provider uses [Go modules](https://github.com/golang/go/wiki/Modules).
Please see the Go documentation for the most up to date information about using Go modules.

To add a new dependency `github.com/author/dependency` to your Terraform provider:

```shell
go get github.com/author/dependency
go mod tidy
```

Then commit the changes to `go.mod` and `go.sum`.

## Using the provider

Add the provider to your `required_providers` block:

```hcl
terraform {
  required_providers {
    capellaextras = {
      source  = "cdsre/capellaextras"
    }
  }
}

provider "capellaextras" {
  # Or set CAPELLA_AUTHENTICATION_TOKEN in the environment.
  authentication_token = var.capella_token
}
```

## Developing the Provider

If you wish to work on the provider, you'll first need [Go](http://www.golang.org) installed on your machine (see [Requirements](#requirements) above).

To compile the provider, run `go install`. This will build the provider and put the provider binary in the `$GOPATH/bin` directory.

To generate or update documentation, run `make generate`.

In order to run the full suite of Acceptance tests, run `make testacc`.

*Note:* Acceptance tests create real resources, and often cost money to run.

```shell
make testacc
```

### Testing with a locally built binary

Terraform's [development overrides](https://developer.hashicorp.com/terraform/cli/config/config-file#development-overrides-for-provider-developers)
let you point Terraform at a local provider binary instead of downloading one from the registry.
No `terraform init` is needed and version constraints are ignored.

**1. Build and install the provider, then generate the override config:**

```shell
make dev-override
```

This installs the binary to `$GOBIN` (usually `$GOPATH/bin`) and writes a
`.terraformrc.local` file in the repository root:

```hcl
provider_installation {
  dev_overrides {
    "registry.terraform.io/cdsre/capellaextras" = "/home/you/go/bin"
  }

  direct {}
}
```

**2. Tell the Terraform CLI to use that config:**

```shell
export TF_CLI_CONFIG_FILE=$(pwd)/.terraformrc.local
```

You can add this export to your shell profile or a local `.envrc` (direnv) so it
applies automatically when you enter the repository directory.

**3. Run Terraform as normal:**

```shell
terraform plan
terraform apply
```

Terraform will print a warning that dev overrides are active — this is expected.

**4. Revert to the registry provider:**

```shell
unset TF_CLI_CONFIG_FILE
```

`.terraformrc.local` is listed in `.gitignore` and will not be committed.
