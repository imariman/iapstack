package huawei

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
)

// providerResolverFixture returns one deterministic DNS answer.
type providerResolverFixture struct {
	addresses []netip.Addr
}

// providerDialerFixture records attempts made after address validation.
type providerDialerFixture struct {
	addresses []string
}

// LookupNetIP returns the configured provider address set.
func (resolver providerResolverFixture) LookupNetIP(
	context.Context,
	string,
	string,
) ([]netip.Addr, error) {
	return resolver.addresses, nil
}

// DialContext records one pinned destination and simulates an unavailable provider.
func (dialer *providerDialerFixture) DialContext(
	_ context.Context,
	_ string,
	address string,
) (net.Conn, error) {
	dialer.addresses = append(dialer.addresses, address)
	return nil, errors.New("provider unavailable")
}

// TestValidateHTTPSRejectsUnsafeDestinations verifies literal private and malformed endpoints fail early.
func TestValidateHTTPSRejectsUnsafeDestinations(t *testing.T) {
	t.Parallel()

	for _, endpoint := range []string{
		"http://provider.example/order",
		"https://user:secret@provider.example/order",
		"https://provider.example/order#fragment",
		"https://127.0.0.1/order",
		"https://169.254.169.254/latest/meta-data",
		"https://[::1]/order",
	} {
		if err := validateHTTPS(endpoint); err == nil {
			t.Errorf("validateHTTPS(%q) error = nil, want rejection", endpoint)
		}
	}
	if err := validateHTTPS("https://provider.example/order"); err != nil {
		t.Fatalf("validateHTTPS() public hostname error = %v", err)
	}
}

// TestProviderDialerRejectsMixedResolution verifies one unsafe DNS answer blocks every connection.
func TestProviderDialerRejectsMixedResolution(t *testing.T) {
	t.Parallel()

	connectionDialer := &providerDialerFixture{}
	dialer := &publicProviderDialer{
		resolver: providerResolverFixture{addresses: []netip.Addr{
			netip.MustParseAddr("8.8.8.8"),
			netip.MustParseAddr("127.0.0.1"),
		}},
		dialer: connectionDialer,
	}
	if _, err := dialer.DialContext(context.Background(), "tcp", "provider.example:443"); err == nil {
		t.Fatal("DialContext() error = nil, want unsafe resolution rejection")
	}
	if len(connectionDialer.addresses) != 0 {
		t.Fatalf("DialContext() attempted unsafe connection to %v", connectionDialer.addresses)
	}
}

// TestProviderDialerPinsPublicResolution verifies connections use the validated address, not the hostname.
func TestProviderDialerPinsPublicResolution(t *testing.T) {
	t.Parallel()

	connectionDialer := &providerDialerFixture{}
	dialer := &publicProviderDialer{
		resolver: providerResolverFixture{addresses: []netip.Addr{netip.MustParseAddr("8.8.8.8")}},
		dialer:   connectionDialer,
	}
	_, _ = dialer.DialContext(context.Background(), "tcp", "provider.example:443")
	if len(connectionDialer.addresses) != 1 || connectionDialer.addresses[0] != "8.8.8.8:443" {
		t.Fatalf("DialContext() addresses = %v, want pinned public address", connectionDialer.addresses)
	}
}
