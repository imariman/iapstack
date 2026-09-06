# Contributing to IAPStack

Thank you for helping improve IAPStack. The project is prerelease software, so public
contracts and internal boundaries may still change. Keep contributions focused and
describe the user or operator problem they solve.

By participating, you agree to follow the [Code of Conduct](CODE_OF_CONDUCT.md). Report
security vulnerabilities through the private process in [SECURITY.md](SECURITY.md).

## Before you start

Search the [existing issues](https://github.com/imariman/iapstack/issues) before opening
a new one. For a substantial feature or architectural change, open a feature request
first so scope and compatibility can be discussed before implementation.

Never commit store credentials, purchase evidence, API keys, customer data, generated
release evidence from a real account, or local environment files.

## Development setup

Use the Go toolchain declared in `go.mod`, PostgreSQL 17, and the protection settings listed
in the [operations guide](docs/operations.md). The repository README contains a
[Compose quick start](README.md#quick-start).

Run the Go checks relevant to a server change:

```sh
go fmt ./...
go vet ./...
go test -race -count=1 ./...
```

Run PostgreSQL integration tests with `go test -race -tags=integration -count=1 ./...`
while `IAPSTACK_TEST_DATABASE_URL` points to a disposable test database. Changes to a Flutter package should run `dart analyze` or `flutter analyze`
and that package's tests. See each package README under `sdk/flutter/` for its setup.

## Make a change

- Add or update tests for behavior changes and bug fixes where a regression can be
  exercised meaningfully.
- Update `contracts/openapi/v1.yaml`, the HTTP API documentation, and affected clients
  together when changing a public endpoint or schema.
- Preserve application and project scoping, idempotency, and the protected-data
  boundary described in the [architecture decisions](docs/adr/README.md).
- Keep provider-specific types and behavior inside the relevant store adapter.
- Document operator-visible configuration, migrations, compatibility changes, and
  security consequences.
- Use fixtures or sandbox data in tests; redact secrets and customer information from
  logs, screenshots, issue text, and pull requests.

Provider lifecycle changes may require the real-store gates in the
[release runbook](docs/releases/v0.1.0-rc.1.md). A pull request can contribute the code
without claiming a provider gate passed unless the required secret-free evidence is
available for the exact candidate commit.

## Open a pull request

Describe the problem, the resulting behavior, and how you verified it. Link the
relevant issue when one exists. Keep unrelated refactors out of the same pull request,
and call out migrations, public contract changes, operational steps, or known limits.

Contributions are submitted under the repository's
[Apache License 2.0](LICENSE).
