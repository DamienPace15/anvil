package cmd

import (
	"regexp"
	"strings"
	"testing"
)

var validBucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)

func TestResolveBucketName_UnchangedWhenValid(t *testing.T) {
	got := resolveBucketName("damienpace", "test-auth0", "085a65")
	if want := "damienpace-anvil-state-test-auth0-085a65"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveBucketName_Lowercases(t *testing.T) {
	got := resolveBucketName("damienpace", "testAuth0", "085a65")
	if want := "damienpace-anvil-state-testauth0-085a65"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveBucketName_ReplacesInvalidChars(t *testing.T) {
	got := resolveBucketName("dev", "my_app.v2", "abc123")
	if !validBucketName.MatchString(got) {
		t.Errorf("%q is not a valid bucket name", got)
	}
}

func TestResolveBucketName_TruncatesKeepingId(t *testing.T) {
	got := resolveBucketName("a-very-long-stage-name", strings.Repeat("project", 8), "085a65")
	if len(got) > 63 {
		t.Errorf("%q is %d chars, max 63", got, len(got))
	}
	if !strings.HasSuffix(got, "-085a65") {
		t.Errorf("%q lost its unique id", got)
	}
	if !validBucketName.MatchString(got) {
		t.Errorf("%q is not a valid bucket name", got)
	}
}

func TestTrailEventSelectors_ManagementOnlyByDefault(t *testing.T) {
	sel := trailEventSelectors(false)
	if len(sel) != 1 || !*sel[0].IncludeManagementEvents {
		t.Fatalf("expected one selector with management events, got %+v", sel)
	}
	if len(sel[0].DataResources) != 0 {
		t.Errorf("S3 data events should be off by default, got %+v", sel[0].DataResources)
	}
}

func TestTrailEventSelectors_S3OptIn(t *testing.T) {
	sel := trailEventSelectors(true)
	if len(sel[0].DataResources) != 1 || *sel[0].DataResources[0].Type != "AWS::S3::Object" {
		t.Errorf("expected S3 object data events, got %+v", sel[0].DataResources)
	}
}
