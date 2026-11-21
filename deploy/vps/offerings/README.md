# Application Offering Documents

Place one `<application_id>.json` file per application in this directory so `iapstack` serves it at `GET /v1/applications/<application_id>/offering`.

Each file is mounted read-only into `/etc/iapstack/offerings` (`IAPSTACK_OFFERING_DIRECTORY`) inside the `iapstack` container, which runs as UID/GID `65532`. Make sure files here are world-readable (`chmod 644 <application_id>.json`) and a matching record in `store_products` exists for every `store_product_id`.

`*.json` files in this directory are gitignored so upgrades do not touch operator-managed offerings.

## Example (`<application_id>.json`)

```json
{
  "schema_version": 1,
  "application_id": "my-app",
  "revision": "2026-01-01",
  "offerings": [
    {
      "id": "default",
      "packages": [
        {
          "id": "lifetime",
          "store_product_id": "my_app_premium_lifetime"
        }
      ]
    }
  ]
}
```
