package webassets

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

var indexReference = regexp.MustCompile(`(?:src|href)="([^"]+)"`)

func TestEmbeddedBuildIsComplete(t *testing.T) {
	root := Root()
	index, err := fs.ReadFile(root, "index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	for _, match := range indexReference.FindAllSubmatch(index, -1) {
		reference := string(match[1])
		if strings.Contains(reference, "://") || strings.HasPrefix(reference, "data:") {
			continue
		}
		name := strings.SplitN(strings.TrimPrefix(reference, "/"), "?", 2)[0]
		if _, err := fs.Stat(root, name); err != nil {
			t.Errorf("index reference %q is unavailable: %v", reference, err)
		}
	}

	err = fs.WalkDir(root, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasSuffix(name, ".js") {
			if _, err := fs.Stat(root, name+".gz"); err != nil {
				t.Errorf("production JavaScript %q has no gzip peer", name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded build: %v", err)
	}
}
