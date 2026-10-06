import {cpSync, mkdirSync, readFileSync, rmSync, writeFileSync} from 'node:fs';
import {dirname, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const stage = resolve(root, '.example-package');
const manifest = JSON.parse(readFileSync(resolve(root, 'package.json'), 'utf8'));
// A package-shaped copy avoids CocoaPods traversing a recursive file:.. symlink.
rmSync(stage, {recursive: true, force: true});
mkdirSync(stage, {recursive: true});
for (const path of manifest.files) cpSync(resolve(root, path), resolve(stage, path), {recursive: true});
delete manifest.scripts;
delete manifest.devDependencies;
writeFileSync(resolve(stage, 'package.json'), JSON.stringify(manifest, null, 2) + '\n');
