# Repository public-readiness review — 2026-09-06

This is a repository publication review, not evidence of a production-ready release.
The three-provider lifecycle gates in [the release runbook](v0.1.0-rc.1.md) remain
required for any new release candidate or stable release.

## Pull requests and validation

PRs #48–#56 address entitlement concurrency, Huawei notification authentication and
product identity, Apple JWS policy, inbox redelivery, Google Play acknowledgement
identity, webhook signing secrets, retryable failures, and restore filtering.

The pre-merge review found six successful CI jobs for each of #48–#54. The Go source
ordering failure in #55 and Dart formatting failure in #56 required test-file fixes.
Overlapping test helpers in #48/#53 and #49/#50 require retaining both regressions.
The final combined revision must pass the full CI workflow: repository checks, Go
race/integration tests and coverage, Flutter SDK checks, iOS build, container build,
and the Compose release gate.

## Repository content

Gitleaks 8.30.1 scanned all fetched Git refs (165 commits before merges). Four
findings were individually verified as synthetic test fixtures: two documented
non-production HMAC values and two UUID customer-binding values in Apple tests.
No live credentials were identified. A separate redacted scan of GitHub issue,
comment, release, and deployment metadata found no findings. Scanning is an automated
check with manual triage, not proof that every possible secret pattern is absent.

Community files provide contribution instructions, conduct expectations, support
routes, private vulnerability reporting, issue forms, a PR template, and CODEOWNERS.
The README describes the actual three-provider scope and prerelease limitations.

## Sandbox deployments

The Render dashboard was checked as well as GitHub deployment records. Render's live
commit identifiers differ from the SHA carried in the GitHub deployment records;
use the provider dashboard to identify the deployed revision.

| Component | Observed state |
| --- | --- |
| API and worker sandbox | Live on `7b6bc4d8b2eac484a1634424ca86c87e21cf9c3e`; compact `server` command; readiness HTTP 200 |
| Webhook receiver | Live on `8be4304ac08132e5f1b6391fac8dfb6a5af9348d`; readiness HTTP 204 |
| PostgreSQL | Version 17, available; database-specific rules block all external internet traffic |
| Deploy policy | Both web services track `main`; auto-deploy and PR previews are off |
| Unauthenticated reads | API administration and metrics return HTTP 401; receiver metrics path returns HTTP 404 |

The two web services use free instances and may sleep. The free PostgreSQL database
is scheduled to expire on **2026-09-29** according to the Render dashboard. Preserve
any needed test evidence and data before that date, or arrange a supported database
plan. No services, credentials, deployments, or data were deleted or redeployed in
this review. The live services predate PRs #48–#56 and must not be treated as validation
of those fixes. GitHub labels the sandbox deployment records as production; that
label does not establish production readiness.

## Historical prereleases

`v0.1.0-rc1` and `v0.1.0-rc2` were published on 2026-08-24. Both are marked as
prereleases, neither is latest, and neither has attached release assets. They predate
the current schema-v2 evidence gate and the fixes under review. The repository has
an example evidence file, not completed three-provider release evidence.

Retain these tags and release records as history. Do not promote them to stable,
reuse their tag names, or recommend them for production. The README identifies them
as legacy previews. New releases must use the protected release workflow and its
numbered candidate format, such as `v0.1.0-rc.1`.

## Publication configuration

GitHub returned HTTP 403 for rulesets while the repository was private on the current
plan. The publication procedure therefore changes visibility and immediately applies:

- An active `main` ruleset requiring PRs, resolved review threads, all six CI jobs,
  dependency review, an up-to-date branch, and CodeQL results without high/critical
  security findings or error-level findings. Force pushes and deletion are blocked.
  There are no bypass actors. The approval count is zero so the sole maintainer can
  merge their own PRs while the automated gates remain enforced.
- An active `v*` tag ruleset blocking tag updates, force pushes, and deletion.
- Secret scanning, push protection, private vulnerability reporting, Dependabot
  alerts/security updates, and weekly dependency update configuration.
- A CodeQL workflow for Go, JavaScript/TypeScript, and GitHub Actions with extended
  queries on pushes, PRs (including forks), and a weekly schedule. Dart is covered by the SDK analyzer/tests and dependency review, not CodeQL.
- A `stable-release` environment limited to `main` with maintainer review.

The settings must be read back from GitHub after activation, and the initial CodeQL
run must complete before considering scanning validated. Enabling repository
security controls does not close the real-store lifecycle release gates.
