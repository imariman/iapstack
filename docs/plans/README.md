# Post-v0.1 plans

These documents are the designs for work that is now in the tree. They do not change the `v0.1.0-rc.1` release gate. No product version is assigned. Hosted SaaS is intentionally not implemented.

| Plan | File | In the tree |
| --- | --- | --- |
| Customer migration and aliases | [customer-migration-alias.md](customer-migration-alias.md) | Migration 010, alias and merge HTTP, dashboard fields |
| RevenueCat one-way import | [revenuecat-import.md](revenuecat-import.md) | `cmd/iapstack-revenuecat-import`, migration 012. Apple re-verify is not included |
| Financial reports | [financial-reports.md](financial-reports.md) | Migration 011 and the Apple consolidated parser. Google and Huawei stay `fixture_required` |
| Remote paywall, hosted SaaS, Kubernetes | [paywall-saas-kubernetes.md](paywall-saas-kubernetes.md) | File-based offering route and `deploy/k8s`. Hosted SaaS is not in this server |

The RevenueCat importer calls the alias HTTP route and never calls customer merge. Historical RevenueCat access is an `import` projection, not a store verification.
