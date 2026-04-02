package awssite

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/s3"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// UploadSiteAssets walks staticDir and uploads every file to the given S3 bucket.
// Cache headers are set per file type:
//   - Immutable assets (/_app/immutable/*, /_astro/*, etc): max-age=31536000, immutable
//   - Everything else: s-maxage=86400 with stale-while-revalidate
//
// The immutablePrefixes param lets each framework declare which path prefixes
// contain content-hashed assets eligible for long-term caching.
func UploadSiteAssets(ctx *pulumi.Context, parent pulumi.Resource, name string, bucket *s3.Bucket, staticDir string, immutablePrefixes []string) error {
	return filepath.Walk(staticDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		relPath, _ := filepath.Rel(staticDir, path)
		key := filepath.ToSlash(relPath)

		contentType := mime.TypeByExtension(filepath.Ext(path))
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		cacheControl := "public, max-age=0, s-maxage=86400, stale-while-revalidate=8640"
		for _, prefix := range immutablePrefixes {
			if strings.HasPrefix(key, prefix) {
				cacheControl = "public, max-age=31536000, immutable"
				break
			}
		}

		// Hash the content to produce a stable, unique resource name.
		content, _ := os.ReadFile(path)
		hash := sha256.Sum256(content)
		hashStr := hex.EncodeToString(hash[:8])
		resourceName := fmt.Sprintf("%s-asset-%s-%s", name, SanitiseResourceName(key), hashStr)

		return ctx.RegisterResource("aws:s3/bucketObjectv2:BucketObjectv2", resourceName, pulumi.Map{
			"bucket":       bucket.Bucket,
			"key":          pulumi.String(key),
			"source":       pulumi.NewFileAsset(path),
			"contentType":  pulumi.String(contentType),
			"cacheControl": pulumi.String(cacheControl),
		}, &s3.BucketObjectv2{}, pulumi.Parent(parent))
	})
}
