import 'package:flutter/material.dart';
import 'package:iapstack_apple_example/app.dart';
import 'package:iapstack_apple_example/example_config.dart';

/// Starts the manual StoreKit 2 harness without persisting runtime secrets.
void main() => runApp(ExampleApp(config: ExampleConfig.fromEnvironment()));
