package hashutil

import (
	"crypto/md5" //nolint:gosec // Required for the non-FIPS legacy API contract.
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
)

// CompatibilityHash preserves the legacy MD5 value unless Go is running in
// FIPS-only mode, where invoking MD5 would terminate the request or process.
func CompatibilityHash(value string) string {
	if fipsOnly() {
		digest := sha256.Sum256([]byte(value))
		return hex.EncodeToString(digest[:])
	}
	digest := md5.Sum([]byte(value)) //nolint:gosec // Public cache/Gravatar identifier only.
	return hex.EncodeToString(digest[:])
}

func fipsOnly() bool {
	mode := ""
	for _, setting := range strings.Split(os.Getenv("GODEBUG"), ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(setting), "=")
		if ok && key == "fips140" {
			mode = value
		}
	}
	return mode == "only"
}
