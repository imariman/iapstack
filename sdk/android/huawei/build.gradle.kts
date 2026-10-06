plugins {
  id("com.android.library")
  kotlin("android")
  `maven-publish`
}

apply(from = rootProject.file("publishing.gradle.kts"))

android {
  namespace = "com.iapstack.huawei"
  compileSdk = 35
  defaultConfig { minSdk = 26 }
  compileOptions {
    sourceCompatibility = JavaVersion.VERSION_17
    targetCompatibility = JavaVersion.VERSION_17
  }
  testOptions { unitTests.isIncludeAndroidResources = true }
  publishing { singleVariant("release") { withSourcesJar() } }
}

kotlin { jvmToolchain(17) }

dependencies {
  api(project(":core"))
  implementation("com.huawei.hms:iap:6.13.0.300")
  implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.8.1")
  implementation("com.google.code.gson:gson:2.12.1")

  testImplementation(kotlin("test-junit"))
  testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test:1.8.1")
  testImplementation("org.mockito:mockito-core:5.14.2")
  testImplementation("org.robolectric:robolectric:4.14.1")
  testImplementation("com.squareup.okhttp3:mockwebserver:4.12.0")
  testImplementation("com.squareup.okhttp3:okhttp:4.12.0")
}
