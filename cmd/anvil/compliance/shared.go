package compliance

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	logstypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	schedtypes "github.com/aws/aws-sdk-go-v2/service/scheduler/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

const (
	// VersionTag on the CodeBuild project records which CLI last converged the
	// shared layer.
	VersionTag = "anvil:compliance-version"

	ownerTagKey   = "Component"
	ownerTagValue = "Compliance"

	buildTimeoutMinutes  = 30
	concurrentBuilds     = 3
	logRetentionDays     = 30
	iamPropagationWindow = 90 * time.Second
)

// ownershipTags go on every shared resource. Teardown only deletes resources
// that carry them.
func ownershipTags() map[string]string {
	return map[string]string{"ManagedBy": "anvil", ownerTagKey: ownerTagValue}
}

// Logger receives progress lines.
type Logger struct {
	Check func(string)
	Warn  func(string)
}

// Shared manages the per-account, per-region compliance layer.
type Shared struct {
	Names Names

	s3    *s3.Client
	iam   *iam.Client
	logs  *cloudwatchlogs.Client
	cb    *codebuild.Client
	sched *scheduler.Client
	log   Logger
}

// New resolves the account from the caller's credentials. cfg.Region decides
// which region's shared layer is managed.
func New(ctx context.Context, cfg aws.Config, log Logger) (*Shared, error) {
	if cfg.Region == "" {
		return nil, fmt.Errorf("no AWS region configured")
	}
	id, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("AWS credentials not found or expired: %w", err)
	}
	return &Shared{
		Names: Names{Account: aws.ToString(id.Account), Region: cfg.Region},
		s3:    s3.NewFromConfig(cfg),
		iam:   iam.NewFromConfig(cfg),
		logs:  cloudwatchlogs.NewFromConfig(cfg),
		cb:    codebuild.NewFromConfig(cfg),
		sched: scheduler.NewFromConfig(cfg),
		log:   log,
	}, nil
}

// ── Status ──────────────────────────────────────────────────

// State is what's currently deployed.
type State struct {
	Exists  bool
	Version string // value of VersionTag, "" if missing
}

func (s *Shared) Status(ctx context.Context) (State, error) {
	out, err := s.cb.BatchGetProjects(ctx, &codebuild.BatchGetProjectsInput{Names: []string{s.Names.Project()}})
	if err != nil {
		return State{}, fmt.Errorf("reading CodeBuild project: %w", err)
	}
	if len(out.Projects) == 0 {
		return State{}, nil
	}
	st := State{Exists: true}
	for _, t := range out.Projects[0].Tags {
		if aws.ToString(t.Key) == VersionTag {
			st.Version = aws.ToString(t.Value)
		}
	}
	return st, nil
}

// NeedsConverge reports whether the CLI at cliVersion should (re)apply the
// shared layer. Dev builds always converge; release builds converge when the
// layer is missing or was last applied by an older CLI, and never downgrade.
func NeedsConverge(st State, cliVersion string) bool {
	if !st.Exists || st.Version == "" {
		return true
	}
	if cliVersion == "dev" || st.Version == "dev" {
		return cliVersion == "dev"
	}
	return compareVersions(cliVersion, st.Version) > 0
}

