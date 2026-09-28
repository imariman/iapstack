plugins {
  kotlin("jvm") version "1.9.24" apply false
  kotlin("android") version "1.9.24" apply false
  id("com.android.library") version "8.9.3" apply false
  id("com.android.application") version "8.9.3" apply false
}

allprojects {
  repositories {
    google()
    mavenCentral()
    maven("https://developer.huawei.com/repo/") {
      content { includeGroupByRegex("com\\.huawei.*") }
    }
  }
}
