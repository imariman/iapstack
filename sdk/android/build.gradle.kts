plugins {
  kotlin("jvm") version "1.9.24" apply false
}

allprojects {
  repositories {
    mavenCentral()
    google()
  }
}

subprojects {
  apply(plugin = "org.jetbrains.kotlin.jvm")

  kotlin {
    jvmToolchain(17)
  }

  tasks.withType<Test> {
    useJUnitPlatform()
  }
}
