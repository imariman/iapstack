#!/usr/bin/env bash
# Compile independent consumers against the exact staged Maven artifacts.
set -euo pipefail
repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
staging_repository="${1:-$repository_root/sdk/android/build/maven}"
staging_repository="$(cd "$staging_repository" && pwd)"
version="$(tr -d '\r\n' < "$repository_root/sdk/version.txt")"
consumer_directory="$(mktemp -d "${TMPDIR:-/tmp}/iapstack-android-consumer.XXXXXX")"
trap 'rm -rf "$consumer_directory"' EXIT

mkdir -p "$consumer_directory/jvm/src/main/kotlin" "$consumer_directory/android/src/main/kotlin"
cat > "$consumer_directory/settings.gradle.kts" <<'EOF'
pluginManagement {
  repositories { gradlePluginPortal(); google(); mavenCentral() }
}
rootProject.name = "iapstack-published-sdk-consumer"
include(":jvm", ":android")
EOF
cat > "$consumer_directory/build.gradle.kts" <<'EOF'
plugins {
  kotlin("jvm") version "1.9.24" apply false
  kotlin("android") version "1.9.24" apply false
  id("com.android.library") version "8.9.3" apply false
}
allprojects {
  repositories {
    exclusiveContent {
      forRepository {
        maven {
          url = uri(providers.gradleProperty("stagingRepository").get())
          if (providers.gradleProperty("pomOnly").get().toBoolean()) {
            metadataSources { mavenPom(); artifact(); ignoreGradleMetadataRedirection() }
          }
        }
      }
      filter { includeGroup("io.github.imariman.iapstack") }
    }
    google()
    mavenCentral()
    maven("https://developer.huawei.com/repo/") {
      content { includeGroupByRegex("com\\.huawei.*") }
    }
  }
}
EOF
cat > "$consumer_directory/gradle.properties" <<'EOF'
android.useAndroidX=true
org.gradle.jvmargs=-Xmx1g
EOF
cat > "$consumer_directory/jvm/build.gradle.kts" <<'EOF'
plugins { kotlin("jvm") }
kotlin { jvmToolchain(17) }
dependencies {
  implementation("io.github.imariman.iapstack:iapstack-core:${providers.gradleProperty("sdkVersion").get()}")
}
EOF
cat > "$consumer_directory/jvm/src/main/kotlin/Consumer.kt" <<'EOF'
import com.iapstack.core.IapStackClient
import com.iapstack.core.IapStackConfig
import kotlinx.coroutines.Dispatchers
import okhttp3.OkHttpClient
import java.net.URI

// Exercise both the default constructor and public dependency types. The consumer
// must not add OkHttp or coroutines to compensate for missing publication metadata.
fun clients(): List<IapStackClient> {
  val config = IapStackConfig(URI("https://iap.example"), "app", "customer-token")
  return listOf(
    IapStackClient(config),
    IapStackClient(config, httpClient = OkHttpClient(), ioDispatcher = Dispatchers.IO),
  )
}
EOF
cat > "$consumer_directory/android/build.gradle.kts" <<'EOF'
plugins { id("com.android.library"); kotlin("android") }
android {
  namespace = "com.iapstack.consumer"
  compileSdk = 35
  defaultConfig { minSdk = 26 }
  compileOptions {
    sourceCompatibility = JavaVersion.VERSION_17
    targetCompatibility = JavaVersion.VERSION_17
  }
}
kotlin { jvmToolchain(17) }
dependencies {
  val version = providers.gradleProperty("sdkVersion").get()
  implementation("io.github.imariman.iapstack:iapstack-google-play:$version")
  implementation("io.github.imariman.iapstack:iapstack-huawei:$version")
}
EOF
cat > "$consumer_directory/android/src/main/AndroidManifest.xml" <<'EOF'
<manifest />
EOF
cat > "$consumer_directory/android/src/main/kotlin/Consumer.kt" <<'EOF'
package com.iapstack.consumer

import android.app.Activity
import com.iapstack.core.IapStackClient
import com.iapstack.core.IapStackConfig
import com.iapstack.googleplay.BillingClientGooglePlayPlatform
import com.iapstack.googleplay.GooglePlayIapStack
import com.iapstack.googleplay.GooglePlayProductKind
import com.iapstack.huawei.HmsHuaweiIapPlatform
import com.iapstack.huawei.HuaweiIapStack
import com.iapstack.huawei.HuaweiProductKind
import kotlinx.coroutines.flow.Flow
import java.net.URI

// Only the two store artifacts are declared: core must resolve transitively.
fun stores(activity: Activity): Pair<GooglePlayIapStack, HuaweiIapStack> {
  val client = IapStackClient(IapStackConfig(URI("https://iap.example"), "app", "customer-token"))
  val playCatalog = mapOf("premium" to GooglePlayProductKind.SUBSCRIPTION)
  val play = BillingClientGooglePlayPlatform(activity, playCatalog) { activity }
  val huawei = HmsHuaweiIapPlatform(activity)
  val updates: Flow<*> = play.purchaseUpdates
  check(updates === play.purchaseUpdates)
  return GooglePlayIapStack(client, playCatalog, play) to
    HuaweiIapStack(client, mapOf("premium" to HuaweiProductKind.SUBSCRIPTION), huawei)
}
EOF

# Check Gradle module metadata and the POM fallback used by Maven consumers.
for pom_only in false true; do
  "$repository_root/sdk/android/gradlew" -p "$consumer_directory" --no-daemon \
    --console=plain --rerun-tasks \
    "-PstagingRepository=$staging_repository" "-PsdkVersion=$version" "-PpomOnly=$pom_only" \
    :jvm:build :android:assembleRelease
done
