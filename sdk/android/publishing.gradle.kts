import org.gradle.api.publish.PublishingExtension
import org.gradle.api.publish.maven.MavenPublication

// One version across the post-v0.1 SDK release train; server releases are separate.
group = "io.github.imariman.iapstack"
version = rootProject.file("../version.txt").readText().trim()

tasks.withType<AbstractArchiveTask>().configureEach {
  isPreserveFileTimestamps = false
  isReproducibleFileOrder = true
  from(rootProject.file("../../LICENSE")) { into("META-INF") }
}

configure<PublishingExtension> {
  repositories {
    // Only stage locally; scripts/publish-sdk-maven.py verifies and uploads the exact bytes.
    maven {
      name = "Staging"
      url = uri(rootProject.layout.buildDirectory.dir("maven"))
    }
  }
}

// AGP registers the release component only after evaluating the Android DSL.
afterEvaluate {
  configure<PublishingExtension> {
    publications {
      create<MavenPublication>("release") {
        artifactId = "iapstack-${project.name}"
        from(components[if (project.name == "core") "java" else "release"])
        pom {
          name.set("IAPStack ${project.name} SDK")
          description.set("Prerelease IAPStack v1 client and native store integration.")
          url.set("https://github.com/imariman/iapstack")
          licenses {
            license {
              name.set("Apache License, Version 2.0")
              url.set("https://www.apache.org/licenses/LICENSE-2.0.txt")
              distribution.set("repo")
            }
          }
          developers {
            developer {
              id.set("imariman")
              name.set("IAPStack Contributors")
              url.set("https://github.com/imariman")
            }
          }
          scm {
            connection.set("scm:git:https://github.com/imariman/iapstack.git")
            developerConnection.set("scm:git:ssh://git@github.com/imariman/iapstack.git")
            url.set("https://github.com/imariman/iapstack")
          }
        }
      }
    }
  }
}
