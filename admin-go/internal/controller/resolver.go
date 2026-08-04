package controller

import (
	"fmt"
	"net/url"
	"strings"
)

type APIVersion string

const (
	V1 APIVersion = "v1"
	V2 APIVersion = "v2"
)

type ClusterLookup interface {
	Cluster(token string) (string, bool)
}

type TargetResolver struct {
	baseURL  *url.URL
	clusters ClusterLookup
}

func NewTargetResolver(baseURL *url.URL, clusters ClusterLookup) *TargetResolver {
	return &TargetResolver{baseURL: cloneURL(baseURL), clusters: clusters}
}

func (r *TargetResolver) Resolve(token string, version APIVersion, resourceSegments ...string) (*url.URL, error) {
	return r.resolve(token, true, version, resourceSegments...)
}

// ResolveLocal always targets the local Controller, regardless of a switched federation session.
func (r *TargetResolver) ResolveLocal(version APIVersion, resourceSegments ...string) (*url.URL, error) {
	return r.resolve("", false, version, resourceSegments...)
}

func (r *TargetResolver) ResolveCluster(clusterID string, version APIVersion, resourceSegments ...string) (*url.URL, error) {
	return r.resolveCluster(clusterID, version, resourceSegments...)
}

func (r *TargetResolver) resolve(token string, clusterAware bool, version APIVersion, resourceSegments ...string) (*url.URL, error) {
	clusterID := ""
	if clusterAware {
		clusterID, _ = r.clusters.Cluster(token)
	}
	return r.resolveCluster(clusterID, version, resourceSegments...)
}

func (r *TargetResolver) resolveCluster(clusterID string, version APIVersion, resourceSegments ...string) (*url.URL, error) {
	if version != V1 && version != V2 {
		return nil, fmt.Errorf("unsupported Controller API version %q", version)
	}
	segments := []string{string(version)}
	if clusterID != "" {
		segments = []string{"v1", "fed", "cluster", clusterID, string(version)}
	}
	segments = append(segments, resourceSegments...)
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return nil, fmt.Errorf("invalid empty or dot Controller path segment")
		}
	}

	target := cloneURL(r.baseURL)
	target.Path = "/" + strings.Join(segments, "/")
	escaped := make([]string, len(segments))
	for index, segment := range segments {
		escaped[index] = url.PathEscape(segment)
	}
	target.RawPath = "/" + strings.Join(escaped, "/")
	target.RawQuery = ""
	target.Fragment = ""
	return target, nil
}