// compareVersions compares dotted numeric versions ("1.4.0", "v1.4"); any
// non-numeric suffix is ignored.
func compareVersions(a, b string) int {
	pa, pb := versionParts(a), versionParts(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func versionParts(v string) []int {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var parts []int
	for _, p := range strings.Split(v, ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			break
		}
		parts = append(parts, n)
	}
	return parts
}

// ── Ensure ──────────────────────────────────────────────────

// Ensure creates or updates every shared resource. Each step is idempotent:
// "already exists" is success, then the desired settings are applied, which
// also corrects drift.
func (s *Shared) Ensure(ctx context.Context, cliVersion string) error {
	if err := s.ensureBucket(ctx); err != nil {
		return fmt.Errorf("results bucket: %w", err)
	}
	s.log.Check("Results bucket " + s.Names.Bucket())

	if err := s.ensureLogGroup(ctx); err != nil {
		return fmt.Errorf("log group: %w", err)
	}
	s.log.Check("Log group " + s.Names.LogGroup())

	if err := s.ensureRole(ctx, s.Names.ScanRole(), "Anvil compliance scanner (read-only)",
		scanTrustPolicy(s.Names), scanManagedPolicies, "anvil-compliance-scan", scanInlinePolicy(s.Names)); err != nil {
		return fmt.Errorf("scan role: %w", err)
	}
	s.log.Check("Scan role " + s.Names.ScanRole())

	if err := s.ensureRole(ctx, s.Names.InvokeRole(), "Lets Anvil compliance schedules start scans",
		invokeTrustPolicy(s.Names), nil, "anvil-compliance-invoke", invokeInlinePolicy(s.Names)); err != nil {
		return fmt.Errorf("invoke role: %w", err)
	}
	s.log.Check("Invoke role " + s.Names.InvokeRole())

	if err := s.ensureProject(ctx, cliVersion); err != nil {
		return fmt.Errorf("CodeBuild project: %w", err)
	}
	s.log.Check("CodeBuild project " + s.Names.Project())

	if err := s.ensureScheduleGroup(ctx); err != nil {
		return fmt.Errorf("schedule group: %w", err)
	}
	s.log.Check("Schedule group " + s.Names.ScheduleGroup())
	return nil
}

func (s *Shared) ensureBucket(ctx context.Context) error {
	bucket := aws.String(s.Names.Bucket())
	owner := aws.String(s.Names.Account)

	_, err := s.s3.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: bucket, ExpectedBucketOwner: owner})
	switch {
	case err == nil:
	case httpStatus(err) == 404:
		in := &s3.CreateBucketInput{Bucket: bucket, ObjectOwnership: s3types.ObjectOwnershipBucketOwnerEnforced}
		if s.Names.Region != "us-east-1" {
			in.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{
				LocationConstraint: s3types.BucketLocationConstraint(s.Names.Region),
			}
		}
		if _, err := s.s3.CreateBucket(ctx, in); err != nil {
			var owned *s3types.BucketAlreadyOwnedByYou
			var taken *s3types.BucketAlreadyExists
			switch {
			case errors.As(err, &owned):
			case errors.As(err, &taken):
				return squatted(s.Names.Bucket())
			default:
				return err
			}
		}
	case httpStatus(err) == 403:
		// ExpectedBucketOwner mismatch: the name exists in another account.
		return squatted(s.Names.Bucket())
	default:
		return err
	}

	steps := []func() error{
		func() error {
			_, err := s.s3.PutPublicAccessBlock(ctx, &s3.PutPublicAccessBlockInput{
				Bucket: bucket, ExpectedBucketOwner: owner,
				PublicAccessBlockConfiguration: &s3types.PublicAccessBlockConfiguration{
					BlockPublicAcls: aws.Bool(true), BlockPublicPolicy: aws.Bool(true),
					IgnorePublicAcls: aws.Bool(true), RestrictPublicBuckets: aws.Bool(true),
				},
			})
			return err
		},
		func() error {
			_, err := s.s3.PutBucketOwnershipControls(ctx, &s3.PutBucketOwnershipControlsInput{
				Bucket: bucket, ExpectedBucketOwner: owner,
				OwnershipControls: &s3types.OwnershipControls{Rules: []s3types.OwnershipControlsRule{
					{ObjectOwnership: s3types.ObjectOwnershipBucketOwnerEnforced},
				}},
			})
			return err
		},
		func() error {
			// SSE-KMS with the AWS-managed key: free, and Bucket Keys keep the
			// request cost negligible.
			_, err := s.s3.PutBucketEncryption(ctx, &s3.PutBucketEncryptionInput{
				Bucket: bucket, ExpectedBucketOwner: owner,
				ServerSideEncryptionConfiguration: &s3types.ServerSideEncryptionConfiguration{
					Rules: []s3types.ServerSideEncryptionRule{{
						ApplyServerSideEncryptionByDefault: &s3types.ServerSideEncryptionByDefault{
							SSEAlgorithm: s3types.ServerSideEncryptionAwsKms,
						},
						BucketKeyEnabled: aws.Bool(true),
					}},
				},
			})
			return err
		},
		func() error {
			_, err := s.s3.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
				Bucket: bucket, ExpectedBucketOwner: owner,
				VersioningConfiguration: &s3types.VersioningConfiguration{Status: s3types.BucketVersioningStatusEnabled},
			})
			return err
		},
		func() error {
			_, err := s.s3.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{
				Bucket: bucket, ExpectedBucketOwner: owner, Policy: aws.String(bucketPolicy(s.Names)),
			})
			return err
		},
		func() error {
			_, err := s.s3.PutBucketLifecycleConfiguration(ctx, &s3.PutBucketLifecycleConfigurationInput{
				Bucket: bucket, ExpectedBucketOwner: owner,
				LifecycleConfiguration: &s3types.BucketLifecycleConfiguration{Rules: lifecycleRules()},
			})
			return err
		},
		func() error {
			_, err := s.s3.PutBucketTagging(ctx, &s3.PutBucketTaggingInput{
				Bucket: bucket, ExpectedBucketOwner: owner,
				Tagging: &s3types.Tagging{TagSet: s3Tags(ownershipTags())},
			})
			return err
		},
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

