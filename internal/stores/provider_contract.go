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
