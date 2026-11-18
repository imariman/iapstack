import {
  defineRailway,
  github,
  group,
  postgres,
  project,
  service,
} from "railway/iac";

// "compact" runs the API and the durable worker in one service, which keeps a small
// installation inside the Hobby plan's included usage. "split" runs them as separate
// services that can be sized independently. The API service keeps its name in both
// topologies: renaming it would recreate the service and regenerate its sealed
// protection keys, which makes stored credentials unreadable.
const topology: "compact" | "split" = "compact";

const source = github("imariman/iapstack", { branch: "main" });
const region = "europe-west4";
const hexadecimalRootKey = {
  description: "Generated 32-byte IAPStack root key; do not regenerate during redeploys.",
  generator: 'secret(64, "abcdef0123456789")',
  isSealed: true,
};

export default defineRailway(() => {
  const database = postgres("IAPStack Postgres", { region });

  const api = service("IAPStack API", {
    source,
    build: {
      builder: "DOCKERFILE",
      dockerfilePath: "Dockerfile",
    },
    start:
      topology === "compact"
        ? "/usr/local/bin/iapstack server"
        : "/usr/local/bin/iapstack api",
    preDeploy: "/usr/local/bin/iapstack migrate",
    healthcheck: "/readyz",
    healthcheckTimeout: 300,
    replicas: { [region]: 1 },
    deploy: {
      drainingSeconds: 30,
      restartPolicyType: "ALWAYS",
    },
    env: {
      IAPSTACK_DATABASE_URL: database.env.DATABASE_URL,
      IAPSTACK_PROTECTION_ACTIVE_KEY_ID: "primary",
      IAPSTACK_PROTECTION_KEY: hexadecimalRootKey,
      IAPSTACK_PROTECTION_FINGERPRINT_KEY: {
        ...hexadecimalRootKey,
        description:
          "Generated stable 32-byte fingerprint root; changing it requires a data migration.",
      },
      IAPSTACK_BOOTSTRAP_ADMIN_KEY: {
        description:
          "Installation-only administrator bearer; remove it after creating a stored admin key.",
        generator:
          'secret(48, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")',
        isSealed: true,
      },
      IAPSTACK_METRICS_BEARER_TOKEN: {
        description:
          "Generated metrics-only bearer shared by API and worker; do not reuse an admin key.",
        generator:
          'secret(48, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")',
        isSealed: true,
      },
      IAPSTACK_HUAWEI_ALLOW_PRIVATE_NETWORKS: "false",
      IAPSTACK_WEBHOOK_ALLOW_PRIVATE_NETWORKS: "false",
      IAPSTACK_LOG_LEVEL: "info",
      ...(topology === "compact" ? { IAPSTACK_WORKER_CONCURRENCY: "2" } : {}),
    },
  });

  if (topology === "compact") {
    return project("IAPStack", {
      resources: [group("IAPStack", [database, api])],
    });
  }

  const worker = service("IAPStack Worker", {
    source,
    build: {
      builder: "DOCKERFILE",
      dockerfilePath: "Dockerfile",
    },
    start: "/usr/local/bin/iapstack worker",
    replicas: { [region]: 1 },
    deploy: {
      drainingSeconds: 30,
      restartPolicyType: "ALWAYS",
    },
    env: {
      IAPSTACK_DATABASE_URL: database.env.DATABASE_URL,
      IAPSTACK_PROTECTION_ACTIVE_KEY_ID:
        api.env.IAPSTACK_PROTECTION_ACTIVE_KEY_ID,
      IAPSTACK_PROTECTION_KEY: api.env.IAPSTACK_PROTECTION_KEY,
      IAPSTACK_PROTECTION_FINGERPRINT_KEY:
        api.env.IAPSTACK_PROTECTION_FINGERPRINT_KEY,
      IAPSTACK_METRICS_BEARER_TOKEN:
        api.env.IAPSTACK_METRICS_BEARER_TOKEN,
      IAPSTACK_HUAWEI_ALLOW_PRIVATE_NETWORKS: "false",
      IAPSTACK_WEBHOOK_ALLOW_PRIVATE_NETWORKS: "false",
      IAPSTACK_LOG_LEVEL: "info",
    },
  });

  return project("IAPStack", {
    resources: [group("IAPStack", [database, api, worker])],
  });
});
