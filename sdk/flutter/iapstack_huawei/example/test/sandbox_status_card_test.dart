import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iapstack_huawei/iapstack_huawei.dart';
import 'package:iapstack_huawei_example/sandbox_status_card.dart';

void main() {
  testWidgets('renders both sandbox conditions on a narrow device',
      (tester) async {
    tester.view.physicalSize = const Size(320, 640);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await tester.pumpWidget(
      const MaterialApp(
        home: Scaffold(
          body: SafeArea(
            child: SandboxStatusCard(
              status: HuaweiSandboxStatus(
                isSandboxUser: true,
                isSandboxApk: false,
                apkVersion: '43',
              ),
            ),
          ),
        ),
      ),
    );

    expect(find.text('Huawei sandbox blocked'), findsOneWidget);
    expect(find.text('Test account'), findsOneWidget);
    expect(find.text('Sandbox APK'), findsOneWidget);
    expect(find.text('Eligible'), findsOneWidget);
    expect(find.text('Not eligible'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}
