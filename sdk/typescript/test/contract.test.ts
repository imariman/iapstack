import { test } from 'bun:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { MAX_RETRY_AFTER_MS } from '../src/index';

const here = dirname(fileURLToPath(import.meta.url));
const repositoryRoot = join(here, '..', '..', '..');

function readRepositoryFile(...parts: string[]): string {
  return readFileSync(join(repositoryRoot, ...parts), 'utf8');
}

function sectionAfter(document: string, marker: string): string {
  const start = document.indexOf(marker);
  if (start < 0) {
    return '';
  }
  const rest = document.slice(start);
  const next = rest.indexOf('\n  /', marker.length);
  if (next < 0) {
    return rest;
  }
  return rest.slice(0, next);
}

test('OpenAPI declares host operations and authentication', () => {
  const contract = readRepositoryFile('contracts', 'openapi', 'v1.yaml');
  for (const needle of [
    'operationId: createCustomerSession',
    'operationId: getCustomerEntitlements',
    'applicationBearer:',
    'customerSessionBearer:',
    'external_customer_id',
    'expires_at',
    'customer_id',
    'entitlements',
    'ErrorEnvelope',
    'ApiError',
  ]) {
    assert.equal(contract.includes(needle), true, `OpenAPI contract is missing ${needle}`);
  }

  const sessionsBlock = sectionAfter(contract, '/v1/applications/{application_id}/customer-sessions:');
  assert.equal(
    sessionsBlock.includes('- applicationBearer: []'),
    true,
    'createCustomerSession must require the durable application bearer',
  );

  const entitlementsBlock = sectionAfter(
    contract,
    '/v1/applications/{application_id}/customers/{external_customer_id}/entitlements:',
  );
  assert.equal(
    entitlementsBlock.includes('- customerSessionBearer: []'),
    true,
    'getCustomerEntitlements must stay on the customer-session host lookup path',
  );
  assert.equal(
    entitlementsBlock.includes('- applicationBearer: []'),
    false,
    'getCustomerEntitlements must not claim application-bearer authentication',
  );
});

test('webhook contract fields are documented', () => {
  const document = readRepositoryFile('docs', 'api-v1.md');
  for (const needle of [
    'IAPStack-Event-ID',
    'IAPStack-Timestamp',
    'IAPStack-Signature',
    'HMAC-SHA-256',
    'v2=<hex>',
    '<event_id>',
    '<unix_timestamp>',
    '<exact_raw_body>',
  ]) {
    assert.equal(document.includes(needle), true, `webhook contract is missing ${needle}`);
  }
});

test('shared SDK behavior is documented', () => {
  const document = readRepositoryFile('docs', 'api-v1.md');
  for (const needle of [
    '## Shared SDK behavior',
    '`max(jitter, min(Retry-After, 30 s))`',
    '`grantsAccess`.** Go and Swift expose a method',
    '`effective_starts_at` is informational',
  ]) {
    assert.equal(document.includes(needle), true, `shared SDK behavior is missing ${needle}`);
  }
  assert.equal(MAX_RETRY_AFTER_MS, 30_000);
  const contract = readRepositoryFile('contracts', 'openapi', 'v1.yaml');
  assert.equal(contract.includes('Retry-After:'), true);
});

test('host SDK package has no runtime dependencies', () => {
  const pkg = JSON.parse(readFileSync(join(here, '..', 'package.json'), 'utf8')) as {
    dependencies?: Record<string, string>;
  };
  assert.equal(pkg.dependencies === undefined || Object.keys(pkg.dependencies).length === 0, true);
});
