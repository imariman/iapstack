package huawei

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// providerAddressResolver resolves every address associated with a Huawei endpoint.
type providerAddressResolver interface {
	// LookupNetIP returns matching IPv4 and IPv6 addresses for destination validation.
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

// providerConnectionDialer opens a connection to one validated pinned address.
type providerConnectionDialer interface {
	// DialContext opens a context-bound network connection.
	DialContext(context.Context, string, string) (net.Conn, error)
}

// restrictedProviderDialer resolves and pins Huawei connections under the configured address policy.
type restrictedProviderDialer struct {
	allowPrivateNetworks bool
	resolver             providerAddressResolver
	dialer               providerConnectionDialer
}

var (
	// providerSharedAddressPrefix blocks carrier-grade NAT destinations.
	providerSharedAddressPrefix = netip.MustParsePrefix("100.64.0.0/10")
	// providerBenchmarkAddressPrefix blocks the IPv4 inter-network benchmark range.
	providerBenchmarkAddressPrefix = netip.MustParsePrefix("198.18.0.0/15")
)

// newProviderClient constructs a proxy-free, redirect-free client with connection-time address enforcement.
func newProviderClient(timeout time.Duration, allowPrivateNetworks bool) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = (&restrictedProviderDialer{
		allowPrivateNetworks: allowPrivateNetworks,
		resolver:             net.DefaultResolver,
		dialer:               &net.Dialer{},
	}).DialContext
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// validateHTTPS requires an absolute HTTPS provider endpoint permitted by the configured address policy.
func validateHTTPS(value string, allowPrivateNetworks bool) error {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.Fragment != "" || strings.Contains(value, "#") {
		return errors.New("huawei endpoint must be an absolute HTTPS URL without user information or fragments")
	}
	hostname := parsed.Hostname()
	if hostname == "" || strings.Contains(hostname, "%") {
		return errors.New("huawei endpoint hostname is invalid")
	}
	if address, parseError := netip.ParseAddr(hostname); parseError == nil &&
		!allowPrivateNetworks && !isPublicProviderAddress(address) {
		return errors.New("huawei endpoint must use a public network address")
	}
	return nil
}

// DialContext resolves, validates, and pins one Huawei endpoint connection.
func (dialer *restrictedProviderDialer) DialContext(
	ctx context.Context,
	network string,
	address string,
) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("huawei endpoint address is invalid")
	}
	addresses, err := dialer.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("huawei endpoint could not be resolved")
	}
	if !dialer.allowPrivateNetworks {
		for _, resolved := range addresses {
			if !isPublicProviderAddress(resolved) {
				return nil, errors.New("huawei endpoint resolved to a non-public network address")
			}
		}
	}
	for _, resolved := range addresses {
		connection, dialError := dialer.dialer.DialContext(
			ctx,
			network,
			net.JoinHostPort(resolved.String(), port),
		)
		if dialError == nil {
			return connection, nil
		}
	}
	return nil, errors.New("huawei endpoint is unavailable")
}

// isPublicProviderAddress reports whether an address is suitable for provider communication.
func isPublicProviderAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() ||
		address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() ||
		address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	if address.Is4() && (providerSharedAddressPrefix.Contains(address) || providerBenchmarkAddressPrefix.Contains(address)) {
		return false
	}
	return true
}
