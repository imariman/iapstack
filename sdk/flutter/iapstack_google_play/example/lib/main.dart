import 'package:flutter/material.dart';
import 'package:iapstack_google_play_example/app.dart';
import 'package:iapstack_google_play_example/example_config.dart';

/// Starts the internal-testing harness with compile-time runtime configuration.
void main() {
  runApp(ExampleApp(config: ExampleConfig.fromEnvironment()));
}
