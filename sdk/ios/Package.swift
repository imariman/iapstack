// swift-tools-version: 5.9
import PackageDescription

let package = Package(
  name: "IAPStackApple",
  platforms: [
    .iOS(.v15),
    .macOS(.v12),
  ],
  products: [
    .library(
      name: "IAPStackApple",
      targets: ["IAPStackApple"],
    ),
  ],
  targets: [
    .target(
      name: "IAPStackApple",
      path: "Sources/IAPStackApple",
    ),
    .testTarget(
      name: "IAPStackAppleTests",
      dependencies: ["IAPStackApple"],
      path: "Tests/IAPStackAppleTests",
    ),
  ],
)
