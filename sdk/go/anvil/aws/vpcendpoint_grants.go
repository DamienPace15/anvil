// sdk/go/anvil/aws/vpcendpoint_grants.go
// Hand-written. Not generated — do not delete.
//
// Defines the EndpointTarget interface and NewVpcEndpointTarget constructor.
// The generated vpcEndpoint.go is regenerated on every gen-sdk run — this
// file survives regeneration because it is hand-written.
//
// EndpointTarget lives in the aws package so users never need a separate
// grants import — everything stays in aws.

package aws

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// EndpointTarget is implemented by any Anvil VPC endpoint resource.
// Pass a slice of EndpointTarget to Lambda.GrantVpcEndpointAccess.
//
// Satisfied by *VpcEndpoint via NewVpcEndpointTarget.
type EndpointTarget interface {
	// EndpointName returns the logical name of the endpoint.
	// Used for SG rule naming — must match the name passed to NewVpcEndpoint.
	EndpointName() string

	// EndpointSecurityGroupId returns the dedicated SG ID of the endpoint.
	// Ingress rules are added to this SG when compute resources call
	// GrantVpcEndpointAccess.
	EndpointSecurityGroupId() pulumi.StringOutput
}

// vpcEndpointTarget wraps a *VpcEndpoint with its logical name so it satisfies
// EndpointTarget. Created via NewVpcEndpointTarget.
type vpcEndpointTarget struct {
	name     string
	endpoint *VpcEndpoint
}

// EndpointName returns the logical name of this endpoint.
func (t *vpcEndpointTarget) EndpointName() string {
	return t.name
}

// EndpointSecurityGroupId returns the endpoint's dedicated security group ID.
func (t *vpcEndpointTarget) EndpointSecurityGroupId() pulumi.StringOutput {
	return t.endpoint.SecurityGroupId
}

// NewVpcEndpointTarget wraps a *VpcEndpoint so it satisfies EndpointTarget.
// Pass the same name used when constructing the VpcEndpoint.
//
// Usage:
//
//	ep, _ := aws.NewVpcEndpoint(ctx, "ssm", &aws.VpcEndpointArgs{...})
//
//	fn.GrantVpcEndpointAccess(ctx, []aws.EndpointTarget{
//	    aws.NewVpcEndpointTarget("ssm", ep),
//	})
func NewVpcEndpointTarget(name string, endpoint *VpcEndpoint) EndpointTarget {
	return &vpcEndpointTarget{name: name, endpoint: endpoint}
}
