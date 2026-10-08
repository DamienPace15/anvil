package compliance

import "encoding/json"

// Managed policies for the scan role: read-only configuration access.
var scanManagedPolicies = []string{
	"arn:aws:iam::aws:policy/SecurityAudit",
	"arn:aws:iam::aws:policy/job-function/ViewOnlyAccess",
}

// prowlerReadActions is Prowler's published prowler-additions-policy.json
// (5.44.0) minus every action that reads data rather than configuration, or
// writes. Removed: lambda:GetFunction* (code download URL), lambda:GetLayerVersion,
// logs:FilterLogEvents, ecr:BatchGetImage, ecr:GetDownloadUrlForLayer,
// s3:ListBucket, ssm:GetDocument, datapipeline:GetPipelineDefinition,
// securityhub:BatchImportFindings (a write), securityhub:GetFindings.
var prowlerReadActions = []string{
	"account:Get*",
	"amplify:ListApps",
	"amplify:ListBranches",
	"appstream:Describe*",
	"appstream:List*",
	"backup:List*",
	"backup:Get*",
	"bedrock:List*",
	"bedrock:Get*",
	"cloudtrail:GetInsightSelectors",
	"codeartifact:List*",
	"codebuild:BatchGet*",
	"codebuild:ListReportGroups",
	"cognito-idp:GetUserPoolMfaConfig",
	"datapipeline:DescribePipelines",
	"datapipeline:ListPipelines",
	"dlm:Get*",
	"drs:Describe*",
	"ds:Get*",
	"ds:Describe*",
	"ds:List*",
	"dynamodb:GetResourcePolicy",
	"ec2:GetEbsEncryptionByDefault",
	"ec2:GetSnapshotBlockPublicAccessState",
	"ec2:GetInstanceMetadataDefaults",
	"ecr:Describe*",
	"ecr:GetRegistryScanningConfiguration",
	"elasticfilesystem:DescribeBackupPolicy",
	"glue:GetConnections",
	"glue:GetSecurityConfiguration*",
	"glue:SearchTables",
	"glue:GetMLTransforms",
	"inspector2:BatchGetFindingDetails",
	// Configuration only, in place of lambda:GetFunction*.
	"lambda:GetFunctionConfiguration",
	"lambda:GetFunctionUrlConfig",
	"lambda:GetFunctionCodeSigningConfig",
	"lambda:GetFunctionConcurrency",
	"lightsail:GetRelationalDatabases",
	"macie2:GetMacieSession",
	"macie2:GetAutomatedDiscoveryConfiguration",
	"rolesanywhere:ListProfiles",
	"rolesanywhere:ListTagsForResource",
	"rolesanywhere:ListTrustAnchors",
	"s3:GetAccountPublicAccessBlock",
	"s3:GetObjectAcl",
	"servicecatalog:Describe*",
	"servicecatalog:List*",
	"shield:DescribeProtection",
	"shield:GetSubscriptionState",
	"ssm-incidents:List*",
	"states:ListTagsForResource",
	"support:Describe*",
	"tag:GetResources",
	"tag:GetTagKeys",
	"wellarchitected:List*",
}

// dataReadActions are explicitly denied. An explicit Deny beats any Allow, so
// the scan role can't read data even if AWS later widens SecurityAudit or
// ViewOnlyAccess.
var dataReadActions = []string{
	"s3:GetObject",
	"s3:GetObjectVersion",
	"s3:ListBucket",
	"s3:ListBucketVersions",
	"logs:GetLogEvents",
	"logs:FilterLogEvents",
	"logs:StartQuery",
	"logs:GetQueryResults",
	"logs:StartLiveTail",
	"lambda:GetFunction",
	"lambda:GetLayerVersion",
	"ecr:BatchGetImage",
	"ecr:GetDownloadUrlForLayer",
	"ssm:GetDocument",
	"ssm:GetParameter",
	"ssm:GetParameters",
	"ssm:GetParametersByPath",
	"secretsmanager:GetSecretValue",
	"dynamodb:GetItem",
	"dynamodb:BatchGetItem",
	"dynamodb:Query",
	"dynamodb:Scan",
	"sqs:ReceiveMessage",
	"kinesis:GetRecords",
	"athena:GetQueryResults",
	"cloudformation:GetTemplate",
	"datapipeline:GetPipelineDefinition",
}

type statement struct {
	Sid       string         `json:"Sid,omitempty"`
	Effect    string         `json:"Effect"`
	Principal map[string]any `json:"Principal,omitempty"`
	Action    any            `json:"Action"`
	Resource  any            `json:"Resource,omitempty"`
	Condition map[string]any `json:"Condition,omitempty"`
}

func document(stmts ...statement) string {
	b, err := json.Marshal(map[string]any{"Version": "2012-10-17", "Statement": stmts})
	if err != nil {
		panic(err) // static structures; can't fail
	}
	return string(b)
}

// scanTrustPolicy lets only this account's compliance project assume the scan
// role (confused-deputy protection).
func scanTrustPolicy(n Names) string {
	return document(statement{
		Effect:    "Allow",
		Principal: map[string]any{"Service": "codebuild.amazonaws.com"},
		Action:    "sts:AssumeRole",
		Condition: map[string]any{
			"StringEquals": map[string]any{"aws:SourceAccount": n.Account},
			"ArnLike":      map[string]any{"aws:SourceArn": n.ProjectArn()},
		},
	})
}

func scanInlinePolicy(n Names) string {
	return document(
		statement{Sid: "ProwlerReadOnly", Effect: "Allow", Action: prowlerReadActions, Resource: "*"},
		statement{
			Sid:      "ProwlerAPIGatewayReadOnly",
			Effect:   "Allow",
			Action:   "apigateway:GET",
			Resource: []string{"arn:aws:apigateway:*::/restapis/*", "arn:aws:apigateway:*::/apis/*"},
		},
		statement{Sid: "NoDataReads", Effect: "Deny", Action: dataReadActions, Resource: "*"},
		statement{
			Sid:      "WriteResults",
			Effect:   "Allow",
			Action:   []string{"s3:PutObject", "s3:PutObjectTagging"},
			Resource: n.BucketArn() + "/results/*",
		},
		statement{
			Sid:      "WriteBuildLogs",
			Effect:   "Allow",
			Action:   []string{"logs:CreateLogStream", "logs:PutLogEvents"},
			Resource: []string{n.LogGroupArn(), n.LogGroupArn() + ":*"},
		},
	)
}

// invokeTrustPolicy lets only EventBridge Scheduler in this account assume the
// invoke role.
func invokeTrustPolicy(n Names) string {
	return document(statement{
		Effect:    "Allow",
		Principal: map[string]any{"Service": "scheduler.amazonaws.com"},
		Action:    "sts:AssumeRole",
		Condition: map[string]any{"StringEquals": map[string]any{"aws:SourceAccount": n.Account}},
	})
}

func invokeInlinePolicy(n Names) string {
	return document(statement{
		Sid:      "StartComplianceScan",
		Effect:   "Allow",
		Action:   "codebuild:StartBuild",
		Resource: n.ProjectArn(),
	})
}

func bucketPolicy(n Names) string {
	return document(statement{
		Sid:       "DenyNonTLS",
		Effect:    "Deny",
		Principal: map[string]any{"AWS": "*"},
		Action:    "s3:*",
		Resource:  []string{n.BucketArn(), n.BucketArn() + "/*"},
		Condition: map[string]any{"Bool": map[string]any{"aws:SecureTransport": "false"}},
	})
}
