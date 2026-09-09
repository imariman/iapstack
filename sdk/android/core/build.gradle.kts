plugins {
  kotlin("jvm")
}

dependencies {
  implementation("com.squareup.okhttp3:okhttp:4.12.0")
  implementation("com.google.code.gson:gson:2.12.1")

  testImplementation(kotlin("test"))
  testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test:1.8.1")
  testImplementation("com.squareup.okhttp3:mockwebserver:4.12.0")
  testImplementation("org.snakeyaml:snakeyaml:2.4")
}
