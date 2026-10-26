package financialreports

import "strings"

var (
	// currencyExponentSpecial lists ISO 4217 currencies whose minor-unit exponent is not 2.
	currencyExponentSpecial = map[string]int32{
		"BIF": 0, "CLP": 0, "DJF": 0, "GNF": 0, "ISK": 0, "JPY": 0, "KMF": 0,
		"KRW": 0, "PYG": 0, "RWF": 0, "UGX": 0, "VND": 0, "VUV": 0, "XAF": 0,
		"XOF": 0, "XPF": 0,
		"BHD": 3, "IQD": 3, "JOD": 3, "KWD": 3, "LYD": 3, "OMR": 3, "TND": 3,
		"CLF": 4, "UYW": 4,
	}
	// currencyExponentTwo lists checked-in ISO 4217 currencies with exponent 2.
	currencyExponentTwo = currencySet(
		"AED AFN ALL AMD ANG AOA ARS AUD AWG AZN BAM BBD BDT BGN BMD BND BOB BRL BSD " +
			"BTN BWP BYN BZD CAD CDF CHF CNY COP CRC CUP CVE CZK DOP DZD EGP ERN ETB EUR " +
			"FJD FKP GBP GEL GHS GIP GMD GTQ GYD HKD HNL HTG HUF IDR ILS INR IRR JMD KES " +
			"KGS KHR KYD KZT LAK LBP LKR LRD LSL MAD MDL MGA MKD MMK MNT MOP MRU MUR MVR " +
			"MWK MXN MZN NAD NGN NIO NOK NPR NZD PAB PEN PGK PHP PKR PLN QAR RON RSD RUB " +
			"SAR SBD SCR SDG SEK SGD SHP SLE SOS SRD STN SVC SYP SZL THB TJS TMT TOP TRY " +
			"TTD TWD TZS UAH USD UYU UZS VES WST XCD YER ZAR ZMW ZWL",
	)
)

// CurrencyExponent returns the checked-in ISO 4217 minor-unit exponent.
func CurrencyExponent(code string) (int32, bool) {
	if exponent, ok := currencyExponentSpecial[code]; ok {
		return exponent, true
	}
	if _, ok := currencyExponentTwo[code]; ok {
		return 2, true
	}
	return 0, false
}

// ValidCurrencyCode reports whether code is three ASCII letters.
func ValidCurrencyCode(code string) bool {
	if len(code) != 3 {
		return false
	}
	for _, character := range code {
		if character < 'A' || character > 'Z' {
			return false
		}
	}
	return true
}

// currencySet splits a space-separated currency list into a lookup set.
func currencySet(list string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, code := range strings.Fields(list) {
		result[code] = struct{}{}
	}
	return result
}
