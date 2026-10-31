'use strict';

const path = require('path');

// require() resolves from this file, so the script has to live in the
// package whose overrides and patchedDependencies it checks. glob's
// search uses the process cwd, which bun run sets to that package.
process.chdir(path.resolve(__dirname, '..'));

const m = require('minimatch');
if (typeof m !== 'function') {
  throw new Error(
    'require("minimatch") must be a function. Regenerate patches/minimatch@<pin>.patch when overrides.minimatch changes, and keep patchedDependencies pinned to that same version.',
  );
}

const glob = require('glob');
const globVersion = require('glob/package.json').version;
if (String(globVersion).split('.')[0] !== '7') {
  throw new Error(`expected glob 7, found ${globVersion}`);
}
const found = glob.sync('package.json');
if (!found.length) {
  throw new Error(
    'glob.sync("package.json") found nothing. glob 7 needs the minimatch shim; regenerate the patch when the pin changes.',
  );
}

if (m('a/b.js', '**/*.js') !== true) {
  throw new Error("minimatch('a/b.js', '**/*.js') must be true");
}
