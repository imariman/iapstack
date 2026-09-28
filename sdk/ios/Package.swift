// swift-tools-version: 6.1
// Sources use trailing commas in argument and parameter lists (SE-0439), which
// require the Swift 6.1 toolchain (Xcode 16.3+). Language mode stays Swift 5.
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
  swiftLanguageModes: [.v5],
)
