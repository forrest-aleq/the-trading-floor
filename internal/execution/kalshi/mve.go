package kalshi

import "strings"

const unsafeAllowMVEWrappersEnv = "KALSHI_UNSAFE_ALLOW_MVE_WRAPPERS"

func IsMultivariateTicker(ticker string) bool {
	ticker = strings.ToUpper(strings.TrimSpace(ticker))
	return strings.HasPrefix(ticker, "KXMVE")
}

func ShouldBlockMultivariateTicker(ticker string) bool {
	return IsMultivariateTicker(ticker) && !UnsafeAllowMVEWrappers()
}

func UnsafeAllowMVEWrappers() bool {
	return readBoolEnv(unsafeAllowMVEWrappersEnv, false)
}
