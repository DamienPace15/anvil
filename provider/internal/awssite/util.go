// Package awssite provides shared AWS infrastructure helpers for site deployments.
// It is consumed by provider-specific site components (aws/sveltekitsite, aws/astrosite, etc)
// and is not intended for use by standalone resource components (aws/lambda, aws/bucket).
package awssite

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// SanitiseResourceName converts a file path or arbitrary string into a valid
// Pulumi resource name segment (lowercase, hyphens only, no consecutive hyphens).
func SanitiseResourceName(s string) string {
	result := strings.NewReplacer("/", "-", ".", "-", " ", "-", "_", "-").Replace(s)
	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}
	return strings.Trim(result, "-")
}

// CopyDir recursively copies src into dst, preserving file modes.
func CopyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relPath, _ := filepath.Rel(src, path)
		destPath := filepath.Join(dst, relPath)
		if info.IsDir() {
			return os.MkdirAll(destPath, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destPath, data, info.Mode())
	})
}

// ExtractZoneName returns the apex domain with a trailing dot, suitable for
// Route53 hosted zone lookups (e.g. "sub.example.com" → "example.com.").
func ExtractZoneName(domain string) string {
	parts := strings.Split(domain, ".")
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], ".") + "."
	}
	return domain + "."
}

// CoerceToStringOutput converts an interface{} into a pulumi.Input suitable
// for use in a Lambda environment variable map.
// Handles: string, pulumi.StringOutput, pulumi.StringInput, pulumi.Output.
func CoerceToStringOutput(v interface{}) pulumi.Input {
	switch val := v.(type) {
	case string:
		return pulumi.String(val)
	case pulumi.StringOutput:
		return val
	case pulumi.StringInput:
		return val
	case pulumi.Output:
		return val.ApplyT(func(raw interface{}) string {
			return fmt.Sprintf("%v", raw)
		}).(pulumi.StringOutput)
	default:
		return pulumi.Sprintf("%v", val)
	}
}

// CreateUSEast1Provider returns an AWS provider pinned to us-east-1,
// required for ACM certificates used with CloudFront.
func CreateUSEast1Provider(ctx *pulumi.Context, name string, parent pulumi.Resource) (*aws.Provider, error) {
	return aws.NewProvider(ctx, name+"-us-east-1", &aws.ProviderArgs{
		Region: pulumi.String("us-east-1"),
	}, pulumi.Parent(parent))
}
