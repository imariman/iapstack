import {cpSync, existsSync, mkdirSync, rmSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {dirname, resolve} from 'node:path';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const sdk = resolve(root, '..');
// Published tarballs already contain this immutable source snapshot.
if (existsSync(resolve(sdk, 'ios/Sources/IAPStackApple')) && existsSync(resolve(sdk, 'android/core/src/main/kotlin'))) {
  rmSync(resolve(root, 'native'), {recursive: true, force: true});
  mkdirSync(resolve(root, 'native'), {recursive: true});
  cpSync(resolve(sdk, 'ios/Sources/IAPStackApple'), resolve(root, 'native/ios'), {recursive: true});
  for (const module of ['core', 'google-play', 'huawei']) {
    cpSync(resolve(sdk, `android/${module}/src/main/kotlin`), resolve(root, `native/android/${module}`), {recursive: true});
  }
  cpSync(resolve(sdk, '../LICENSE'), resolve(root, 'LICENSE'));
} else if (!existsSync(resolve(root, 'native/ios/AppleIAPStack.swift'))) {
  throw new Error('Canonical native SDK sources or prepared native snapshot are required');
}
