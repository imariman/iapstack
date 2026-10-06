import java.util.Properties

plugins {
  id("com.android.application")
  kotlin("android")
}

// Local package identity, HMS app ID, and signing configuration are never checked in.
val local = Properties().apply {
  val config = file("store.properties")
  if (config.exists()) config.inputStream().use { load(it) }
}
android {
  namespace = "com.iapstack.example"
  compileSdk = 35
  defaultConfig {
    applicationId = local.getProperty("applicationId", "com.iapstack.example.nativeclient")
    minSdk = 26
    targetSdk = 35
    versionCode = 1
    versionName = "1.0"
    manifestPlaceholders["hmsAppId"] = local.getProperty("hmsAppId", "")
  }
  signingConfigs {
    if (local.getProperty("storeFile") != null) {
      create("store") {
        storeFile = file(local.getProperty("storeFile"))
        keyAlias = local.getProperty("keyAlias")
        storePassword = System.getenv("IAPSTACK_ANDROID_STORE_PASSWORD")
        keyPassword = System.getenv("IAPSTACK_ANDROID_KEY_PASSWORD")
      }
    }
  }
  buildTypes {
    getByName("release") { signingConfig = signingConfigs.findByName("store") }
  }
  testOptions { unitTests.isIncludeAndroidResources = true }
  compileOptions {
    sourceCompatibility = JavaVersion.VERSION_17
    targetCompatibility = JavaVersion.VERSION_17
  }
}
kotlin { jvmToolchain(17) }

dependencies {
  implementation(project(":google-play"))
  implementation(project(":huawei"))
  implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.8.1")
  testImplementation(kotlin("test-junit"))
  testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test:1.8.1")
  testImplementation("org.robolectric:robolectric:4.14.1")
}
