import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_bloc/flutter_bloc.dart';
import 'package:iapstack/iapstack.dart';
import 'package:iapstack_google_play/iapstack_google_play.dart';
import 'package:iapstack_google_play_example/app_theme.dart';
import 'package:iapstack_google_play_example/configuration_page.dart';
import 'package:iapstack_google_play_example/example_config.dart';
import 'package:iapstack_google_play_example/example_cubit.dart';
import 'package:iapstack_google_play_example/example_page.dart';

/// Root application for manual Google Play internal testing.
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
  bool _bootstrapping = false;
  String? _bootstrapError;

  @override
  void initState() {
    super.initState();
    if (widget.config.isComplete) {
      _bootstrap();
    }
  }

  Future<void> _bootstrap() async {
    if (_bootstrapping) return;
    setState(() {
      _bootstrapping = true;
      _bootstrapError = null;
    });
    try {
      var customerToken = widget.config.customerToken.trim();
      if (customerToken.isEmpty) {
        customerToken = await _mintCustomerSession(widget.config);
      }
      if (!mounted) return;
      final backend = IapStackClient(
        IapStackConfig(
          baseUri: Uri.parse(widget.config.baseUrl),
          applicationId: widget.config.applicationId,
          customerToken: customerToken,
        ),
      );
      final googlePlay = GooglePlayIapStack(
        client: backend,
        productKinds: widget.config.productKinds,
      );
      final cubit = ExampleCubit(
        backend: backend,
        googlePlay: googlePlay,
        externalCustomerId: widget.config.externalCustomerId,
        productIds: widget.config.productKinds.keys.toSet(),
      )..initialize();
      setState(() {
        _cubit = cubit;
        _bootstrapping = false;
      });
    } on Object {
      if (!mounted) return;
      setState(() {
        _bootstrapping = false;
        _bootstrapError = 'Could not create a fresh customer session.';
      });
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
        home: widget.config.isComplete
            ? _SessionBootstrapPage(
                loading: _bootstrapping,
                error: _bootstrapError,
                onRetry: _bootstrap,
              )
            : ConfigurationPage(missingValues: widget.config.missingValues),
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

Future<String> _mintCustomerSession(ExampleConfig config) async {
  final brokerUri = Uri.parse(config.customerSessionBrokerUrl);
  if (brokerUri.scheme != 'https') {
    throw ArgumentError('Customer session broker must use HTTPS');
  }
  final brokerCertificate = await rootBundle.load(
    'assets/iapstack_test_broker_ca.pem',
  );
  final securityContext = SecurityContext(withTrustedRoots: true)
    ..setTrustedCertificatesBytes(brokerCertificate.buffer.asUint8List());
  final client = HttpClient(context: securityContext)
    ..connectionTimeout = const Duration(seconds: 10);
  try {
    final request = await client.postUrl(brokerUri);
    final requestBody = utf8.encode(
      jsonEncode(<String, String>{
        'application_id': config.applicationId,
        'external_customer_id': config.externalCustomerId,
      }),
    );
    request.headers.contentType = ContentType.json;
    request.contentLength = requestBody.length;
    request.add(requestBody);
    final response = await request.close().timeout(const Duration(seconds: 15));
    final body = await utf8.decoder.bind(response).join();
    if (response.statusCode != HttpStatus.ok) {
      throw const HttpException('Customer session broker rejected request');
    }
    final payload = jsonDecode(body);
    if (payload is! Map<String, dynamic> ||
        payload['token'] is! String ||
        (payload['token'] as String).trim().isEmpty) {
      throw const FormatException('Invalid customer session response');
    }
    return payload['token'] as String;
  } finally {
    client.close(force: true);
  }
}

class _SessionBootstrapPage extends StatelessWidget {
  const _SessionBootstrapPage({
    required this.loading,
    required this.error,
    required this.onRetry,
  });

  final bool loading;
  final String? error;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: const Text('IAPStack Google Play Test')),
    body: Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: <Widget>[
            if (loading) ...<Widget>[
              const CircularProgressIndicator(),
              const SizedBox(height: 16),
              const Text('Creating a fresh customer session…'),
            ] else ...<Widget>[
              const Icon(Icons.cloud_off, size: 48),
              const SizedBox(height: 16),
              Text(error ?? 'Customer session broker is unavailable.'),
              const SizedBox(height: 16),
              FilledButton(onPressed: onRetry, child: const Text('Retry')),
            ],
          ],
        ),
      ),
    ),
  );
}
