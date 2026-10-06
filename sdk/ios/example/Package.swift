// swift-tools-version: 6.1
import PackageDescription

let package = Package(
  name: "IAPStackSampleCore",
  platforms: [.iOS(.v15), .macOS(.v12)],
  products: [.library(name: "IAPStackSampleCore", targets: ["IAPStackSampleCore"])],
  dependencies: [.package(name: "IAPStackApple", path: "../../..")],
  targets: [
    .target(name: "IAPStackSampleCore", dependencies: [
      .product(name: "IAPStackApple", package: "IAPStackApple"),
    ]),
    .testTarget(name: "IAPStackSampleCoreTests", dependencies: ["IAPStackSampleCore"]),
  ],
  swiftLanguageModes: [.v5]
)
