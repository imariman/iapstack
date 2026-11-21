# Application Offering Documents

Place one `<application_id>.json` file per application in this directory so `iapstack` serves it at `GET /v1/applications/<application_id>/offering`.

The `iapstack` container runs with a read-only root filesystem as UID/GID `65532` and mounts `./offerings` at `/etc/iapstack/offerings` (`IAPSTACK_OFFERING_DIRECTORY`). For manual deploys (or hosts with a restrictive umask such as `027` or `077`), make sure both the directory and files are readable by UID `65532`:

```sh
chmod 755 deploy/vps/offerings
chmod 644 deploy/vps/offerings/<application_id>.json
```

`install.sh` applies `755` to `offerings` and `644` to `offerings/*.json` on every run.

> **Backups:** `deploy/vps/offerings/*.json` files are gitignored so upgrades do not touch them, and `deploy/vps/backup.sh` backs up PostgreSQL only (not `.env` or offering JSON files). Keep your offering files in your own repository or back them up off-server together with `.env`.

## Document Constraints

The loader (`internal/offering/loader.go`) validates every request strictly and returns `404` both when a file is missing and when a document is invalid or references an unknown `store_product_id`:

- **File rules:** regular file only, max size `64 KiB` (`65536` bytes), filename must be `<application_id>.json`.
- **Strict JSON:** unknown fields (`DisallowUnknownFields`) and trailing data are rejected.
- **`schema_version`:** must be `1`.
- **`application_id`:** must match `<application_id>` in the filename and pass identifier validation.
- **`revision`:** required, `1`–`128` characters from `[A-Za-z0-9._~-]` (used in `ETag`; ISO timestamps containing `:` are rejected).
- **`offerings` & `packages`:** `offerings` must be non-empty with unique offering `id`s; each offering's `packages` must be non-empty with unique package `id`s within that offering.
- **Optional `display_key`:** package field following the same identifier format rules.
- **Registered products:** every `store_product_id` must already exist in `store_products` (register via the dashboard or `PUT /v1/admin/projects/{project_id}/applications/{application_id}/store-products/{provider_product_id}`).

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
          "store_product_id": "my_app_premium_lifetime",
          "display_key": "lifetime"
        }
      ]
    }
  ]
}
```
