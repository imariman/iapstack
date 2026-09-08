import 'package:flutter/material.dart';
import 'package:iapstack_apple_example/app.dart';
import 'package:iapstack_apple_example/example_config.dart';
import 'package:iapstack_apple_example/sandbox_tools.dart';

/// Starts the manual StoreKit 2 harness without persisting runtime secrets.
Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  final customerToken = await const SandboxTools().takeCustomerToken();
  runApp(
    ExampleApp(
      config: ExampleConfig.fromEnvironment(customerToken: customerToken),
    ),
  );
}
