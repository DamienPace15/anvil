package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	cognitotypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"github.com/aws/aws-sdk-go-v2/service/dsql"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
)

// protectedResource is a resource that would block a destroy: either Pulumi's
// `protect` option, or deletion protection enabled on the AWS resource itself.
type protectedResource struct {
	urn    string
	kind   string // human-readable reason, e.g. "DynamoDB deletion protection"
	unlock func(ctx context.Context) error
}

// findProtectedResources lists every resource in the stack's state that a plain
// destroy would refuse to delete. Nothing is changed.
func findProtectedResources(ctx context.Context, s auto.Stack, defaultRegion string) (*apitype.DeploymentV3, []protectedResource, error) {
	exported, err := s.Export(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("exporting stack state: %w", err)
	}

	var deployment apitype.DeploymentV3
	if err := json.Unmarshal(exported.Deployment, &deployment); err != nil {
		return nil, nil, fmt.Errorf("reading stack state: %w", err)
	}

	var found []protectedResource
	for _, r := range deployment.Resources {
		urn := string(r.URN)
		if r.Protect {
			found = append(found, protectedResource{urn: urn, kind: "Pulumi protect"})
		}

		region := outputString(r.Outputs, "region")
		if region == "" {
			region = defaultRegion
		}

		switch string(r.Type) {
		case "aws:dynamodb/table:Table":
			name := outputString(r.Outputs, "name")
			if outputBool(r.Outputs, "deletionProtectionEnabled") && name != "" {
				found = append(found, protectedResource{urn: urn, kind: "DynamoDB deletion protection",
					unlock: func(ctx context.Context) error {
						cfg, err := awsConfigFor(ctx, region)
						if err != nil {
							return err
						}
						_, err = dynamodb.NewFromConfig(cfg).UpdateTable(ctx, &dynamodb.UpdateTableInput{
							TableName:                 aws.String(name),
							DeletionProtectionEnabled: aws.Bool(false),
						})
						return err
					}})
			}
		case "aws:cognito/userPool:UserPool":
			id := outputString(r.Outputs, "id")
			if outputString(r.Outputs, "deletionProtection") == "ACTIVE" && id != "" {
				found = append(found, protectedResource{urn: urn, kind: "Cognito deletion protection",
					unlock: func(ctx context.Context) error {
						cfg, err := awsConfigFor(ctx, region)
						if err != nil {
							return err
						}
						// UpdateUserPool resets fields that aren't passed; acceptable
						// here because the pool is deleted immediately afterwards.
						_, err = cognitoidentityprovider.NewFromConfig(cfg).UpdateUserPool(ctx, &cognitoidentityprovider.UpdateUserPoolInput{
							UserPoolId:         aws.String(id),
							DeletionProtection: cognitotypes.DeletionProtectionTypeInactive,
						})
						return err
					}})
			}
		case "aws:dsql/cluster:Cluster":
			id := outputString(r.Outputs, "identifier")
			if outputBool(r.Outputs, "deletionProtectionEnabled") && id != "" {
				found = append(found, protectedResource{urn: urn, kind: "DSQL deletion protection",
					unlock: func(ctx context.Context) error {
						cfg, err := awsConfigFor(ctx, region)
						if err != nil {
							return err
						}
						_, err = dsql.NewFromConfig(cfg).UpdateCluster(ctx, &dsql.UpdateClusterInput{
							Identifier:                aws.String(id),
							DeletionProtectionEnabled: aws.Bool(false),
						})
						return err
					}})
			}
		}
	}

	return &deployment, found, nil
}

// removeProtection clears Pulumi `protect` flags in state and disables
// AWS-level deletion protection, so a following destroy can delete everything.
func removeProtection(ctx context.Context, s auto.Stack, deployment *apitype.DeploymentV3, found []protectedResource) error {
	stateChanged := false
	for i := range deployment.Resources {
		if deployment.Resources[i].Protect {
			deployment.Resources[i].Protect = false
			stateChanged = true
		}
	}

	if stateChanged {
		raw, err := json.Marshal(deployment)
		if err != nil {
			return fmt.Errorf("encoding stack state: %w", err)
		}
		if err := s.Import(ctx, apitype.UntypedDeployment{Version: 3, Deployment: raw}); err != nil {
			return fmt.Errorf("unprotecting resources in state: %w", err)
		}
	}

	for _, p := range found {
		if p.unlock == nil {
			continue
		}
		if err := p.unlock(ctx); err != nil {
			return fmt.Errorf("disabling %s on %s: %w", p.kind, p.urn, err)
		}
	}

	return nil
}

func awsConfigFor(ctx context.Context, region string) (aws.Config, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return aws.Config{}, fmt.Errorf("AWS credentials not found or expired: %w", err)
	}
	return cfg, nil
}

func outputString(outputs map[string]interface{}, key string) string {
	v, _ := outputs[key].(string)
	return v
}

func outputBool(outputs map[string]interface{}, key string) bool {
	v, _ := outputs[key].(bool)
	return v
}
