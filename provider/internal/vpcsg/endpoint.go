package vpcsg

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// EndpointTarget is implemented by any Anvil VPC endpoint resource.
type EndpointTarget interface {
	Name() string
	SecurityGroupOutput() pulumi.StringOutput
}

// GrantEndpointAccess opens egress port 443 from a compute SG to one or more
// endpoint SGs.
//
// The ingress side is owned by the VpcEndpoint constructor — it is wired once
// at endpoint creation time. Compute resources never touch the endpoint SG's
// ingress rules, which prevents duplicate rule errors when multiple compute
// resources reference the same endpoint.
func GrantEndpointAccess(
	ctx *pulumi.Context,
	resourceName string,
	computeSgId pulumi.StringOutput,
	endpoints []EndpointTarget,
	parent pulumi.Resource,
) error {
	for i, endpoint := range endpoints {
		egressRuleName := fmt.Sprintf("%s-%d-endpoint-egress", resourceName, i)
		if err := AddEgressRule(ctx, egressRuleName, computeSgId, endpoint.SecurityGroupOutput(), 443, 443, "tcp", parent); err != nil {
			return fmt.Errorf("GrantEndpointAccess: egress to %s: %w", resourceName, err)
		}
	}
	return nil
}
