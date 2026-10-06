plugins {
  kotlin("jvm")
  `maven-publish`
}

apply(from = rootProject.file("publishing.gradle.kts"))

java { withSourcesJar() }

kotlin {
  jvmToolchain(17)
}

dependencies {
  api("org.jetbrains.kotlinx:kotlinx-coroutines-core:1.8.1")
  api("com.squareup.okhttp3:okhttp:4.12.0")
  implementation("com.google.code.gson:gson:2.12.1")

  testImplementation(kotlin("test"))
  testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test:1.8.1")
  testImplementation("com.squareup.okhttp3:mockwebserver:4.12.0")
  testImplementation("org.yaml:snakeyaml:2.4")
}

tasks.test {
  useJUnitPlatform()
}
