import 'package:flutter/material.dart';
import 'package:iapstack_huawei_example/app.dart';
import 'package:iapstack_huawei_example/example_config.dart';

/// Starts the sandbox example using compile-time runtime configuration.
void main() {
  runApp(ExampleApp(config: ExampleConfig.fromEnvironment()));
}
