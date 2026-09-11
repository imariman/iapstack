plugins {
  kotlin("jvm")
}

kotlin {
  jvmToolchain(17)
}

dependencies {
  api(project(":core"))

  testImplementation(kotlin("test"))
  testImplementation("com.squareup.okhttp3:mockwebserver:4.12.0")
  testImplementation("com.squareup.okhttp3:okhttp:4.12.0")
}

tasks.test {
  useJUnitPlatform()
}
