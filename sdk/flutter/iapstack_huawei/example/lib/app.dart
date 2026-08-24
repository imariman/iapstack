import 'package:flutter/material.dart';
import 'package:flutter_bloc/flutter_bloc.dart';
import 'package:iapstack/iapstack.dart';
import 'package:iapstack_huawei/iapstack_huawei.dart';
import 'package:iapstack_huawei_example/configuration_page.dart';
import 'package:iapstack_huawei_example/example_config.dart';
import 'package:iapstack_huawei_example/example_cubit.dart';
import 'package:iapstack_huawei_example/example_page.dart';

/// Root application for manual Huawei sandbox verification.
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
          applicationKey: widget.config.applicationKey,
        ),
      );
      _cubit = ExampleCubit(
        backend: backend,
        huawei: HuaweiIapStack(client: backend),
        externalCustomerId: widget.config.externalCustomerId,
        productId: widget.config.productId,
        productKind: widget.config.productKind,
      );
    }
  }

  @override
  void dispose() {
    _cubit?.close();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final theme = ThemeData(
      colorScheme: ColorScheme.fromSeed(seedColor: Colors.deepPurple),
      useMaterial3: true,
    );
    final cubit = _cubit;
    if (!widget.config.isComplete || cubit == null) {
      return MaterialApp(
        debugShowCheckedModeBanner: false,
        theme: theme,
        home: ConfigurationPage(missingValues: widget.config.missingValues),
      );
    }
    return BlocProvider<ExampleCubit>.value(
      value: cubit,
      child: MaterialApp(
        debugShowCheckedModeBanner: false,
        theme: theme,
        home: ExamplePage(config: widget.config),
      ),
    );
  }
}
