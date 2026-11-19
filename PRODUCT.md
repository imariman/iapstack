# Product

<!-- impeccable:product-schema 1 -->

> Written without the init interview, at the user's request to go straight to the dashboard redesign.
> Every line below is inferred from the repository (README.md, docs/v0.1-scope.md, design-system/iapstack/MASTER.md, dashboard/) and should be confirmed or corrected by the maintainers.

## Platform

web

## Users

Engineers and small-team operators who self-host IAPStack for their own mobile apps. They open the operations dashboard to commission store connections (Apple, Google Play, Huawei), check that purchases are being verified and entitlements delivered, and investigate a specific customer, transaction or failed webhook. Usually a solo developer or a small studio running several apps, not a large finance or support team.

## Product Purpose

IAPStack is an open-source, self-hosted control plane that verifies in-app purchases server-side and turns store transactions into durable application entitlements. It gives teams one backend-owned, store-neutral model across Apple App Store, Google Play and Huawei AppGallery while credentials, purchase data and access decisions stay in infrastructure they control. Success: every purchase is verified before access changes, and the operator can see at a glance that the route from store to app backend is healthy.

## Positioning

Self-hosted and open source (Apache-2.0), written in Go, backed by PostgreSQL. Unlike hosted subscription platforms, the operator owns the credentials, the data and the deployment. The pipeline Store → Verify → Access → Deliver is the product's mechanism: provider evidence is verified before access changes, and every state transition is durable and observable.

## Operating Context

- The dashboard is embedded in the Go binary and served same-origin at `/dashboard/` with a strict CSP (`default-src 'self'`; no third-party scripts, styles or fonts). Assets: `dashboard/index.html`, `app.css`, `app.js`, no build step, no framework.
- Sign-in exchanges an admin bearer key for a 12-hour browser session.
- Main jobs: pick a project, read the purchase route health, read 30-day verification activity and revenue status, manage applications (store credentials, webhook endpoint, application keys), browse the product map, job queues, recent verifications, customers and outbound notification delivery, rotate admin/application access keys.
- Used on a desktop or laptop during setup and incident checks; occasionally on a phone to glance at health.

## Capabilities and Constraints

- v0.1 scope: subscriptions and non-consumable purchases; consumables, hosted operation and other stores are out of scope.
- Revenue is shown only when official store financial reports are imported; sandbox/test purchases never count as real revenue and are shown separately as "sandbox test value".
- Terminology in use: project, application, provider, environment (production/sandbox/test), entitlement, product, store mapping, customer, access (allowed/denied/unresolved), lifecycle state, inbox/outbox/reconciliation queues, webhook delivery.
- Prerelease software; nothing in the UI may imply production readiness, hosted service, or customer counts it does not have.

## Brand Commitments

- Name: IAPStack. A Go gopher mascot exists (`assets/iapstack-gopher.png`), used in the README.
- Logo explorations exist in the untracked `brand/` folder (`brand/logo/kose.svg`, `deste.svg`); not yet adopted, so not binding.
- No confirmed voice guide; existing copy is plain, precise and technical.
- Visual preference (user decision, 2026-11-18): for the operations dashboard the user chose the category standard, a polished dark developer-SaaS look, over themed directions, asking for it to be executed better than the first sketch. Craft references: Linear, Vercel, Stripe. Later extended at the user's request with an equivalent light theme; the dashboard follows the operating system by default and offers System / Light / Dark from the sign-in screen and the top bar.

## Evidence on Hand

- Real UI copy and data model in `dashboard/` and `internal/transport/httpapi/admin.go`.
- No real customer data, testimonials, logos or metrics. Previews must use clearly synthetic data.

## Product Principles

- Verification before access: the UI shows evidence and state, never optimistic guesses.
- Honest numbers: never present sandbox value or estimates as revenue.
- Operator-owned: secrets are shown once and never echoed back.
- Health at a glance, detail on demand.

## Accessibility & Inclusion

State is never communicated by colour alone (existing design-system rule). Keyboard focus visible, WCAG AA contrast, reduced-motion respected.
