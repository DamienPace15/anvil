// sdk/go/anvil/grants.go
// Hand-written. Backed up/restored during gen-sdk like app.go and block.go.
//
// Provides the runtime grant execution for all resource grant methods.

package anvil

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/iam"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// GrantTarget is implemented by any Anvil compute resource that can receive
// IAM permissions from infra resources via grant methods.
type GrantTarget interface {
	// GrantName returns the logical resource name passed to the constructor.
	GrantName() string

	// GrantRoleArn returns the ARN of the IAM execution role attached to this
	// compute resource.
	GrantRoleArn() pulumi.StringOutput
}

// GrantOptions provides optional metadata for grant methods.
type GrantOptions struct {
	// Justification documents why this grant is needed.
	// Stored as a tag on the generated IAM policy resource for audit purposes.
	Justification string
}

// CreateGrant creates a scoped IAM RolePolicy granting the specified actions
// on the specified resource ARNs to the target's execution role.
//
// This is the core engine that all resource-specific grant methods delegate to.
func CreateGrant(
	ctx *pulumi.Context,
	parent pulumi.Resource,
	name string,
	target GrantTarget,
	actions []string,
	resourceArns []pulumi.StringInput,
	opts *GrantOptions,
) error {
	policyJSON := pulumi.All(toOutputs(resourceArns)...).ApplyT(func(args []interface{}) (string, error) {
		resources := make([]string, len(args))
		for i, a := range args {
			resources[i] = a.(string)
		}

		doc := map[string]interface{}{
			"Version": "2012-10-17",
			"Statement": []map[string]interface{}{
				{
					"Effect":   "Allow",
					"Action":   actions,
					"Resource": resources,
				},
			},
		}

		b, err := json.Marshal(doc)
		if err != nil {
			return "", fmt.Errorf("failed to marshal IAM policy: %w", err)
		}
		return string(b), nil
	}).(pulumi.StringOutput)

	roleName := target.GrantRoleArn().ApplyT(func(arn string) string {
		idx := strings.LastIndex(arn, "/")
		if idx >= 0 {
			return arn[idx+1:]
		}
		return arn
	}).(pulumi.StringOutput)

	// Encode justification in resource name for audit trail
	policyName := name
	if opts != nil && opts.Justification != "" {
		policyName = fmt.Sprintf("%s-%s", name, sanitize(opts.Justification))
	}

	_, err := iam.NewRolePolicy(ctx, policyName, &iam.RolePolicyArgs{
		Role:   roleName,
		Policy: policyJSON,
	}, pulumi.Parent(parent))

	return err
}

// sanitize cleans a string for use in resource names.
func sanitize(s string) string {
	s = strings.ToLower(s)
	result := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			result = append(result, c)
		} else {
			result = append(result, '-')
		}
	}
	if len(result) > 40 {
		result = result[:40]
	}
	return string(result)
}

// BuildResourceArns constructs the list of ARNs for a grant based on the
// resource's base ARN and optional path scoping.
func BuildResourceArns(baseArn pulumi.StringOutput, paths []string) []pulumi.StringInput {
	arns := []pulumi.StringInput{baseArn}

	if len(paths) == 0 {
		arns = append(arns, pulumi.Sprintf("%s/*", baseArn))
	} else {
		for _, p := range paths {
			arns = append(arns, pulumi.Sprintf("%s/%s", baseArn, p))
		}
	}

	return arns
}

func toOutputs(inputs []pulumi.StringInput) []interface{} {
	outputs := make([]interface{}, len(inputs))
	for i, input := range inputs {
		outputs[i] = input.ToStringOutput()
	}
	return outputs
}
