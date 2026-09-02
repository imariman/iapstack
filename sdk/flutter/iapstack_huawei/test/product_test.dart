import 'package:flutter_test/flutter_test.dart';
import 'package:iapstack_huawei/iapstack_huawei.dart';

void main() {
  group('HuaweiProduct', () {
    test('only exposes active products as purchasable', () {
      for (final testCase in <({int status, bool expected})>[
        (status: 0, expected: true),
        (status: 1, expected: false),
        (status: -1, expected: false),
      ]) {
        expect(
          _product(status: testCase.status).isPurchasable,
          testCase.expected,
          reason: 'Huawei status ${testCase.status}',
        );
      }
    });

    test('uses value equality for complete AppGallery metadata', () {
      final product = _product();
      final equal = _product();
      final different = _product(status: 1);

      expect(product, equal);
      expect(product.hashCode, equal.hashCode);
      expect(product, isNot(different));
    });
  });

  test('HuaweiProductQuery snapshots input collections', () {
    final products = <HuaweiProduct>[_product()];
    final missing = <String>{'missing'};
    final query = HuaweiProductQuery(
      products: products,
      notFoundProductIds: missing,
    );

    products.clear();
    missing.clear();

    expect(query.products, hasLength(1));
    expect(query.notFoundProductIds, const <String>{'missing'});
    expect(() => query.products.clear(), throwsUnsupportedError);
    expect(() => query.notFoundProductIds.clear(), throwsUnsupportedError);
  });
}

HuaweiProduct _product({int status = 0}) => HuaweiProduct(
      id: 'premium_monthly',
      kind: HuaweiProductKind.subscription,
      title: 'Premium Monthly',
      description: 'Monthly access',
      price: r'$4.99',
      priceMicros: 4990000,
      currency: 'USD',
      status: status,
      originalPrice: r'$9.99',
      originalPriceMicros: 9990000,
      promotionalPrice: r'$2.99',
      promotionalPriceMicros: 2990000,
      subscriptionPeriod: 'P1M',
      freeTrialPeriod: 'P7D',
    );
