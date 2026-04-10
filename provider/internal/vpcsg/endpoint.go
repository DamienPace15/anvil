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

// GrantEndpointAccess opens port 443 between a compute SG and one or more
// endpoint SGs. Called by thin wrappers on each compute resource.
func GrantEndpointAccess(
	ctx *pulumi.Context,
	resourceName string,
	computeSgId pulumi.StringOutput,
	endpoints []EndpointTarget,
	parent pulumi.Resource,
) error {
	for _, endpoint := range endpoints {
		egressRuleName := fmt.Sprintf("%s-%s-endpoint-egress", resourceName, endpoint.Name())
		if err := AddEgressRule(ctx, egressRuleName, computeSgId, endpoint.SecurityGroupOutput(), 443, 443, "tcp", parent); err != nil {
			return fmt.Errorf("GrantEndpointAccess: egress to %s: %w", endpoint.Name(), err)
		}

		ingressRuleName := fmt.Sprintf("%s-%s-endpoint-ingress", endpoint.Name(), resourceName)
		if err := AddIngressRule(ctx, ingressRuleName, endpoint.SecurityGroupOutput(), computeSgId, 443, 443, "tcp", parent); err != nil {
			return fmt.Errorf("GrantEndpointAccess: ingress on %s: %w", endpoint.Name(), err)
		}
	}
	return nil
}
