package scanreport

import (
	"encoding/json"
	"testing"
)

func TestRequestPreservesOptionalValues(t *testing.T) {
	input := []byte(`{"show_accepted":false,"max_cve_records":0,"cursor":{"name":"workload-one","host_name":null},"view_pod":"pod","vul_score_filter":{"score_version":"v3","score_bottom":4,"score_top":null},"filters":[],"severity_filter":"high"}`)
	var request Request
	if err := json.Unmarshal(input, &request); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"cursor":{"name":"workload-one"},"filters":[],"max_cve_records":0,"severity_filter":"high","show_accepted":false,"view_pod":"pod","vul_score_filter":{"score_bottom":4,"score_version":"v3"}}`
	if string(encoded) != want {
		t.Fatalf("encoded request = %s, want %s", encoded, want)
	}
}

func TestEmptyRequestOmitsAllFields(t *testing.T) {
	var request Request
	if err := json.Unmarshal([]byte(`{}`), &request); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{}` {
		t.Fatalf("encoded request = %s", encoded)
	}
}
