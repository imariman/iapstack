// swift-tools-version: 5.9
import PackageDescription

let package = Package(
  name: "IAPStackApple",
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