// lifecycleRules expire results by their retention object tag. Versioning is on,
// so each rule also removes noncurrent versions; otherwise expiry would only add
// a delete marker and nothing would actually be deleted.
func lifecycleRules() []s3types.LifecycleRule {
	var rules []s3types.LifecycleRule
	for _, tier := range RetentionTiers {
		rules = append(rules, s3types.LifecycleRule{
			ID:                          aws.String("retention-" + tier),
			Status:                      s3types.ExpirationStatusEnabled,
			Filter:                      &s3types.LifecycleRuleFilter{Tag: &s3types.Tag{Key: aws.String("retention"), Value: aws.String(tier)}},
			Expiration:                  &s3types.LifecycleExpiration{Days: aws.Int32(retentionDays[tier])},
			NoncurrentVersionExpiration: &s3types.NoncurrentVersionExpiration{NoncurrentDays: aws.Int32(1)},
		})
	}
	// Untagged objects (latest.json pointers) never expire, but their
	// overwritten versions do; also clear delete markers and stale uploads.
	rules = append(rules, s3types.LifecycleRule{
		ID:                             aws.String("cleanup"),
		Status:                         s3types.ExpirationStatusEnabled,
		Filter:                         &s3types.LifecycleRuleFilter{Prefix: aws.String("")},
		Expiration:                     &s3types.LifecycleExpiration{ExpiredObjectDeleteMarker: aws.Bool(true)},
		NoncurrentVersionExpiration:    &s3types.NoncurrentVersionExpiration{NoncurrentDays: aws.Int32(30)},
		AbortIncompleteMultipartUpload: &s3types.AbortIncompleteMultipartUpload{DaysAfterInitiation: aws.Int32(1)},
	})
	return rules
}

func (s *Shared) ensureLogGroup(ctx context.Context) error {
	_, err := s.logs.CreateLogGroup(ctx, &cloudwatchlogs.CreateLogGroupInput{
		LogGroupName: aws.String(s.Names.LogGroup()),
		Tags:         ownershipTags(),
	})
	var exists *logstypes.ResourceAlreadyExistsException
	if err != nil && !errors.As(err, &exists) {
		return err
	}
	if _, err := s.logs.PutRetentionPolicy(ctx, &cloudwatchlogs.PutRetentionPolicyInput{
		LogGroupName:    aws.String(s.Names.LogGroup()),
		RetentionInDays: aws.Int32(logRetentionDays),
	}); err != nil {
		return err
	}
	_, err = s.logs.TagResource(ctx, &cloudwatchlogs.TagResourceInput{
		ResourceArn: aws.String(s.Names.LogGroupArn()),
		Tags:        ownershipTags(),
	})
	return err
}

