package webhooks

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

// addressResolver resolves every IP address currently associated with one webhook hostname.
type addressResolver interface {
	// LookupNetIP returns matching IPv4 and IPv6 addresses for destination validation.
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

// connectionDialer opens one connection only after destination validation succeeds.
type connectionDialer interface {
	// DialContext opens a context-bound connection to one pinned network address.
	DialContext(context.Context, string, string) (net.Conn, error)
}

// restrictedDialer resolves and pins each webhook connection to an allowed destination address.
type restrictedDialer struct {
	allowPrivateNetworks bool
	resolver             addressResolver
	dialer               connectionDialer
}

var (
	// sharedAddressPrefix blocks carrier-grade NAT destinations that can expose provider infrastructure.
	sharedAddressPrefix = netip.MustParsePrefix("100.64.0.0/10")
	// benchmarkAddressPrefix blocks the IPv4 inter-network benchmark range from webhook delivery.
	benchmarkAddressPrefix = netip.MustParsePrefix("198.18.0.0/15")
)

// newWebhookClient constructs a redirect-free client with delivery-time destination enforcement.
func newWebhookClient(timeout time.Duration, allowPrivateNetworks bool) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	transport.DialContext = (&restrictedDialer{
		allowPrivateNetworks: allowPrivateNetworks,
		resolver:             net.DefaultResolver,
		dialer:               &net.Dialer{},
	}).DialContext
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// validateWebhookDestination accepts absolute HTTPS URLs and applies the configured literal-address policy.
func validateWebhookDestination(value string, allowPrivateNetworks bool) error {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("webhook destination must be an absolute HTTPS URL without user information or fragments")
	}
	hostname := parsed.Hostname()
	if hostname == "" || strings.Contains(hostname, "%") {
		return errors.New("webhook destination hostname is invalid")
	}
	if address, parseErr := netip.ParseAddr(hostname); parseErr == nil &&
		!allowPrivateNetworks && !isPublicWebhookAddress(address) {
		return errors.New("webhook destination must use a public network address")
	}
	return nil
}

// DialContext resolves all destination addresses, rejects mixed unsafe results, and dials a pinned address.
func (dialer *restrictedDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("webhook destination address is invalid")
	}
	addresses, err := dialer.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("webhook destination could not be resolved")
	}
	if !dialer.allowPrivateNetworks {
		for _, resolved := range addresses {
			if !isPublicWebhookAddress(resolved) {
				return nil, errors.New("webhook destination resolved to a non-public network address")
			}
		}
	}
	for _, resolved := range addresses {
		connection, dialErr := dialer.dialer.DialContext(ctx, network, net.JoinHostPort(resolved.String(), port))
		if dialErr == nil {
			return connection, nil
		}
	}
	return nil, errors.New("webhook destination is unavailable")
}

// isPublicWebhookAddress reports whether an address is suitable for default Internet-only delivery.
func isPublicWebhookAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() ||
		address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() ||
		address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	if address.Is4() && (sharedAddressPrefix.Contains(address) || benchmarkAddressPrefix.Contains(address)) {
		return false
	}
	return true
}
