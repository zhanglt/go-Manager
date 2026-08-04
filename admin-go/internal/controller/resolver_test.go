package controller

import (
	"net/url"
	"testing"
)

type clusterLookup map[string]string

func (c clusterLookup) Cluster(token string) (string, bool) {
	value, ok := c[token]
	return value, ok
}

func TestTargetResolver(t *testing.T) {
	baseURL, _ := url.Parse("https://controller:10443/v1")
	resolver := NewTargetResolver(baseURL, clusterLookup{"remote": "member/one"})
	for _, test := range []struct {
		token   string
		version APIVersion
		want    string
	}{
		{"local", V1, "https://controller:10443/v1/user_role"},
		{"local", V2, "https://controller:10443/v2/user_role"},
		{"remote", V1, "https://controller:10443/v1/fed/cluster/member%2Fone/v1/user_role"},
		{"remote", V2, "https://controller:10443/v1/fed/cluster/member%2Fone/v2/user_role"},
	} {
		target, err := resolver.Resolve(test.token, test.version, "user_role")
		if err != nil || target.String() != test.want {
			t.Errorf("Resolve(%q, %q) = %v, %v; want %q", test.token, test.version, target, err, test.want)
		}
	}
}

func TestTargetResolverEscapesResourceSegment(t *testing.T) {
	baseURL, _ := url.Parse("https://controller:10443/v1")
	target, err := NewTargetResolver(baseURL, clusterLookup{}).Resolve("token", V1, "user_role", "admin/name")
	if err != nil || target.String() != "https://controller:10443/v1/user_role/admin%2Fname" {
		t.Fatalf("target = %v, error = %v", target, err)
	}
}

func TestTargetResolverUsesExplicitTransactionCluster(t *testing.T) {
	baseURL, _ := url.Parse("https://controller:10443/v1")
	resolver := NewTargetResolver(baseURL, clusterLookup{"token": "current"})
	target, err := resolver.ResolveCluster("transaction-cluster", V2, "scan", "registry")
	if err != nil {
		t.Fatal(err)
	}
	if target.EscapedPath() != "/v1/fed/cluster/transaction-cluster/v2/scan/registry" {
		t.Fatalf("path = %q", target.EscapedPath())
	}
}
