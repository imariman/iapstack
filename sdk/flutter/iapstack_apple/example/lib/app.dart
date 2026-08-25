import 'package:flutter/material.dart';
import 'package:flutter_bloc/flutter_bloc.dart';
import 'package:iapstack/iapstack.dart';
import 'package:iapstack_apple/iapstack_apple.dart';
import 'package:iapstack_apple_example/app_theme.dart';
import 'package:iapstack_apple_example/configuration_page.dart';
import 'package:iapstack_apple_example/example_config.dart';
import 'package:iapstack_apple_example/example_cubit.dart';
import 'package:iapstack_apple_example/example_page.dart';

/// Root application for manual StoreKit 2 testing.
class ExampleApp extends StatefulWidget {
  /// Creates the configured example application.
  const ExampleApp({required this.config, super.key});

  /// Runtime-only example configuration.
  final ExampleConfig config;

  @override
  State<ExampleApp> createState() => ExampleAppState();
}

/// Owns and closes SDK resources for [ExampleApp].
class ExampleAppState extends State<ExampleApp> {
  ExampleCubit? _cubit;

  @override
  void initState() {
    super.initState();
    if (widget.config.isComplete) {
      final backend = IapStackClient(
        IapStackConfig(
          baseUri: Uri.parse(widget.config.baseUrl),
          applicationId: widget.config.applicationId,
          customerToken: widget.config.customerToken,
        ),
      );
      final apple = AppleIapStack(
        client: backend,
        productKinds: widget.config.productKinds,
      );
      _cubit = ExampleCubit(
        backend: backend,
        apple: apple,
        externalCustomerId: widget.config.externalCustomerId,
        productIds: widget.config.productKinds.keys.toSet(),
      )..initialize();
    }
  }

  @override
  void dispose() {
    _cubit?.close();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final cubit = _cubit;
    if (!widget.config.isComplete || cubit == null) {
      return MaterialApp(
        debugShowCheckedModeBanner: false,
        theme: AppTheme.light,
        home: ConfigurationPage(missingValues: widget.config.missingValues),
      );
    }
    return BlocProvider<ExampleCubit>.value(
      value: cubit,
      child: MaterialApp(
        debugShowCheckedModeBanner: false,
        theme: AppTheme.light,
        home: ExamplePage(config: widget.config),
      ),
    );
  }
}
