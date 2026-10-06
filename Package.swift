// swift-tools-version: 6.1
// Sources use trailing commas in argument and parameter lists (SE-0439), which
// require the Swift 6.1 toolchain (Xcode 16.3+). Language mode stays Swift 5.
import PackageDescription

// The only manifest for the Swift SDK: SwiftPM consumers, local development, CI
// and sdk/ios/example all build these sources through it.
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
