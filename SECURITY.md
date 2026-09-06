# Security Policy

IAPStack processes purchase evidence, store credentials, API keys, customer
identifiers, and entitlement state. Please report suspected vulnerabilities privately
so they can be investigated before public disclosure.

## Supported versions

IAPStack is prerelease software and does not yet have a supported stable version.
Security fixes are developed on the default branch. This policy will be updated with
version-specific support information when releases are published.

## Report a vulnerability

Use a [private GitHub security advisory][new-advisory]. Do not open a public issue or
pull request for an undisclosed vulnerability.

Include, when available:

- the affected commit, version, deployment mode, and store provider;
- the conditions required to reproduce the issue;
- the security impact and which data or trust boundary is affected;
- a minimal proof of concept, logs, or traces with secrets and personal data removed;
- any mitigation you have already tested.

The maintainer may ask for more information, coordinate a fix, and discuss a disclosure
date in the advisory. Avoid testing against systems or accounts you do not own or have
permission to assess.

For the project's security assumptions, deployment requirements, and remaining risks,
read the [v0.1 threat model](docs/security.md). Operators remain responsible for their
own TLS termination, network policy, secret storage, backups, provider configuration,
monitoring, and incident response.

[new-advisory]: https://github.com/imariman/iapstack/security/advisories/new
