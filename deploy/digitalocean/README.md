# Deploy IAPStack to DigitalOcean App Platform

The app spec in [`.do/app.yaml`](../../.do/app.yaml) defines a public HTTPS API,
a private worker, a pre-deploy migration job, and a development PostgreSQL database
in Frankfurt. The API receives a default `ondigitalocean.app` HTTPS domain and the
database connection is injected through an App Platform bindable variable.

> [!IMPORTANT]
> IAPStack is in early development and is not ready for production use. The included
> development database is intended only for isolated provider-sandbox testing.

## Cost and account requirements

Creating a DigitalOcean account is free, but this complete deployment is not. App
Platform's free tier covers static sites only. At the prices documented in August
2026, the two 512 MiB containers cost USD 5 per month each and the development
database costs USD 7 per month, for a USD 17 monthly baseline. The migration job is
charged only while it runs. Confirm the live estimate before creating the app.

DigitalOcean's Deploy Button currently supports only one service, optionally with a
development database. It cannot provision IAPStack's required persistent worker and
pre-deploy job, so this repository intentionally does not present a misleading
Deploy-to-DO button. The full topology is still created with one `doctl` command.

## Deploy

1. Create a DigitalOcean account, add a payment method, install `doctl`, and run
   `doctl auth init`.
2. Copy `.do/app.yaml` to a temporary location outside the repository.
3. Generate three different values:

   ```sh
   openssl rand -base64 32
   openssl rand -base64 32
   openssl rand -base64 32
   ```

4. Replace the three `REPLACE_WITH_...` values in the temporary spec. Never commit
   the resulting file.
5. Validate and create the app:

   ```sh
   doctl apps spec validate /path/to/iapstack.app.yaml
   doctl apps create --spec /path/to/iapstack.app.yaml
   ```

6. Read the assigned URL with `doctl apps list`, then verify `<url>/readyz` and open
   `<url>/dashboard/`.
7. Use `IAPSTACK_BOOTSTRAP_ADMIN_KEY` to create the first durable administrator key,
   store it securely, and remove the bootstrap variable from the app spec in the
   control panel.

The committed spec has automatic deployments disabled so a push cannot unexpectedly
replace a sandbox environment. Trigger deployments deliberately from the control
panel or with `doctl apps create-deployment <app-id>`.

Destroy the app after testing and confirm that its development database has also
been removed to stop billing.

Official references:

- [App spec reference](https://docs.digitalocean.com/products/app-platform/reference/app-spec/)
- [App Platform workers](https://docs.digitalocean.com/products/app-platform/how-to/manage-workers/)
- [Deploy Button limits](https://docs.digitalocean.com/products/app-platform/how-to/add-deploy-do-button/)
- [App Platform pricing](https://docs.digitalocean.com/products/app-platform/details/pricing/)
