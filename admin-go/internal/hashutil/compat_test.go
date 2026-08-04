package hashutil

import (
	"os"
	"strings"
	"testing"
)

func TestCompatibilityHashLegacy(t *testing.T) {
	if strings.Contains(os.Getenv("GODEBUG"), "fips140=only") {
		t.Skip("the Go runtime permanently disables MD5 in FIPS-only mode")
	}
	t.Setenv("GODEBUG", "")
	if got := CompatibilityHash("test-version"); got != "a8b14b49cca6ee9a2dc6e28f87cc542c" {
		t.Fatalf("legacy hash = %q", got)
	}
}

func TestCompatibilityHashFIPSOnly(t *testing.T) {
	t.Setenv("GODEBUG", "http2client=0,fips140=off,fips140=only")
	if got := CompatibilityHash("test-version"); got != "330b4d3ecba793b3cc5ff904009073aae3fdebc8bc8f5c670bd3134312cdc7fe" {
		t.Fatalf("FIPS-only hash = %q", got)
	}
}
