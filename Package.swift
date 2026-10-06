// swift-tools-version: 6.1
import PackageDescription

// The root manifest makes the monorepo consumable directly by SwiftPM.
// sdk/ios/Package.swift remains available for local SDK development.
let package = Package(
  name: "IAPStackApple",
  platforms: [.iOS(.v15), .macOS(.v12)],
  products: [.library(name: "IAPStackApple", targets: ["IAPStackApple"])],
  targets: [
    .target(name: "IAPStackApple", path: "sdk/ios/Sources/IAPStackApple"),
    .testTarget(
      name: "IAPStackAppleTests",
      dependencies: ["IAPStackApple"],
      path: "sdk/ios/Tests/IAPStackAppleTests"
    ),
  ],
  swiftLanguageModes: [.v5]
)
