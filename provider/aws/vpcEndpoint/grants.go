package vpcendpoint

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// SecurityGroupOutput returns the endpoint's dedicated security group ID output.
// Implements vpcsg.EndpointTarget — called by vpcsg.GrantEndpointAccess when
// a compute resource calls GrantEndpointAccess([]*vpcendpoint.VpcEndpoint{...}).
func (v *VpcEndpoint) SecurityGroupOutput() pulumi.StringOutput {
	return v.SecurityGroupId
}
