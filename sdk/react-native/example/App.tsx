import React, {useEffect, useState} from 'react';
import {AppState, Button, Platform, ScrollView, StyleSheet, Text, TextInput, View} from 'react-native';
import {SafeAreaProvider, SafeAreaView} from 'react-native-safe-area-context';
import {IapStackStore, type StoreProduct, type Storefront} from '@iapstack/react-native/store';

/** Runnable single-tester flow; the host authenticates the tester and selects customer identity. */
export default function App() {
  const [host, setHost] = useState('https://host.example.com/session');
  const [login, setLogin] = useState('');
  const [productId, setProductId] = useState('premium_monthly');
  const [store, setStore] = useState<Storefront>(Platform.OS === 'ios' ? 'apple' : 'google_play');
  const [products, setProducts] = useState<StoreProduct[]>([]);
  const [ready, setReady] = useState(false);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState('Sign in to your trusted host to obtain a short-lived customer session.');
  useEffect(() => {
    const events = IapStackStore.addListener(event => {
      if (event.type === 'error') setMessage(`Purchase update: ${event.code}. Refresh session and restore to retry.`);
      else setMessage(`Verified: ${event.result.entitlements.filter(e => e.grantsAccess).map(e => e.key).join(', ') || 'No access granted'}`);
    });
    const foreground = AppState.addEventListener('change', state => {
      if (state === 'active' && ready) {
        IapStackStore.getEntitlements().then(result => setMessage(`Access: ${result.entitlements.filter(e => e.grantsAccess).map(e => e.key).join(', ') || 'none'}`))
          .catch(() => setMessage('Session may have expired. Sign in again, then restore.'));
      }
    });
    return () => { events.remove(); foreground.remove(); };
  }, [ready]);
  useEffect(() => () => { void IapStackStore.dispose(); }, []);
  const run = async (action: () => Promise<void>) => {
    setBusy(true);
    try { await action(); } catch (error) {
      const code = (error as {code?: string}).code ?? 'operation_failed';
      setMessage(`Failed: ${code}. Refresh session or use Restore to recover interrupted purchases.`);
    } finally { setBusy(false); }
  };
  const connect = async () => {
    await IapStackStore.dispose();
    setReady(false); setProducts([]);
    // Only the separate tester login credential leaves JS. Never enter the application's durable bearer.
    const session = await IapStackStore.requestCustomerSession(host, login);
    await IapStackStore.configure({storefront: store, baseUri: session.baseUri,
      applicationId: session.applicationId, externalCustomerId: session.externalCustomerId,
      customerToken: session.customerToken, productKinds: {[productId]: 'subscription'}});
    setLogin('');
    setReady(true);
    setMessage(await IapStackStore.isAvailable() ? 'Store ready. Load products or restore.' : 'Purchases unavailable; restore and entitlement lookup remain available.');
  };
  return <SafeAreaProvider><SafeAreaView style={styles.safe}><ScrollView contentContainerStyle={styles.page}>
    <Text style={styles.title}>IAPStack</Text>
    <Text>Native purchase → server verification → entitlement access</Text>
    <TextInput accessibilityLabel="Trusted host session URL" value={host} onChangeText={setHost} autoCapitalize="none" style={styles.input} />
    <TextInput accessibilityLabel="Tester login token" value={login} onChangeText={setLogin} autoCapitalize="none" secureTextEntry placeholder="Tester login token (not application bearer)" style={styles.input} />
    <TextInput accessibilityLabel="Subscription product ID" value={productId} onChangeText={setProductId} autoCapitalize="none" editable={!ready} style={styles.input} />
    {Platform.OS === 'android' && <View style={styles.row}>
      <Button title={`Google Play${store === 'google_play' ? ' ✓' : ''}`} disabled={ready || busy} onPress={() => setStore('google_play')} />
      <Button title={`Huawei${store === 'huawei' ? ' ✓' : ''}`} disabled={ready || busy} onPress={() => setStore('huawei')} />
    </View>}
    <Button title="Sign in / refresh session" disabled={busy || !login} onPress={() => run(connect)} />
    <Button title="Load products" disabled={!ready || busy} onPress={() => run(async () => {
      const query = await IapStackStore.queryProducts([productId]); setProducts(query.products);
      setMessage(query.notFoundProductIds.length ? `Not found: ${query.notFoundProductIds.join(', ')}` : 'Choose an offer.');
    })} />
    {products.map(p => <View key={p.selectionKey} style={styles.product}>
      <Text>{p.title} — {p.price}</Text><Text>{p.description}</Text>
      {p.basePlanId && <Text>Base plan: {p.basePlanId}; offer: {p.offerId ?? 'standard'}</Text>}
      <Button title="Purchase and verify" disabled={busy} onPress={() => run(async () => {
        const result = await IapStackStore.purchase(p.selectionKey);
        setMessage(result ? `Verified: ${result.entitlements.filter(e => e.grantsAccess).map(e => e.key).join(', ') || 'No access granted'}` : 'Checkout opened or approval pending. Watch purchase updates.');
      })} />
    </View>)}
    <Button title="Restore" disabled={!ready || busy} onPress={() => run(async () => {
      const result = await IapStackStore.restore(); setMessage(`Restored ${result.results.length} verified purchases.`);
    })} />
    <Button title="Refresh entitlements" disabled={!ready || busy} onPress={() => run(async () => {
      const result = await IapStackStore.getEntitlements(); setMessage(`Access: ${result.entitlements.filter(e => e.grantsAccess).map(e => e.key).join(', ') || 'none'}`);
    })} />
    {store === 'huawei' && <Button title="Resolve Huawei sign-in" disabled={!ready || busy} onPress={() => run(async () => { await IapStackStore.resolveHuaweiEnvironment(); setMessage('Huawei environment ready.'); })} />}
    <Button title="Sign out" disabled={busy} onPress={() => run(async () => {
      await IapStackStore.dispose(); setReady(false); setProducts([]); setMessage('Signed out.');
    })} />
    <Text accessibilityLiveRegion="polite" style={styles.message}>{message}</Text>
  </ScrollView></SafeAreaView></SafeAreaProvider>;
}
const styles = StyleSheet.create({safe: {flex: 1}, page: {padding: 24, gap: 16},
  title: {fontSize: 28, fontWeight: 'bold'}, input: {borderWidth: 1, borderColor: '#888', padding: 10},
  row: {flexDirection: 'row', justifyContent: 'space-between'}, product: {padding: 14, backgroundColor: '#eee'},
  message: {paddingVertical: 20}});