func (s *Shared) ensureRole(ctx context.Context, name, description, trust string, managed []string, inlineName, inline string) error {
	got, err := s.iam.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(name)})
	var missing *iamtypes.NoSuchEntityException
	switch {
	case errors.As(err, &missing):
		if _, err := s.iam.CreateRole(ctx, &iam.CreateRoleInput{
			RoleName:                 aws.String(name),
			Description:              aws.String(description),
			AssumeRolePolicyDocument: aws.String(trust),
			Tags:                     iamTags(ownershipTags()),
		}); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		if !ownedIAM(got.Role.Tags) {
			return fmt.Errorf("role %s already exists and was not created by Anvil; remove or rename it", name)
		}
		if _, err := s.iam.UpdateAssumeRolePolicy(ctx, &iam.UpdateAssumeRolePolicyInput{
			RoleName: aws.String(name), PolicyDocument: aws.String(trust),
		}); err != nil {
			return err
		}
		if _, err := s.iam.TagRole(ctx, &iam.TagRoleInput{RoleName: aws.String(name), Tags: iamTags(ownershipTags())}); err != nil {
			return err
		}
	}

	for _, arn := range managed {
		if _, err := s.iam.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
			RoleName: aws.String(name), PolicyArn: aws.String(arn),
		}); err != nil {
			return err
		}
	}
	_, err = s.iam.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
		RoleName: aws.String(name), PolicyName: aws.String(inlineName), PolicyDocument: aws.String(inline),
	})
	return err
}

func (s *Shared) ensureProject(ctx context.Context, cliVersion string) error {
	buildspec, err := Buildspec()
	if err != nil {
		return err
	}

	tags := ownershipTags()
	tags[VersionTag] = cliVersion

	source := &cbtypes.ProjectSource{Type: cbtypes.SourceTypeNoSource, Buildspec: aws.String(buildspec)}
	artifacts := &cbtypes.ProjectArtifacts{Type: cbtypes.ArtifactsTypeNoArtifacts}
	env := &cbtypes.ProjectEnvironment{
		Type:                     cbtypes.EnvironmentTypeArmContainer,
		ComputeType:              cbtypes.ComputeTypeBuildGeneral1Small,
		Image:                    aws.String(ProwlerImage),
		ImagePullCredentialsType: cbtypes.ImagePullCredentialsTypeCodebuild,
		EnvironmentVariables: []cbtypes.EnvironmentVariable{
			{Name: aws.String("ANVIL_RESULTS_BUCKET"), Value: aws.String(s.Names.Bucket()), Type: cbtypes.EnvironmentVariableTypePlaintext},
		},
	}
	logs := &cbtypes.LogsConfig{CloudWatchLogs: &cbtypes.CloudWatchLogsConfig{
		Status: cbtypes.LogsConfigStatusTypeEnabled, GroupName: aws.String(s.Names.LogGroup()),
	}}
	description := aws.String(fmt.Sprintf("Anvil compliance scanner (Prowler %s)", ProwlerVersion))

	existing, err := s.cb.BatchGetProjects(ctx, &codebuild.BatchGetProjectsInput{Names: []string{s.Names.Project()}})
	if err != nil {
		return err
	}
	exists := len(existing.Projects) > 0
	if exists && !ownedCodeBuild(existing.Projects[0].Tags) {
		return fmt.Errorf("project %s already exists and was not created by Anvil", s.Names.Project())
	}

	limit := aws.Int32(concurrentBuilds)
	apply := func() error {
		if exists {
			_, err := s.cb.UpdateProject(ctx, &codebuild.UpdateProjectInput{
				Name: aws.String(s.Names.Project()), Description: description,
				Source: source, Artifacts: artifacts, Environment: env, LogsConfig: logs,
				ServiceRole:          aws.String(s.Names.ScanRoleArn()),
				TimeoutInMinutes:     aws.Int32(buildTimeoutMinutes),
				ConcurrentBuildLimit: limit,
				Tags:                 codebuildTags(tags),
			})
			return err
		}
		_, err := s.cb.CreateProject(ctx, &codebuild.CreateProjectInput{
			Name: aws.String(s.Names.Project()), Description: description,
			Source: source, Artifacts: artifacts, Environment: env, LogsConfig: logs,
			ServiceRole:          aws.String(s.Names.ScanRoleArn()),
			TimeoutInMinutes:     aws.Int32(buildTimeoutMinutes),
			ConcurrentBuildLimit: limit,
			Tags:                 codebuildTags(tags),
		})
		return err
	}

	// A freshly created role takes a few seconds to become assumable, and new
	// accounts can have an account-wide build limit below concurrentBuilds.
	deadline := time.Now().Add(iamPropagationWindow)
	for {
		err := apply()
		if err == nil {
			return nil
		}
		msg := strings.ToLower(err.Error())
		switch {
		case strings.Contains(msg, "concurrent") && strings.Contains(msg, "limit") && limit != nil:
			s.log.Warn("Account CodeBuild concurrency is below 3; scans will run one at a time")
			limit = nil
		case strings.Contains(msg, "sts:assumerole") && time.Now().Before(deadline):
			time.Sleep(5 * time.Second)
		default:
			return err
		}
	}
}

