package compliance

import "fmt"

// Names derives every shared-layer resource name from the account and region,
// so any CLI in the account finds the same resources without state.
type Names struct {
	Account string
	Region  string
}

func (n Names) base() string { return fmt.Sprintf("anvil-compliance-%s-%s", n.Account, n.Region) }

// Bucket and Project share the base name. S3 names are global, hence the
// account ID; regional resources are per region, hence the region.
func (n Names) Bucket() string   { return n.base() }
func (n Names) Project() string  { return n.base() }
func (n Names) LogGroup() string { return "/aws/codebuild/" + n.base() }

// IAM is global to the account, so role names carry the region.
func (n Names) ScanRole() string   { return "anvil-compliance-scan-" + n.Region }
func (n Names) InvokeRole() string { return "anvil-compliance-invoke-" + n.Region }

// ScheduleGroup is regional, so it needs no suffix.
func (n Names) ScheduleGroup() string { return "anvil-compliance" }

func (n Names) BucketArn() string { return "arn:aws:s3:::" + n.Bucket() }
func (n Names) ProjectArn() string {
	return fmt.Sprintf("arn:aws:codebuild:%s:%s:project/%s", n.Region, n.Account, n.Project())
}
func (n Names) LogGroupArn() string {
	return fmt.Sprintf("arn:aws:logs:%s:%s:log-group:%s", n.Region, n.Account, n.LogGroup())
}
func (n Names) ScheduleGroupArn() string {
	return fmt.Sprintf("arn:aws:scheduler:%s:%s:schedule-group/%s", n.Region, n.Account, n.ScheduleGroup())
}
func (n Names) roleArn(name string) string {
	return fmt.Sprintf("arn:aws:iam::%s:role/%s", n.Account, name)
}
func (n Names) ScanRoleArn() string   { return n.roleArn(n.ScanRole()) }
func (n Names) InvokeRoleArn() string { return n.roleArn(n.InvokeRole()) }
