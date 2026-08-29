# Deploy IAPStack to Heroku

The Heroku manifest provisions one always-on API dyno, one always-on worker dyno,
and an Essential-0 Heroku Postgres database. Heroku terminates HTTPS for the public
API, injects `PORT` and `DATABASE_URL`, and runs database migrations in the release
phase before a new version becomes current.

> [!IMPORTANT]
> IAPStack is in early development and is not ready for production use. This
> deployment is intended for isolated provider-sandbox testing.

## Before deploying

1. Create a Heroku account and add a payment method. Heroku does not offer a
   permanently free runtime. The manifest deliberately selects two always-on Basic
   dynos because the worker must remain active; Eco dynos sleep and their shared
   monthly hour pool is insufficient for two continuously running processes.
2. Generate two different secrets locally:

   ```sh
   openssl rand -base64 32
   openssl rand -base64 32
   ```

   The Deploy form asks for the first value as `IAPSTACK_PROTECTION_KEY` and the
   second as `IAPSTACK_PROTECTION_FINGERPRINT_KEY`. It independently generates the
   bootstrap and metrics-only bearers. Do not commit any value.
3. Review the current price shown by Heroku. At the prices documented in August
   2026, two Basic dynos plus Essential-0 Postgres total USD 19 per month before
   taxes and add-ons.

## Deploy Button

Click the button in the repository README, choose a region, enter the two generated
keys, and approve the resources. The button uses `app.json`; the container build and
process commands come from `heroku.yml`.

Heroku Buttons create Cedar-generation apps only. The `container` stack used here is
therefore intentional. Both process images are built from the repository Dockerfile.

After deployment:

1. Confirm `https://<app-name>.herokuapp.com/readyz` returns a successful response.
2. Open `https://<app-name>.herokuapp.com/dashboard/` and use the generated
   `IAPSTACK_BOOTSTRAP_ADMIN_KEY` from the app's Config Vars.
3. Create the first durable administrator key, store it securely, and then remove
   `IAPSTACK_BOOTSTRAP_ADMIN_KEY` from Config Vars.
4. Configure only sandbox/test store credentials and point the Flutter example at
   the Heroku HTTPS URL.

Deleting the Heroku app also deletes its attached resources. Export any evidence you
need before cleanup, and verify the database add-on is gone to stop billing.

## Manual deployment

If the Deploy Button is not suitable, create an app with the container stack, attach
`heroku-postgresql:essential-0`, set the same config vars, and push the repository:

```sh
heroku create --stack container
heroku addons:create heroku-postgresql:essential-0
heroku config:set IAPSTACK_PROTECTION_ACTIVE_KEY_ID=primary
git push heroku main
heroku ps:scale web=1:basic worker=1:basic
```

Set the three secret config variables separately so they do not enter shell history.

Official references:

- [Heroku Buttons](https://devcenter.heroku.com/articles/heroku-button)
- [`app.json` schema](https://devcenter.heroku.com/articles/app-json-schema)
- [`heroku.yml` container builds](https://devcenter.heroku.com/articles/build-docker-images-heroku-yml)
- [Heroku pricing](https://www.heroku.com/pricing/)
