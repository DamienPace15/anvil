package awssite

import (
	"fmt"
	"strings"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/acm"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/cloudfront"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/route53"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// DomainResult holds the outputs of a custom domain setup.
type DomainResult struct {
	// CertARN is the validated ACM certificate ARN, ready for use in CloudFront.
	CertARN pulumi.StringOutput

	// DNSInstructions is a human-readable string describing the DNS records
	// the user needs to add if Route53 is not managing the domain.
	DNSInstructions pulumi.StringOutput

	// Validation is the CertificateValidation resource. CloudFront must
	// depend on this to ensure the cert is valid before the distribution is created.
	Validation pulumi.Resource
}

// SetupCustomDomain creates an ACM certificate in us-east-1 and waits for DNS validation.
// Returns a DomainResult the caller uses to wire up CloudFront and Route53.
func SetupCustomDomain(ctx *pulumi.Context, parent pulumi.Resource, name string, domain string) (*DomainResult, error) {
	usEast1, err := CreateUSEast1Provider(ctx, name, parent)
	if err != nil {
		return nil, err
	}

	cert := &acm.Certificate{}
	err = ctx.RegisterResource("aws:acm/certificate:Certificate", name+"-cert", pulumi.Map{
		"domainName":       pulumi.String(domain),
		"validationMethod": pulumi.String("DNS"),
		"tags":             pulumi.StringMap{"ManagedBy": pulumi.String("anvil")},
	}, cert, pulumi.Parent(parent), pulumi.Provider(usEast1))
	if err != nil {
		return nil, err
	}

	dnsInstructions := cert.DomainValidationOptions.ApplyT(func(opts []acm.CertificateDomainValidationOption) string {
		if len(opts) == 0 {
			return ""
		}
		var instructions []string
		for _, opt := range opts {
			if opt.ResourceRecordName != nil && opt.ResourceRecordValue != nil && opt.ResourceRecordType != nil {
				instructions = append(instructions, fmt.Sprintf("  %s  Name: %s  Value: %s",
					*opt.ResourceRecordType, *opt.ResourceRecordName, *opt.ResourceRecordValue))

				ctx.Log.Warn(fmt.Sprintf(
					"\n⏳ Add this DNS record to validate your certificate for %s:\n"+
						"   Type:  %s\n"+
						"   Name:  %s\n"+
						"   Value: %s\n"+
						"   Deploy will continue automatically once the record is detected.\n",
					domain, *opt.ResourceRecordType, *opt.ResourceRecordName, *opt.ResourceRecordValue), nil)
			}
		}
		if len(instructions) == 0 {
			return ""
		}
		return fmt.Sprintf("DNS records for %s certificate validation:\n%s", domain, strings.Join(instructions, "\n"))
	}).(pulumi.StringOutput)

	certValidation := &acm.CertificateValidation{}
	err = ctx.RegisterResource("aws:acm/certificateValidation:CertificateValidation", name+"-cert-validation", pulumi.Map{
		"certificateArn": cert.Arn,
	}, certValidation, pulumi.Parent(parent), pulumi.Provider(usEast1))
	if err != nil {
		return nil, err
	}

	return &DomainResult{
		CertARN:         certValidation.CertificateArn,
		DNSInstructions: dnsInstructions,
		Validation:      certValidation,
	}, nil
}

// CreateRoute53Records creates A and AAAA alias records pointing to a CloudFront distribution.
// Best-effort — logs a warning and returns if the hosted zone is not found.
func CreateRoute53Records(ctx *pulumi.Context, parent pulumi.Resource, name string, domain string, distribution *cloudfront.Distribution) {
	zone, err := route53.LookupZone(ctx, &route53.LookupZoneArgs{
		Name:        pulumi.StringRef(ExtractZoneName(domain)),
		PrivateZone: pulumi.BoolRef(false),
	})
	if err != nil || zone == nil {
		ctx.Log.Info(fmt.Sprintf("No Route53 hosted zone for %s — point DNS to CloudFront manually.", ExtractZoneName(domain)), nil)
		return
	}

	for _, recordType := range []string{"A", "AAAA"} {
		err = ctx.RegisterResource("aws:route53/record:Record", fmt.Sprintf("%s-domain-%s", name, strings.ToLower(recordType)), pulumi.Map{
			"zoneId": pulumi.String(zone.ZoneId),
			"name":   pulumi.String(domain),
			"type":   pulumi.String(recordType),
			"aliases": pulumi.Array{pulumi.Map{
				"name":                 distribution.DomainName,
				"zoneId":               pulumi.String("Z2FDTNDATAQYW2"),
				"evaluateTargetHealth": pulumi.Bool(false),
			}},
		}, &route53.Record{}, pulumi.Parent(parent))
		if err != nil {
			ctx.Log.Warn(fmt.Sprintf("Failed to create Route53 %s record: %v", recordType, err), nil)
		}
	}

	ctx.Log.Info(fmt.Sprintf("Route53 DNS records created for %s", domain), nil)
}