func (s *Shared) ensureScheduleGroup(ctx context.Context) error {
	_, err := s.sched.CreateScheduleGroup(ctx, &scheduler.CreateScheduleGroupInput{
		Name: aws.String(s.Names.ScheduleGroup()),
		Tags: schedulerTags(ownershipTags()),
	})
	var conflict *schedtypes.ConflictException
	if err != nil && !errors.As(err, &conflict) {
		return err
	}
	return nil
}

// ── Teardown ────────────────────────────────────────────────

// RegisteredApps lists the schedules in the shared group: one per app with
// compliance enabled.
func (s *Shared) RegisteredApps(ctx context.Context) ([]string, error) {
	var names []string
	p := scheduler.NewListSchedulesPaginator(s.sched, &scheduler.ListSchedulesInput{GroupName: aws.String(s.Names.ScheduleGroup())})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		var missing *schedtypes.ResourceNotFoundException
		if errors.As(err, &missing) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		for _, sch := range page.Schedules {
			names = append(names, aws.ToString(sch.Name))
		}
	}
	return names, nil
}

// Teardown deletes the shared layer by name, skipping (with a warning) anything
// that doesn't carry Anvil's ownership tags. The results bucket is kept unless
// deleteResults is set.
func (s *Shared) Teardown(ctx context.Context, deleteResults bool) error {
	apps, err := s.RegisteredApps(ctx)
	if err != nil {
		return err
	}
	if len(apps) > 0 {
		return fmt.Errorf("%d app(s) still have compliance enabled (%s); remove `compliance` from them and deploy first",
			len(apps), strings.Join(apps, ", "))
	}

	if err := s.deleteProject(ctx); err != nil {
		return fmt.Errorf("CodeBuild project: %w", err)
	}
	if err := s.deleteScheduleGroup(ctx); err != nil {
		return fmt.Errorf("schedule group: %w", err)
	}
	for _, role := range []string{s.Names.ScanRole(), s.Names.InvokeRole()} {
		if err := s.deleteRole(ctx, role); err != nil {
			return fmt.Errorf("role %s: %w", role, err)
		}
	}
	if err := s.deleteLogGroup(ctx); err != nil {
		return fmt.Errorf("log group: %w", err)
	}
	if deleteResults {
		if err := s.deleteBucket(ctx); err != nil {
			return fmt.Errorf("results bucket: %w", err)
		}
	} else {
		s.log.Check("Kept results bucket " + s.Names.Bucket())
	}
	return nil
}

