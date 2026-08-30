# Deployment targets

A complete IAPStack environment needs a public HTTPS API, durable worker execution,
PostgreSQL, and a migration step. API and worker can share one compact process until
measured capacity or isolation requirements justify separate services. Prices and allowances below reflect official documentation
checked in August 2026; always review the provider's live estimate before approval.

| Target | Account and free allowance | Complete IAPStack test | Repository definition |
| --- | --- | --- | --- |
| Render | Free signup; free web and 30-day PostgreSQL support a disposable compact sandbox | Default Blueprint is free; production can use one paid compact service or split API/worker services | [`render.yaml`](../render.yaml) |
| Railway | New account trial: USD 5 for up to 30 days; then USD 1 monthly Free credit | Trial can cover a short test; an always-on stack will normally exceed the recurring credit | [Railway guide](railway/README.md) |
| Heroku | Payment method required; no permanent free runtime | About USD 19/month for the committed two-dyno and PostgreSQL shape | [`app.json`](../app.json) |
| DigitalOcean | Account signup is free; App Platform free tier is static-only | About USD 17/month for API, worker, and development PostgreSQL | [`.do/app.yaml`](../.do/app.yaml) |
| Fly.io | Short trial only: two Machine-hours or seven days | Managed PostgreSQL starts around USD 40.80/month with default storage, before API and worker Machines | [`fly.toml`](../fly.toml) |
| Koyeb | Card required, USD 29 temporary authorization hold, and prorated selected plan charge | Short default uses free API + free 5-hour database + USD 2.68/month worker; persistent shape starts around USD 35.12/month plus DB storage | [Koyeb bootstrap](koyeb/README.md) |
| Coolify | Self-hosted software is free; Cloud starts at USD 5/month | You still provide an always-on Linux server; best when suitable infrastructure already exists | [Coolify Compose](coolify/compose.yaml) |

For the first provider-sandbox exercise, Render is the least complicated compact
deployment: its Blueprint creates all resources together and returns an HTTPS domain.
Railway is attractive for a brief cost-controlled trial, but its account verification,
credit ceiling, and account-owned template publication add variables. Coolify is the
most economical long-term option only when you already operate a suitable public
server and accept responsibility for backups, updates, firewalling, and uptime.

Do not deploy every target simultaneously just to compare dashboards. Create one
isolated environment, set a strict spend limit where the provider supports one,
complete the provider-sandbox lifecycle, then delete every service, database, disk,
volume, and separately billed resource before moving to the next target.

## Deliberate exclusions

Vercel is not included because IAPStack requires background work to remain available
inside a persistent process; a request-driven function deployment would be incomplete. AWS, Google Cloud,
and Azure can run the Docker image, but a responsible one-click definition there needs
provider-specific networking, managed database, secret management, identity, logging,
backup, and budget policy. Those enterprise IaC modules should be designed separately
instead of being represented by a deceptively small manifest.

Official pricing and limit references:

- [Render free instances](https://render.com/docs/free)
- [Railway trial](https://docs.railway.com/pricing/free-trial) and [plans](https://docs.railway.com/pricing/plans)
- [Heroku pricing](https://www.heroku.com/pricing/)
- [DigitalOcean App Platform pricing](https://docs.digitalocean.com/products/app-platform/details/pricing/)
- [Fly.io trial](https://fly.io/docs/about/free-trial/) and [Managed Postgres](https://fly.io/docs/mpg/)
- [Koyeb pricing FAQ](https://www.koyeb.com/docs/faqs/pricing), [instances](https://www.koyeb.com/docs/reference/instances), and [databases](https://www.koyeb.com/docs/databases)
- [Coolify pricing](https://coolify.io/pricing)
