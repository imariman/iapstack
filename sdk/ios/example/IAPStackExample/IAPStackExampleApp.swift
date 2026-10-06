import SwiftUI
import IAPStackSampleCore

@main
struct IAPStackExampleApp: App {
  @StateObject private var store = PurchaseStore()

  var body: some Scene {
    WindowGroup { ContentView(store: store) }
  }
}
