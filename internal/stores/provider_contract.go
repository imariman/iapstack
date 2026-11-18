package stores

import (
	"fmt"

	"github.com/imariman/iapstack/internal/core"
)

const (
	// AppleEvidenceContentType identifies the v1 App Store signed transaction envelope.
	AppleEvidenceContentType = "application/vnd.iapstack.apple-transaction+json"
	// GooglePlayEvidenceContentType identifies the v1 Google Play purchase-token envelope.
	GooglePlayEvidenceContentType = "application/vnd.iapstack.google-play-purchase+json"
	// HuaweiEvidenceContentType identifies the v1 AppGallery signed purchase envelope.
	HuaweiEvidenceContentType = "application/vnd.iapstack.huawei-purchase+json"
)

// EvidenceContentType returns the versioned client evidence contract for a supported provider.
func EvidenceContentType(provider core.Provider) (string, error) {
	switch provider {
	case core.ProviderAppleAppStore:
		return AppleEvidenceContentType, nil
	case core.ProviderGooglePlay:
		return GooglePlayEvidenceContentType, nil
	case core.ProviderHuaweiAppGallery:
		return HuaweiEvidenceContentType, nil
	default:
		return "", fmt.Errorf("unsupported purchase provider %q", provider)
	}
}

// RecordsOfferPhase reports whether this server's adapter for provider maps a signed offer phase.
// Apple maps offerType and offerDiscountType from the signed transaction. Google Play maps the
// subscriptionsv2 line-item offerPhase union. Huawei is not mapped: the official InAppPurchaseData
// field table was not captured in a fixture, so trial evidence stays unavailable rather than guessed.
func RecordsOfferPhase(provider core.Provider) bool {
	switch provider {
	case core.ProviderAppleAppStore, core.ProviderGooglePlay:
		return true
	default:
		return false
	}
}
