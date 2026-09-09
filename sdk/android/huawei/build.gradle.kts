plugins {
  kotlin("jvm")
}

dependencies {
  implementation(project(":core"))
  implementation("org.jetbrains.kotlinx:kotlinx-coroutines-core:1.8.1")
  implementation("com.google.code.gson:gson:2.12.1")

  testImplementation(kotlin("test"))
  testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test:1.8.1")
}
