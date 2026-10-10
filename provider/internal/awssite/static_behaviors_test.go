package awssite

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func TestStaticRootBehaviors(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"robots.txt", "robots.txt.br", "robots.txt.gz", "favicon.png", "bad name.txt"} {
		os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644)
	}
	for _, d := range []string{"_app", "compliance-demo", ".well-known"} {
		os.MkdirAll(filepath.Join(dir, d), 0o755)
	}

	behaviors, skipped, err := StaticRootBehaviors(dir, []string{"_app"}, "web-s3")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, b := range behaviors {
		m := b.(pulumi.Map)
		got[string(m["pathPattern"].(pulumi.String))] = true
		if m["targetOriginId"].(pulumi.String) != "web-s3" {
			t.Errorf("wrong origin: %v", m["targetOriginId"])
		}
	}
	for _, want := range []string{"/robots.txt", "/favicon.png", "/compliance-demo/*", "/.well-known/*"} {
		if !got[want] {
			t.Errorf("missing behavior %s (got %v)", want, got)
		}
	}
	for _, unwanted := range []string{"/_app/*", "/robots.txt.br", "/robots.txt.gz"} {
		if got[unwanted] {
			t.Errorf("unexpected behavior %s", unwanted)
		}
	}
	if len(skipped) != 1 || skipped[0] != "bad name.txt" {
		t.Errorf("skipped = %v", skipped)
	}
}

func TestStaticRootBehaviorsCap(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < maxStaticRootBehaviors+3; i++ {
		os.WriteFile(filepath.Join(dir, "f"+string(rune('a'+i))+".txt"), []byte("x"), 0o644)
	}
	behaviors, skipped, _ := StaticRootBehaviors(dir, nil, "s3")
	if len(behaviors) != maxStaticRootBehaviors || len(skipped) != 3 {
		t.Errorf("behaviors=%d skipped=%d", len(behaviors), len(skipped))
	}
}

func TestStaticRootBehaviorsMissingDir(t *testing.T) {
	b, s, err := StaticRootBehaviors(filepath.Join(t.TempDir(), "nope"), nil, "s3")
	if err != nil || b != nil || s != nil {
		t.Errorf("got %v %v %v", b, s, err)
	}
}
