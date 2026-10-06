// Publish the verified tarballs, allowing a partial release to resume unchanged.
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { spawnSync } from 'node:child_process';

const version = readFileSync(new URL('../sdk/version.txt', import.meta.url), 'utf8').trim();
const directory = process.argv[2];
if (!directory) throw new Error('Usage: node scripts/publish-sdk-npm.mjs ARCHIVE_DIRECTORY');
for (const name of ['host', 'react-native']) {
  const archive = resolve(directory, `iapstack-${name}-${version}.tgz`);
  const integrity = `sha512-${createHash('sha512').update(readFileSync(archive)).digest('base64')}`;
  const existing = spawnSync('npm', ['view', `@iapstack/${name}@${version}`, 'dist.integrity', '--json', '--registry=https://registry.npmjs.org/'], { encoding: 'utf8' });
  if (existing.status === 0) {
    if (JSON.parse(existing.stdout) !== integrity) throw new Error(`Published @iapstack/${name}@${version} differs; never overwrite a release.`);
    console.log(`@iapstack/${name}@${version}: identical published archive, skipping`);
    continue;
  }
  let error;
  try { error = JSON.parse(existing.stdout).error; } catch { /* Treat an unknown failure as fatal. */ }
  if (error?.code !== 'E404') throw new Error(`Cannot verify @iapstack/${name} registry state; resolve authentication/network access before publishing.`);
  const channel = spawnSync('npm', ['view', `@iapstack/${name}`, 'dist-tags.next', '--json', '--registry=https://registry.npmjs.org/'], { encoding: 'utf8' });
  if (channel.status === 0 && channel.stdout.trim()) {
    const current = JSON.parse(channel.stdout);
    const parts = value => {
      const match = /^(\d+)\.(\d+)\.(\d+)-sdk\.(\d+)$/.exec(value);
      if (!match) throw new Error('Unknown next-channel version; inspect before publishing.');
      return match.slice(1).map(Number);
    };
    const previous = parts(current), candidate = parts(version);
    const firstDifference = previous.findIndex((part, index) => part !== candidate[index]);
    if (firstDifference >= 0 && previous[firstDifference] > candidate[firstDifference]) {
      throw new Error(`Refusing to move @iapstack/${name} next backwards from ${current} to ${version}.`);
    }
  } else if (channel.status !== 0) {
    let channelError;
    try { channelError = JSON.parse(channel.stdout).error; } catch { /* Fail closed. */ }
    if (channelError?.code !== 'E404') throw new Error('Cannot verify npm next-channel state.');
  }
  const published = spawnSync('npm', ['publish', archive, '--registry=https://registry.npmjs.org/', '--access=public', '--tag=next', '--provenance', '--ignore-scripts'], { stdio: 'inherit' });
  if (published.status !== 0) process.exit(published.status ?? 1);
}
