package compliance

import (
	"encoding/json"
	"sort"
	"strings"
)

// The dashboard's data model. Built from a stored scan, the previous scan (for
// "what changed"), and a map of resource ARN → Anvil component from the stack's
// Pulumi state.

type ViewFramework struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Name      string `json:"name"`
	Supported bool   `json:"supported"` // false: findings don't carry requirement mappings for it
}

type ViewResource struct {
	UID           string `json:"uid"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Region        string `json:"region,omitempty"`
	Service       string `json:"service,omitempty"`
	Component     string `json:"component"`
	ComponentType string `json:"componentType"`
}

type ViewFinding struct {
	ID            string              `json:"id"`
	Check         string              `json:"check"`
	Title         string              `json:"title"`
	Status        string              `json:"status"`
	Severity      string              `json:"severity"`
	Detail        string              `json:"detail"`
	Risk          string              `json:"risk"`
	Remediation   string              `json:"remediation"`
	Refs          []string            `json:"refs"`
	Resource      ViewResource        `json:"resource"`
	Component     string              `json:"component"`
	ComponentType string              `json:"componentType"`
	Frameworks    map[string][]string `json:"frameworks"` // framework label → requirement IDs
}

type ViewChanges struct {
	Compared bool          `json:"compared"` // false when there's no earlier complete scan
	Previous string        `json:"previous,omitempty"`
	New      []string      `json:"new"`   // IDs of findings failing now but not before
	Fixed    []ViewFinding `json:"fixed"` // failing before, not now
}

type View struct {
	Project    string          `json:"project"`
	Stage      string          `json:"stage"`
	Account    string          `json:"account"`
	Region     string          `json:"region"`
	Scan       Manifest        `json:"scan"`
	Frameworks []ViewFramework `json:"frameworks"`
	Findings   []ViewFinding   `json:"findings"`
	Resources  []ViewResource  `json:"resources"`
	Dropped    int             `json:"dropped"`
	Changes    ViewChanges     `json:"changes"`
	LastDeploy string          `json:"lastDeploy,omitempty"`
}

// Component is the Anvil component that owns a resource.
type Component struct {
	Name string
	Type string
}

// ViewInput is everything BuildView needs.
type ViewInput struct {
	Project, Stage, Account, Region string
	Current                         *ScanResult
	Previous                        *ScanResult // nil when there's no earlier complete scan
	Components                      map[string]Component
	LastDeploy                      string
}

// ocsfFinding is the subset of Prowler's OCSF finding the dashboard uses.
type ocsfFinding struct {
	StatusCode   string `json:"status_code"`
	Severity     string `json:"severity"`
	StatusDetail string `json:"status_detail"`
	Message      string `json:"message"`
	RiskDetails  string `json:"risk_details"`
	Metadata     struct {
		EventCode string `json:"event_code"`
	} `json:"metadata"`
	FindingInfo struct {
		Title string `json:"title"`
	} `json:"finding_info"`
	Remediation struct {
		Desc       string   `json:"desc"`
		References []string `json:"references"`
	} `json:"remediation"`
	Resources []struct {
		UID    string   `json:"uid"`
		Name   string   `json:"name"`
		Type   string   `json:"type"`
		Region string   `json:"region"`
		Labels []string `json:"labels"`
		Group  struct {
			Name string `json:"name"`
		} `json:"group"`
	} `json:"resources"`
	Unmapped struct {
		Compliance     map[string][]string `json:"compliance"`
		AdditionalURLs []string            `json:"additional_urls"`
	} `json:"unmapped"`
}

var ocsfKinds = map[string]string{
	"AwsLambdaFunction":         "Lambda",
	"AwsS3Bucket":               "S3 bucket",
	"AwsCloudFrontDistribution": "CloudFront distribution",
	"AwsIamRole":                "IAM role",
	"AwsWafv2WebAcl":            "WAF WebACL",
	"AwsLogsLogGroup":           "Log group",
	"AwsDynamoDbTable":          "DynamoDB table",
	"AwsSqsQueue":               "SQS queue",
	"AwsApiGatewayV2Api":        "API Gateway",
	"AwsEc2Vpc":                 "VPC",
}

var arnKinds = map[string]string{
	"lambda": "Lambda", "s3": "S3 bucket", "cloudfront": "CloudFront distribution", "iam": "IAM role",
	"wafv2": "WAF WebACL", "logs": "Log group", "dynamodb": "DynamoDB table", "sqs": "SQS queue",
	"apigateway": "API Gateway", "ec2": "EC2 resource", "dsql": "DSQL cluster", "events": "EventBridge",
	"cognito-idp": "Cognito user pool", "kms": "KMS key", "sns": "SNS topic",
}

// arnName is the human part of an ARN: the bucket, function, role or
// distribution name.
func arnName(arn string) string {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 6 {
		return arn
	}
	res := parts[5]
	switch parts[2] {
	case "wafv2": // global/webacl/<name>/<id>
		if seg := strings.Split(res, "/"); len(seg) >= 3 {
			return seg[2]
		}
	case "logs": // log-group:<name>[:*]
		return strings.TrimSuffix(strings.TrimPrefix(res, "log-group:"), ":*")
	}
	if i := strings.LastIndexAny(res, "/:"); i >= 0 {
		return res[i+1:]
	}
	return res
}

func arnService(arn string) string {
	if parts := strings.SplitN(arn, ":", 4); len(parts) >= 3 {
		return parts[2]
	}
	return ""
}

// NormaliseARN makes ARNs from Pulumi state and Prowler comparable.
func NormaliseARN(arn string) string { return strings.TrimSuffix(arn, ":*") }

// componentFor resolves a resource's owning component: from Pulumi state when
// known, else from Anvil's Component tag (bootstrap resources aren't in state).
func componentFor(uid string, labels []string, comps map[string]Component) Component {
	if c, ok := comps[NormaliseARN(uid)]; ok {
		return c
	}
	for _, l := range labels {
		if k, v, ok := strings.Cut(l, ":"); ok && k == "Component" {
			if v == "StateBucket" {
				return Component{Name: "State bucket", Type: "Bootstrap"}
			}
			return Component{Name: v, Type: v}
		}
	}
	return Component{Name: "Other", Type: "Not in this stack's state"}
}

func findingID(check, uid string) string { return check + "|" + uid }

func parseFindings(raw []json.RawMessage, frameworks []ViewFramework, comps map[string]Component) []ViewFinding {
	keyToLabel := map[string]string{}
	for _, fw := range frameworks {
		if info := Framework(fw.ID); info.Key != "" {
			keyToLabel[info.Key] = fw.Label
		}
	}

	out := make([]ViewFinding, 0, len(raw))
	for _, r := range raw {
		var f ocsfFinding
		if json.Unmarshal(r, &f) != nil || len(f.Resources) == 0 {
			continue
		}
		res := f.Resources[0]
		comp := componentFor(res.UID, res.Labels, comps)
		kind := ocsfKinds[res.Type]
		if kind == "" {
			kind = strings.TrimPrefix(res.Type, "Aws")
		}
		name := res.Name
		if name == "" {
			name = arnName(res.UID)
		}

		fws := map[string][]string{}
		for key, reqs := range f.Unmapped.Compliance {
			if label, ok := keyToLabel[key]; ok && len(reqs) > 0 {
				fws[label] = reqs
			}
		}

		refs := append([]string{}, f.Remediation.References...)
		for i, u := range f.Unmapped.AdditionalURLs {
			if i == 3 {
				break
			}
			refs = append(refs, u)
		}
		detail := f.StatusDetail
		if detail == "" {
			detail = f.Message
		}

		out = append(out, ViewFinding{
			ID:          findingID(f.Metadata.EventCode, res.UID),
			Check:       f.Metadata.EventCode,
			Title:       f.FindingInfo.Title,
			Status:      strings.ToUpper(f.StatusCode),
			Severity:    strings.ToLower(f.Severity),
			Detail:      detail,
			Risk:        f.RiskDetails,
			Remediation: f.Remediation.Desc,
			Refs:        refs,
			Resource: ViewResource{
				UID: res.UID, Name: name, Kind: kind, Region: res.Region, Service: res.Group.Name,
				Component: comp.Name, ComponentType: comp.Type,
			},
			Component:     comp.Name,
			ComponentType: comp.Type,
			Frameworks:    fws,
		})
	}
	return out
}

// BuildView assembles the dashboard data for one scan.
func BuildView(in ViewInput) View {
	m := in.Current.Manifest
	v := View{
		Project: in.Project, Stage: in.Stage, Account: in.Account, Region: in.Region,
		Scan: m, Dropped: m.DroppedOutOfScope, LastDeploy: in.LastDeploy,
	}

	for _, id := range m.Frameworks {
		info := Framework(id)
		v.Frameworks = append(v.Frameworks, ViewFramework{ID: id, Label: info.Label, Name: info.Name, Supported: info.Key != ""})
	}

	v.Findings = parseFindings(in.Current.Findings, v.Frameworks, in.Components)

	labelsByUID := map[string][]string{}
	for _, r := range in.Current.Findings {
		var f ocsfFinding
		if json.Unmarshal(r, &f) == nil && len(f.Resources) > 0 {
			labelsByUID[f.Resources[0].UID] = f.Resources[0].Labels
		}
	}
	for _, arn := range m.Resources.ARNs {
		comp := componentFor(arn, labelsByUID[arn], in.Components)
		if _, inState := in.Components[NormaliseARN(arn)]; !inState && labelsByUID[arn] == nil && strings.HasPrefix(arnName(arn), in.Stage+"-anvil-state-") {
			comp = Component{Name: "State bucket", Type: "Bootstrap"}
		}
		kind := arnKinds[arnService(arn)]
		if kind == "" {
			kind = arnService(arn)
		}
		v.Resources = append(v.Resources, ViewResource{
			UID: arn, Name: arnName(arn), Kind: kind, Component: comp.Name, ComponentType: comp.Type,
		})
	}

	v.Changes = ViewChanges{New: []string{}, Fixed: []ViewFinding{}}
	if in.Previous != nil {
		v.Changes.Compared = true
		v.Changes.Previous = in.Previous.Manifest.ScanID
		prev := parseFindings(in.Previous.Findings, v.Frameworks, in.Components)
		prevFail := map[string]ViewFinding{}
		for _, f := range prev {
			if f.Status == "FAIL" {
				prevFail[f.ID] = f
			}
		}
		curFail := map[string]bool{}
		for _, f := range v.Findings {
			if f.Status == "FAIL" {
				curFail[f.ID] = true
				if _, was := prevFail[f.ID]; !was {
					v.Changes.New = append(v.Changes.New, f.ID)
				}
			}
		}
		for id, f := range prevFail {
			if !curFail[id] {
				v.Changes.Fixed = append(v.Changes.Fixed, f)
			}
		}
		sort.Slice(v.Changes.Fixed, func(i, j int) bool { return v.Changes.Fixed[i].ID < v.Changes.Fixed[j].ID })
	}
	return v
}