func (s *Shared) deleteProject(ctx context.Context) error {
	out, err := s.cb.BatchGetProjects(ctx, &codebuild.BatchGetProjectsInput{Names: []string{s.Names.Project()}})
	if err != nil || len(out.Projects) == 0 {
		return err
	}
	if !ownedCodeBuild(out.Projects[0].Tags) {
		s.log.Warn("Skipped CodeBuild project " + s.Names.Project() + " (not created by Anvil)")
		return nil
	}
	if _, err := s.cb.DeleteProject(ctx, &codebuild.DeleteProjectInput{Name: aws.String(s.Names.Project())}); err != nil {
		return err
	}
	s.log.Check("Deleted CodeBuild project " + s.Names.Project())
	return nil
}

func (s *Shared) deleteScheduleGroup(ctx context.Context) error {
	out, err := s.sched.ListTagsForResource(ctx, &scheduler.ListTagsForResourceInput{ResourceArn: aws.String(s.Names.ScheduleGroupArn())})
	var missing *schedtypes.ResourceNotFoundException
	if errors.As(err, &missing) {
		return nil
	}
	if err != nil {
		return err
	}
	tags := map[string]string{}
	for _, t := range out.Tags {
		tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	if !owned(tags) {
		s.log.Warn("Skipped schedule group " + s.Names.ScheduleGroup() + " (not created by Anvil)")
		return nil
	}
	if _, err := s.sched.DeleteScheduleGroup(ctx, &scheduler.DeleteScheduleGroupInput{Name: aws.String(s.Names.ScheduleGroup())}); err != nil {
		return err
	}
	s.log.Check("Deleted schedule group " + s.Names.ScheduleGroup())
	return nil
}

func (s *Shared) deleteRole(ctx context.Context, name string) error {
	got, err := s.iam.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(name)})
	var missing *iamtypes.NoSuchEntityException
	if errors.As(err, &missing) {
		return nil
	}
	if err != nil {
		return err
	}
	if !ownedIAM(got.Role.Tags) {
		s.log.Warn("Skipped role " + name + " (not created by Anvil)")
		return nil
	}

	attached, err := s.iam.ListAttachedRolePolicies(ctx, &iam.ListAttachedRolePoliciesInput{RoleName: aws.String(name)})
	if err != nil {
		return err
	}
	for _, p := range attached.AttachedPolicies {
		if _, err := s.iam.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{RoleName: aws.String(name), PolicyArn: p.PolicyArn}); err != nil {
			return err
		}
	}
	inline, err := s.iam.ListRolePolicies(ctx, &iam.ListRolePoliciesInput{RoleName: aws.String(name)})
	if err != nil {
		return err
	}
	for _, p := range inline.PolicyNames {
		if _, err := s.iam.DeleteRolePolicy(ctx, &iam.DeleteRolePolicyInput{RoleName: aws.String(name), PolicyName: aws.String(p)}); err != nil {
			return err
		}
	}
	if _, err := s.iam.DeleteRole(ctx, &iam.DeleteRoleInput{RoleName: aws.String(name)}); err != nil {
		return err
	}
	s.log.Check("Deleted role " + name)
	return nil
}

func (s *Shared) deleteLogGroup(ctx context.Context) error {
	out, err := s.logs.ListTagsForResource(ctx, &cloudwatchlogs.ListTagsForResourceInput{ResourceArn: aws.String(s.Names.LogGroupArn())})
	var missing *logstypes.ResourceNotFoundException
	if errors.As(err, &missing) {
		return nil
	}
	if err != nil {
		return err
	}
	if !owned(out.Tags) {
		s.log.Warn("Skipped log group " + s.Names.LogGroup() + " (not created by Anvil)")
		return nil
	}
	if _, err := s.logs.DeleteLogGroup(ctx, &cloudwatchlogs.DeleteLogGroupInput{LogGroupName: aws.String(s.Names.LogGroup())}); err != nil {
		return err
	}
	s.log.Check("Deleted log group " + s.Names.LogGroup())
	return nil
}

