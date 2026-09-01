import Flutter
import StoreKit
import UIKit

@main
@objc class AppDelegate: FlutterAppDelegate, FlutterImplicitEngineDelegate {
  private var sandboxToolsChannel: FlutterMethodChannel?

  override func application(
    _ application: UIApplication,
    didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?
  ) -> Bool {
    return super.application(application, didFinishLaunchingWithOptions: launchOptions)
  }

  func didInitializeImplicitFlutterEngine(_ engineBridge: FlutterImplicitEngineBridge) {
    GeneratedPluginRegistrant.register(with: engineBridge.pluginRegistry)
    guard let registrar = engineBridge.pluginRegistry.registrar(
      forPlugin: "IAPStackAppleSandboxTools") else {
      return
    }
    let channel = FlutterMethodChannel(
      name: "com.imariman.iapstack/apple-sandbox-tools",
      binaryMessenger: registrar.messenger())
    channel.setMethodCallHandler { [weak self] call, result in
      self?.handleSandboxTools(call: call, result: result)
    }
    sandboxToolsChannel = channel
  }

  private func handleSandboxTools(call: FlutterMethodCall, result: @escaping FlutterResult) {
    Task { @MainActor in
      guard #available(iOS 15.0, *) else {
        result(FlutterError(
          code: "ios_version_unsupported",
          message: "StoreKit sandbox lifecycle tools require iOS 15 or later.",
          details: nil))
        return
      }
      guard let scene = UIApplication.shared.connectedScenes
        .compactMap({ $0 as? UIWindowScene })
        .first(where: { $0.activationState == .foregroundActive }) else {
        result(FlutterError(
          code: "window_scene_unavailable",
          message: "A foreground window scene is required.",
          details: nil))
        return
      }
      do {
        switch call.method {
        case "showManageSubscriptions":
          try await AppStore.showManageSubscriptions(in: scene)
          result(nil)
        case "beginRefundRequest":
          guard
            let arguments = call.arguments as? [String: Any],
            let productID = arguments["productId"] as? String,
            !productID.isEmpty
          else {
            result(FlutterError(
              code: "invalid_product_id",
              message: "A subscription product identifier is required.",
              details: nil))
            return
          }
          guard let verification = await Transaction.latest(for: productID) else {
            result(FlutterError(
              code: "transaction_not_found",
              message: "No StoreKit transaction exists for this product.",
              details: nil))
            return
          }
          let transaction: Transaction
          switch verification {
          case .verified(let verified):
            transaction = verified
          case .unverified:
            result(FlutterError(
              code: "transaction_unverified",
              message: "StoreKit did not verify the latest transaction.",
              details: nil))
            return
          }
          let status = try await transaction.beginRefundRequest(in: scene)
          switch status {
          case .success:
            result("submitted")
          case .userCancelled:
            result("cancelled")
          @unknown default:
            result("unknown")
          }
        default:
          result(FlutterMethodNotImplemented)
        }
      } catch {
        result(FlutterError(
          code: "storekit_sandbox_action_failed",
          message: "The StoreKit sandbox action failed.",
          details: nil))
      }
    }
  }
}
