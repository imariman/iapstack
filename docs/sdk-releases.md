# SDK installation and releases

The Go, TypeScript, Swift, Kotlin and React Native SDKs implement IAPStack HTTP API
v1. They are post-v0.1 SDKs and remain prerelease. Flutter is still the only mobile
client in the server's v0.1 release gate. Publishing an SDK neither certifies a
server release nor changes its three-provider evidence requirements.

## Version policy

`sdk/version.txt` is the independent SDK release-train version. The initial prepared
version is `0.1.0-sdk.1`; it is **not published merely because it appears here**.
Check the [GitHub SDK release](https://github.com/imariman/iapstack/releases) and
registry availability before using registry install commands.

All five SDK families share `X.Y.Z-sdk.N` prerelease versions for now. Breaking API
changes increment the minor version while major is zero; fixes increment the patch
or the numbered prerelease while a release is being qualified. The supported server
contract is `/v1`, independently of the server's release number. A stable SDK policy
will be introduced explicitly when compatibility commitments are ready.

| Distribution | Release identity |
| --- | --- |
| SwiftPM | Root semantic tag `0.1.0-sdk.1`, exact dependency version |
| Go module | Tag `sdk/go/v0.1.0-sdk.1` |
| npm | `@iapstack/host` and `@iapstack/react-native`, version `0.1.0-sdk.1`, dist-tag `next` |
| Maven | `io.github.imariman.iapstack`, artifacts `iapstack-core`, `iapstack-google-play`, `iapstack-huawei`, version `0.1.0-sdk.1` |

The root `Package.swift` points at the canonical sources in `sdk/ios`. Semantic SDK
tags deliberately omit the server's `v` prefix and use the `sdk.N` prerelease label;
they do not invoke or change the server release workflow. Use an **exact** SDK
version in SwiftPM because the same repository also has server version tags.

## Install after publication

### Go trusted host

```sh
go get github.com/imariman/iapstack/sdk/go@v0.1.0-sdk.1
```

### TypeScript trusted host and React Native

```sh
npm install @iapstack/host@0.1.0-sdk.1
# In a React Native application:
npm install @iapstack/react-native@0.1.0-sdk.1
```

The host package includes compiled ESM and TypeScript declarations and requires
Node 18+. The React Native package includes its native bridge and a generated copy
of the canonical Swift/Kotlin companion sources, so it needs no unpublished native
package. See its README for native platform setup and the runnable example.

### SwiftPM

```swift
.package(url: "https://github.com/imariman/iapstack.git", exact: "0.1.0-sdk.1")
```

Link the `IAPStackApple` product. Before publication use `.package(path: "../iapstack")`
for a local checkout, or an explicitly reviewed commit revision after it is pushed;
do not substitute an old server tag that lacks the root manifest.

### Android

The initial Maven destination is GitHub Packages. Its public Maven packages still
require authentication to download; Maven Central is not claimed as a destination.
Add the repository in the consuming Gradle project's dependency resolution settings:

```kotlin
maven {
  url = uri("https://maven.pkg.github.com/imariman/iapstack")
  credentials {
    username = providers.environmentVariable("GITHUB_ACTOR").orNull
    password = providers.environmentVariable("GITHUB_TOKEN").orNull
  }
  content { includeGroup("io.github.imariman.iapstack") }
}
```

Use a GitHub token with `read:packages` on the developer machine or in CI. Keep it
outside source control. Add `google()` and `mavenCentral()` for transitive dependencies;
Huawei consumers also need `https://developer.huawei.com/repo/`.

```kotlin
implementation("io.github.imariman.iapstack:iapstack-core:0.1.0-sdk.1")
implementation("io.github.imariman.iapstack:iapstack-google-play:0.1.0-sdk.1")
implementation("io.github.imariman.iapstack:iapstack-huawei:0.1.0-sdk.1")
```

Include only the store companions the application uses. Both depend on the same
version of `iapstack-core` through their published POM/Gradle module metadata.

## Verify packages locally

```sh
python3 -m unittest discover -s scripts/tests -p 'test_*.py'
scripts/pack-sdk-npm.sh "$PWD/tmp/sdk-packages"
(cd sdk/android && ./gradlew --no-daemon \
  :core:publishAllPublicationsToStagingRepository \
  :google-play:publishAllPublicationsToStagingRepository \
  :huawei:publishAllPublicationsToStagingRepository)
python3 scripts/check-sdk-packages.py --maven-dir sdk/android/build/maven
scripts/check-sdk-android-consumer.sh
swift test
```

The npm check inspects actual tarballs and installs the host archive in a separate
plain Node consumer. Maven checks inspect all three binaries, source jars, versions
and companion-to-core dependencies. Independent JVM and Android consumers compile
against the staged artifacts, using both Gradle metadata and POM-only resolution,
so undeclared public dependencies cannot be masked by the monorepo examples.
`SDK package checks` runs these in CI. Swift
checks cover both manifests and the native sample; React Native native checks build
the example against the packed companion sources.

## Registry setup and publication

1. Confirm that the publishing npm account controls the `@iapstack` scope. A local
   `npm whoami` can confirm login but does not prove scope write access. Missing scope
   ownership or credentials must be resolved before dispatching publication.
2. For initial package creation, configure an appropriately scoped npm token as
   `NPM_TOKEN` in the GitHub `stable-release` environment. The workflow publishes from
   GitHub with provenance. After bootstrap, configure npm trusted publishing for
   owner `imariman`, repository `iapstack`, workflow `sdk-release.yml`, environment
   `stable-release`, with direct `npm publish` allowed; then remove the bootstrap
   token. Node 24 and npm 11.5.1+ support this OIDC flow.
3. GitHub Maven publishing uses the job's `GITHUB_TOKEN` and `packages: write`.
   Ensure repository settings permit package creation. Configure the desired package
   visibility on first publication. Consumers require `read:packages` credentials.
4. Protect SDK tags (`*-sdk.*` and `sdk/go/v*`) against updates/deletion, in addition
   to the existing server `v*` tag rules. Never force-update a published version.
5. Merge tested code, wait for `CI`, `Swift SDK checks`, `SDK package checks`, and
   `React Native native checks` on the **same main commit**, then dispatch `SDK release`
   with the exact value in `sdk/version.txt`. The workflow uses the existing protected
   `stable-release` environment but never invokes the server release workflow.

The workflow rebuilds and inspects the archives, publishes npm with provenance and
Maven with matching staged bytes, creates Swift/Go tags atomically, verifies the Go
module through the public proxy, attests the downloadable archives and creates a
GitHub prerelease. It never marks the release as latest or changes npm's latest tag.

Publication across registries is not atomic. If a run stops partway through, rerun
the failed job at the **same commit**: identical npm/Maven versions are skipped,
missing Maven artifacts are uploaded and existing SDK tags must match the commit.
Conflicting bytes cause a hard failure. An old partial npm publication is rejected
if it would move `next` behind a newer version. GitHub release assets are uploaded
and downloaded for comparison while the release is still a draft, then published.
Never delete a partial version to replace
its contents; inspect the failure and publish a new version if code needs changing.
Maven metadata retains prior versions and does not move latest backwards on a rerun.

## Shared client behavior

- Durable application bearers stay on trusted hosts. Mobile clients receive
  short-lived, customer-bound sessions and keep them only in memory.
- The API origin must be configured directly: clients reject HTTP redirects rather
  than resending tokens or purchase evidence to redirect targets. Custom transports
  must preserve that rule as well as timeout and response-size limits.
- Restore responses must contain one result per submitted purchase before a native
  companion treats the batch as verified or finishes store transactions.
- Real App Store, Play and AppGallery lifecycle validation remains a separate
  release requirement. Local StoreKit configuration, mocks and package builds do
  not establish real-provider certification.

Registry references: [npm trusted publishing](https://docs.npmjs.com/trusted-publishers/),
[GitHub Gradle registry](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-gradle-registry),
[Android library publication](https://developer.android.com/build/publish-library/upload-library).
