package cmd

import (
	"regexp"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
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

func tagMap(tags []s3types.Tag) map[string]string {
	m := map[string]string{}
	for _, t := range tags {
		m[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	return m
}

func TestMergeTags_KeepsUserTags(t *testing.T) {
	existing := []s3types.Tag{{Key: aws.String("team"), Value: aws.String("platform")}}
	merged, changed := mergeTags(existing, stateBucketTags("testwaf", "damienpace"))
	if !changed {
		t.Fatal("expected a change when adding tags")
	}
	got := tagMap(merged)
	if got["team"] != "platform" {
		t.Errorf("user tag dropped: %v", got)
	}
	if got["ManagedBy"] != "anvil" || got["project"] != "testwaf" || got["stage"] != "damienpace" {
		t.Errorf("missing Anvil tags: %v", got)
	}
}

func TestMergeTags_NoChangeWhenPresent(t *testing.T) {
	want := map[string]string{"ManagedBy": "anvil", "Component": "LoggingBucket"}
	existing := []s3types.Tag{
		{Key: aws.String("Component"), Value: aws.String("LoggingBucket")},
		{Key: aws.String("ManagedBy"), Value: aws.String("anvil")},
	}
	if _, changed := mergeTags(existing, want); changed {
		t.Error("expected no change when all tags already match")
	}
}

func TestMergeTags_OverwritesStaleValue(t *testing.T) {
	existing := []s3types.Tag{{Key: aws.String("stage"), Value: aws.String("old")}}
	merged, changed := mergeTags(existing, map[string]string{"stage": "new"})
	if !changed || tagMap(merged)["stage"] != "new" {
		t.Errorf("expected stage overwritten, got %v (changed=%v)", tagMap(merged), changed)
	}
}
