package transfer

import (
	"net/http"
	"testing"
)

func TestExtractLegacyForm(t *testing.T) {
	input := "--boundary\nDisposition\nType\n\nline-one\nline-two\n--boundary--\n"
	if got := ExtractLegacyForm(input); got != "line-one\nline-two" {
		t.Fatalf("ExtractLegacyForm() = %q", got)
	}
	if got := ExtractLegacyForm("short\nbody"); got != "" {
		t.Fatalf("short form = %q", got)
	}
}

func TestRequestHeaderPreservesEmptyHeader(t *testing.T) {
	header := make(http.Header)
	header["X-Transaction-Id"] = []string{""}
	value, present := RequestHeader(header, "X-Transaction-Id")
	if !present || value != "" {
		t.Fatalf("header value=%q present=%v", value, present)
	}
}