func (s *Shared) deleteBucket(ctx context.Context) error {
	bucket := aws.String(s.Names.Bucket())
	owner := aws.String(s.Names.Account)

	tagsOut, err := s.s3.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{Bucket: bucket, ExpectedBucketOwner: owner})
	if httpStatus(err) == 404 {
		return nil
	}
	if err != nil {
		return err
	}
	tags := map[string]string{}
	for _, t := range tagsOut.TagSet {
		tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	if !owned(tags) {
		s.log.Warn("Skipped results bucket " + s.Names.Bucket() + " (not created by Anvil)")
		return nil
	}

	// A versioned bucket must have every version and delete marker removed.
	p := s3.NewListObjectVersionsPaginator(s.s3, &s3.ListObjectVersionsInput{Bucket: bucket, ExpectedBucketOwner: owner})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return err
		}
		var ids []s3types.ObjectIdentifier
		for _, v := range page.Versions {
			ids = append(ids, s3types.ObjectIdentifier{Key: v.Key, VersionId: v.VersionId})
		}
		for _, m := range page.DeleteMarkers {
			ids = append(ids, s3types.ObjectIdentifier{Key: m.Key, VersionId: m.VersionId})
		}
		if len(ids) == 0 {
			continue
		}
		if _, err := s.s3.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: bucket, ExpectedBucketOwner: owner,
			Delete: &s3types.Delete{Objects: ids, Quiet: aws.Bool(true)},
		}); err != nil {
			return err
		}
	}
	if _, err := s.s3.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: bucket, ExpectedBucketOwner: owner}); err != nil {
		return err
	}
	s.log.Check("Deleted results bucket " + s.Names.Bucket())
	return nil
}

// ── Helpers ─────────────────────────────────────────────────

func squatted(bucket string) error {
	return fmt.Errorf("bucket name %s is owned by another AWS account; Anvil will not write to it", bucket)
}

func httpStatus(err error) int {
	var re *awshttp.ResponseError
	if errors.As(err, &re) {
		return re.HTTPStatusCode()
	}
	return 0
}

func owned(tags map[string]string) bool {
	return tags["ManagedBy"] == "anvil" && tags[ownerTagKey] == ownerTagValue
}

func ownedIAM(tags []iamtypes.Tag) bool {
	m := map[string]string{}
	for _, t := range tags {
		m[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	return owned(m)
}

func ownedCodeBuild(tags []cbtypes.Tag) bool {
	m := map[string]string{}
	for _, t := range tags {
		m[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	return owned(m)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func s3Tags(m map[string]string) []s3types.Tag {
	var out []s3types.Tag
	for _, k := range sortedKeys(m) {
		out = append(out, s3types.Tag{Key: aws.String(k), Value: aws.String(m[k])})
	}
	return out
}

func iamTags(m map[string]string) []iamtypes.Tag {
	var out []iamtypes.Tag
	for _, k := range sortedKeys(m) {
		out = append(out, iamtypes.Tag{Key: aws.String(k), Value: aws.String(m[k])})
	}
	return out
}

func codebuildTags(m map[string]string) []cbtypes.Tag {
	var out []cbtypes.Tag
	for _, k := range sortedKeys(m) {
		out = append(out, cbtypes.Tag{Key: aws.String(k), Value: aws.String(m[k])})
	}
	return out
}

func schedulerTags(m map[string]string) []schedtypes.Tag {
	var out []schedtypes.Tag
	for _, k := range sortedKeys(m) {
		out = append(out, schedtypes.Tag{Key: aws.String(k), Value: aws.String(m[k])})
	}
	return out
}
