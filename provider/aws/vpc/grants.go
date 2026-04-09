package vpc

// grants.go — placeholder for VPC-level grant methods.
//
// This file will be populated in ANVIL-N006 (blank slate security group model)
// and ANVIL-N007 (grantEgress).
//
// Planned methods:
//   - (v *Vpc) GrantEgress — not a VPC-level grant, lives on compute resources
//
// VPC itself does not grant access to other resources directly.
// The grant system for VPC-attached compute lives in the shared
// provider/internal/vpcsg/ package (ANVIL-N006).
