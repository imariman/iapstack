import SwiftUI
import IAPStackSampleCore

struct ContentView: View {
  @ObservedObject var store: PurchaseStore
  @State private var endpoint = ""
  @State private var loginToken = ""

  var body: some View {
    NavigationView {
      Form {
        Section(header: Text("Trusted host"), footer: Text("Use your temporary host login token. Your host keeps the durable application bearer and returns a short-lived customer session.")) {
          TextField("HTTPS session URL", text: $endpoint)
            .keyboardType(.URL)
            .textInputAutocapitalization(.never)
            .autocorrectionDisabled()
            .disabled(store.isBusy)
          SecureField("Temporary host login token", text: $loginToken)
            .textInputAutocapitalization(.never)
            .autocorrectionDisabled()
            .privacySensitive()
            .disabled(store.isBusy)
          Button(store.isConnected ? "Reconnect customer session" : "Start customer session") {
            Task {
              await store.connect(endpoint: endpoint, loginToken: loginToken)
              if store.isConnected { loginToken = "" }
            }
          }.disabled(store.isBusy || loginToken.isEmpty || endpoint.isEmpty)
          if let expiry = store.expiresAt {
            Text("Session expires \(expiry, style: .time)").font(.footnote)
            Button("Disconnect", role: .destructive) { store.disconnect(); loginToken = "" }
              .disabled(store.isBusy)
          }
        }

        Section(header: Text("StoreKit catalog")) {
          ForEach(store.products, id: \.id) { product in
            VStack(alignment: .leading, spacing: 6) {
              Text(product.title).font(.headline)
              Text(product.description).font(.subheadline)
              Button("Buy · \(product.price)") { Task { await store.purchase(product) } }
                .disabled(!store.isConnected || !store.canMakePayments || store.isBusy)
            }.padding(.vertical, 4)
          }
          Button("Reload products") { Task { await store.queryProducts() } }.disabled(store.isBusy)
          if !store.canMakePayments { Text("Purchases are currently unavailable.").font(.footnote) }
        }

        Section(header: Text("Purchases")) {
          Button("Restore purchases") { Task { await store.restore() } }
            .disabled(!store.isConnected || store.isBusy)
          Button("Refresh entitlements") { Task { await store.refreshEntitlements() } }
            .disabled(!store.isConnected || store.isBusy)
          Button("Retry verification (\(store.pendingCount))") { Task { await store.retryPending() } }
            .disabled(!store.isConnected || store.isBusy || store.pendingCount == 0)
        }

        Section(header: Text("Entitlements")) {
          if store.entitlements.isEmpty { Text("No entitlements loaded.").foregroundColor(.secondary) }
          ForEach(store.entitlements, id: \.key) { entitlement in
            HStack {
              Text(entitlement.key)
              Spacer()
              Text(entitlement.grantsAccess() ? "Access allowed" : "Access denied")
                .foregroundColor(entitlement.grantsAccess() ? .green : .secondary)
            }
          }
        }

        Section(header: Text("Status")) {
          if store.isBusy { ProgressView("Working…") }
          Text(store.status).font(.callout).accessibilityIdentifier("purchase-status")
        }
      }
      .navigationTitle("IAPStack Example")
      .task { await store.queryProducts() }
    }
    .navigationViewStyle(.stack)
  }
}
