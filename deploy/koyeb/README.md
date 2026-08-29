# Deploy IAPStack to Koyeb

[`deploy.sh`](deploy.sh) creates a Koyeb PostgreSQL database, non-revealed runtime
secrets, an HTTPS API service, and a private worker service. It applies migrations
from the local checkout before deploying either runtime service. Existing protection
and fingerprint secrets are preserved on repeat runs, so rerunning the script does
not silently rotate encryption material.

> [!IMPORTANT]
> IAPStack is in early development and is not ready for production use. Use an
> isolated app and provider-sandbox credentials.

## Cost and account requirements

Koyeb account creation is free, but a payment card is required. Koyeb documents a
temporary USD 29 authorization hold during verification and initially selects the
Pro plan; switch to Starter before deploying if you do not want the Pro subscription.
Read the current signup and billing screen before confirming it.

The script defaults to Koyeb's free web instance and free PostgreSQL database, plus
one paid `eco-micro` worker because free instances cannot run Worker Services. This
shape is only suitable for a short, supervised sandbox check:

- the free web service sleeps after one hour without traffic;
- the free database has a small monthly active-compute allowance and 1 GB storage;
- the continuously connected worker consumes that database allowance quickly.

For a persistent environment, set both runtime services to at least `eco-micro` and
the database to `small`. At the prices documented in August 2026, two `eco-micro`
services total USD 5.36 per month and a Small PostgreSQL database starts at USD 29.76
per month plus storage. Confirm Koyeb's live estimate before creating paid resources.

Koyeb's Deploy Button creates one service only. It cannot express this database,
API, worker, secrets, and migration topology, so the repository intentionally uses
one bootstrap command instead of presenting an incomplete button.

## Deploy

1. Create the account, choose the Starter plan, install the latest Koyeb CLI, and
   authenticate:

   ```sh
   koyeb login
   ```

2. Push the branch you want Koyeb to build. From the repository root, deploy a short
   test environment:

   ```sh
   IAPSTACK_GIT_BRANCH=codex/one-click-deployments ./deploy/koyeb/deploy.sh
   ```

   The default source is the public `github.com/imariman/iapstack` repository. Set
   `IAPSTACK_GIT_REPOSITORY` when deploying a fork.

3. For a persistent paid environment, choose the paid sizes explicitly:

   ```sh
   IAPSTACK_KOYEB_DATABASE_INSTANCE=small \
   IAPSTACK_KOYEB_API_INSTANCE=eco-micro \
   IAPSTACK_KOYEB_WORKER_INSTANCE=eco-micro \
   ./deploy/koyeb/deploy.sh
   ```

4. Read the app's assigned `.koyeb.app` domain in the dashboard, verify `/readyz`,
   and open `/dashboard/`. The script prints a newly created bootstrap administrator
   key once; use it to create the first durable administrator key and store that key
   securely.

5. Remove `IAPSTACK_BOOTSTRAP_ADMIN_KEY` from the API service after bootstrap. The
   script deliberately disables deploy-on-push; rerun it when you want a deployment.

The database connection string is passed to the migration command through the local
process environment and to Koyeb services through a secret reference. It is never
written to a repository file. Run the script from a trusted checkout because it
executes the repository's migration command locally.

## Cleanup

Delete the `api` and `worker` services, the database, the app, and the five secrets
whose names start with the app name. Verify the Billing page afterward; deleting the
app alone may not remove separately stored secrets.

Official references:

- [Koyeb services](https://www.koyeb.com/docs/reference/services)
- [Koyeb instances and free-instance limits](https://www.koyeb.com/docs/reference/instances)
- [Koyeb databases](https://www.koyeb.com/docs/databases)
- [Koyeb pricing FAQ](https://www.koyeb.com/docs/faqs/pricing)
- [Deploy to Koyeb Button](https://www.koyeb.com/docs/build-and-deploy/deploy-to-koyeb-button)
- [Koyeb CLI reference](https://www.koyeb.com/docs/build-and-deploy/cli/reference)
