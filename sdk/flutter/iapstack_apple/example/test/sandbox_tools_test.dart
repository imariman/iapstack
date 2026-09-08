import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iapstack_apple_example/sandbox_tools.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  const channel = MethodChannel('com.imariman.iapstack/apple-sandbox-tools');

  test('takes the customer token from the native host', () async {
    final calls = <MethodCall>[];
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, (call) async {
          calls.add(call);
          return 'runtime-customer-token';
        });
    addTearDown(
      () => TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(channel, null),
    );

    final token = await const SandboxTools().takeCustomerToken();

    expect(token, 'runtime-customer-token');
    expect(calls, hasLength(1));
    expect(calls.single.method, 'takeCustomerToken');
    expect(calls.single.arguments, isNull);
  });

  test('uses an empty token when the native host has none', () async {
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, (call) async => null);
    addTearDown(
      () => TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(channel, null),
    );

    expect(await const SandboxTools().takeCustomerToken(), isEmpty);
  });
}
