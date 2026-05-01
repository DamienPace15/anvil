package cognitouserpool

import (
	"encoding/json"
	"fmt"

	provider "github.com/DamienPace15/anvil/provider/internal/shared"
	"github.com/DamienPace15/anvil/provider/internal/transform"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/cognito"
	awsconfig "github.com/pulumi/pulumi-aws/sdk/v7/go/aws/config"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/iam"
	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	c "github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

// ── Nested arg types ───────────────────────────────────────────────────────

// PasswordPolicyArgs uses *bool for all boolean fields so Anvil can distinguish
// between "user explicitly set false" and "user omitted the field".
// This is critical — Go zero-value for bool is false, so without pointers a
// partial override like { minLength: 16 } would silently disable all character
// requirements. With *bool, nil means "use the Tier 1 default".
type PasswordPolicyArgs struct {
	MinLength                     int   `pulumi:"minLength,optional"`
	RequireUppercase              *bool `pulumi:"requireUppercase,optional"`
	RequireLowercase              *bool `pulumi:"requireLowercase,optional"`
	RequireNumbers                *bool `pulumi:"requireNumbers,optional"`
	RequireSymbols                *bool `pulumi:"requireSymbols,optional"`
	TemporaryPasswordValidityDays int   `pulumi:"temporaryPasswordValidityDays,optional"`
}

type MfaArgs struct {
	// Mode: OFF | OPTIONAL | REQUIRED
	Mode         string   `pulumi:"mode,optional"`
	Methods      []string `pulumi:"methods,optional"`
	SnsCallerArn string   `pulumi:"snsCallerArn,optional"`
}

type TokenValidityArgs struct {
	AccessTokenValidity  int `pulumi:"accessTokenValidity,optional"`
	IdTokenValidity      int `pulumi:"idTokenValidity,optional"`
	RefreshTokenValidity int `pulumi:"refreshTokenValidity,optional"`
}

type AppClientArgs struct {
	CallbackUrls               []string           `pulumi:"callbackUrls,optional"`
	LogoutUrls                 []string           `pulumi:"logoutUrls,optional"`
	OAuthFlows                 []string           `pulumi:"oauthFlows,optional"`
	OAuthScopes                []string           `pulumi:"oauthScopes,optional"`
	GenerateSecret             bool               `pulumi:"generateSecret,optional"`
	TokenValidity              *TokenValidityArgs `pulumi:"tokenValidity,optional"`
	SupportedIdentityProviders []string           `pulumi:"supportedIdentityProviders,optional"`
}

type HostedUiArgs struct {
	Domain            string `pulumi:"domain"`
	CustomDomain      bool   `pulumi:"customDomain,optional"`
	AcmCertificateArn string `pulumi:"acmCertificateArn,optional"`
}

// IdentityProviderArgs is a generic shape covering all provider types.
// The Type field discriminates which other fields are used in the constructor.
// Adding a new provider never requires a schema change — only a new case in
// buildIdpDetails below.
type IdentityProviderArgs struct {
	// Type: Google | Facebook | LoginWithAmazon | SignInWithApple | OIDC | SAML
	Type             string            `pulumi:"type"`
	Name             string            `pulumi:"name,optional"`
	ClientId         string            `pulumi:"clientId,optional"`
	ClientSecret     string            `pulumi:"clientSecret,optional"`
	OidcIssuer       string            `pulumi:"oidcIssuer,optional"`
	MetadataUrl      string            `pulumi:"metadataUrl,optional"`
	MetadataContent  string            `pulumi:"metadataContent,optional"`
	AttributeMapping map[string]string `pulumi:"attributeMapping,optional"`
}

type CustomAttributeArgs struct {
	Name    string `pulumi:"name"`
	Type    string `pulumi:"type,optional"`
	Mutable bool   `pulumi:"mutable,optional"`
}

type AttributesArgs struct {
	UsernameAttributes []string              `pulumi:"usernameAttributes,optional"`
	RequiredAttributes []string              `pulumi:"requiredAttributes,optional"`
	CustomAttributes   []CustomAttributeArgs `pulumi:"customAttributes,optional"`
}

type EmailConfigurationArgs struct {
	SesFromAddress string `pulumi:"sesFromAddress,optional"`
	ReplyToAddress string `pulumi:"replyToAddress,optional"`
}

// ── Main Args ──────────────────────────────────────────────────────────────

// CognitoUserPoolArgs defines the inputs for an Anvil-managed Cognito user pool.
type CognitoUserPoolArgs struct {
	// PasswordPolicy controls the password requirements enforced at sign-up and
	// password change. Anvil defaults satisfy CIS Benchmarks and SOC 2 baseline.
	// Partial overrides are safe — omitted boolean fields keep the secure default.
	PasswordPolicy *PasswordPolicyArgs `pulumi:"passwordPolicy,optional"`

	// Mfa controls multi-factor authentication enforcement. TOTP requires no
	// additional AWS resources. SMS requires an SNS caller ARN (auto-created if omitted).
	Mfa *MfaArgs `pulumi:"mfa,optional"`

	// AppClient is the default app client created with the pool. Pass appClientId
	// to CognitoAuth.audience to protect your API routes.
	AppClient *AppClientArgs `pulumi:"appClient,optional"`

	// HostedUi enables the Cognito-managed sign-in page. Omit to use the user
	// pools API directly (SDK-based auth only).
	HostedUi *HostedUiArgs `pulumi:"hostedUi,optional"`

	// IdentityProviders lists external IdPs to federate. Type field discriminates
	// the shape — no schema changes required when adding new providers.
	IdentityProviders []IdentityProviderArgs `pulumi:"identityProviders,optional"`

	// Attributes controls sign-in identifiers and required/custom attributes.
	// Username attributes and required attributes cannot be changed after creation.
	Attributes *AttributesArgs `pulumi:"attributes,optional"`

	// EmailConfiguration controls email delivery. Default is Cognito-managed (50/day limit).
	// Set sesFromAddress to use SES for production workloads.
	EmailConfiguration *EmailConfigurationArgs `pulumi:"emailConfiguration,optional"`

	Transform map[string]map[string]interface{} `pulumi:"transform,optional"`
}

// ── Component ──────────────────────────────────────────────────────────────

// CognitoUserPool is the Anvil-managed Cognito user pool component.
type CognitoUserPool struct {
	pulumi.ResourceState

	// UserPoolId is the Cognito user pool ID.
	// Pass to CognitoAuth.userPoolId.
	UserPoolId pulumi.StringOutput `pulumi:"userPoolId"`

	// UserPoolArn is the ARN of the Cognito user pool.
	UserPoolArn pulumi.StringOutput `pulumi:"userPoolArn"`

	// AppClientId is the ID of the default app client.
	// Pass to CognitoAuth audience: [pool.appClientId].
	AppClientId pulumi.StringOutput `pulumi:"appClientId"`

	// AppClientSecret is the client secret for the default app client.
	// Only populated when appClient.generateSecret is true. Always marked secret
	// in Pulumi state — never stored in plaintext.
	AppClientSecret pulumi.StringOutput `pulumi:"appClientSecret"`

	// HostedUiDomain is the full hosted UI base URL.
	// Empty string if hostedUi is not configured.
	HostedUiDomain pulumi.StringOutput `pulumi:"hostedUiDomain"`

	// CloudFrontDomain is the CloudFront distribution domain for a custom hosted UI.
	// Only populated when hostedUi.customDomain is true. Create a Route53 alias
	// record pointing hostedUi.domain → this value to complete DNS setup.
	CloudFrontDomain pulumi.StringOutput `pulumi:"cloudFrontDomain"`

	// Endpoint is the OIDC issuer URL for this user pool.
	// Format: https://cognito-idp.{region}.amazonaws.com/{userPoolId}
	Endpoint pulumi.StringOutput `pulumi:"endpoint"`
}

func (cu *CognitoUserPool) Annotate(a infer.Annotator) {
	a.SetToken("aws", "CognitoUserPool")
	a.Describe(&cu, "An Anvil-managed Cognito user pool. Tier 1 controls (deletion protection, enforced password policy, account recovery via email) are always on. Pair userPoolId and appClientId with CognitoAuth to protect API Gateway routes.")
}

// ── Constructor ────────────────────────────────────────────────────────────

func NewCognitoUserPool(ctx *pulumi.Context, name string, args CognitoUserPoolArgs, opts ...pulumi.ResourceOption) (*CognitoUserPool, error) {
	comp := &CognitoUserPool{}

	provider.NewContext(ctx)

	cfg := c.New(ctx, "anvil")
	stage := cfg.Require("stage")
	stageId := cfg.Require("stageId")

	opts = provider.WithDefault(opts, true)

	if err := ctx.RegisterComponentResource(p.GetTypeToken(ctx), name, comp, opts...); err != nil {
		return nil, err
	}

	region := awsconfig.GetRegion(ctx)
	if region == "" {
		return nil, fmt.Errorf("CognitoUserPool %q: could not determine AWS region — ensure the AWS provider is configured with a region", name)
	}

	// between stages and matching the naming convention of all other Anvil components.
	physicalName := provider.PhysicalName(stage, name, "userpool", stageId)

	// ── 1. Password policy ─────────────────────────────────────────────────
	// Tier 1: enforce a strong baseline by default.
	// This means { minLength: 16 } only changes length; all other requirements
	// remain true. Without *bool, Go zero-value would silently set them to false.
	boolVal := func(ptr *bool, def bool) bool {
		if ptr == nil {
			return def
		}
		return *ptr
	}

	minLength := 12
	requireUppercase := true
	requireLowercase := true
	requireNumbers := true
	requireSymbols := true
	tempPasswordDays := 7

	if args.PasswordPolicy != nil {
		pp := args.PasswordPolicy
		if pp.MinLength > 0 {
			minLength = pp.MinLength
		}
		if pp.TemporaryPasswordValidityDays > 0 {
			tempPasswordDays = pp.TemporaryPasswordValidityDays
		}
		requireUppercase = boolVal(pp.RequireUppercase, true)
		requireLowercase = boolVal(pp.RequireLowercase, true)
		requireNumbers = boolVal(pp.RequireNumbers, true)
		requireSymbols = boolVal(pp.RequireSymbols, true)
	}

	passwordPolicyArg := &cognito.UserPoolPasswordPolicyArgs{
		MinimumLength:                 pulumi.Int(minLength),
		RequireUppercase:              pulumi.Bool(requireUppercase),
		RequireLowercase:              pulumi.Bool(requireLowercase),
		RequireNumbers:                pulumi.Bool(requireNumbers),
		RequireSymbols:                pulumi.Bool(requireSymbols),
		TemporaryPasswordValidityDays: pulumi.Int(tempPasswordDays),
	}

	// ── 2. MFA configuration ───────────────────────────────────────────────
	mfaConfig := "OFF"
	var softwareTokenMfa *cognito.UserPoolSoftwareTokenMfaConfigurationArgs
	var smsConfig *cognito.UserPoolSmsConfigurationArgs

	if args.Mfa != nil && args.Mfa.Mode != "" {
		mfaConfig = args.Mfa.Mode
	}

	if args.Mfa != nil {
		for _, method := range args.Mfa.Methods {
			switch method {
			case "TOTP":
				softwareTokenMfa = &cognito.UserPoolSoftwareTokenMfaConfigurationArgs{
					Enabled: pulumi.Bool(true),
				}
			case "SMS":
				if args.Mfa.SnsCallerArn != "" {
					smsConfig = &cognito.UserPoolSmsConfigurationArgs{
						ExternalId:   pulumi.String(fmt.Sprintf("%s-external", name)),
						SnsCallerArn: pulumi.String(args.Mfa.SnsCallerArn),
						SnsRegion:    pulumi.String(region),
					}
				} else {
					// Auto-create a least-privilege IAM role for SNS SMS publishing.
					snsRole, err := createCognitoSnsRole(ctx, name, stage, stageId, comp)
					if err != nil {
						return nil, fmt.Errorf("CognitoUserPool %q: failed to create SNS IAM role for SMS MFA: %w", name, err)
					}
					smsConfig = &cognito.UserPoolSmsConfigurationArgs{
						ExternalId:   pulumi.String(fmt.Sprintf("%s-external", name)),
						SnsCallerArn: snsRole.Arn,
						SnsRegion:    pulumi.String(region),
					}
				}
			}
		}
	}

	usernameAttrs := pulumi.StringArray{pulumi.String("email")}
	if args.Attributes != nil && len(args.Attributes.UsernameAttributes) > 0 {
		usernameAttrs = make(pulumi.StringArray, len(args.Attributes.UsernameAttributes))
		for i, a := range args.Attributes.UsernameAttributes {
			usernameAttrs[i] = pulumi.String(a)
		}
	}

	autoVerifiedAttrs := pulumi.StringArray{pulumi.String("email")}

	var emailConfig *cognito.UserPoolEmailConfigurationArgs
	if args.EmailConfiguration != nil && args.EmailConfiguration.SesFromAddress != "" {
		emailConfig = &cognito.UserPoolEmailConfigurationArgs{
			EmailSendingAccount: pulumi.String("DEVELOPER"),
			FromEmailAddress:    pulumi.String(args.EmailConfiguration.SesFromAddress),
		}
		if args.EmailConfiguration.ReplyToAddress != "" {
			emailConfig.ReplyToEmailAddress = pulumi.String(args.EmailConfiguration.ReplyToAddress)
		}
	}

	var schemaAttrs cognito.UserPoolSchemaArray
	if args.Attributes != nil {
		for _, ca := range args.Attributes.CustomAttributes {
			attrType := ca.Type
			if attrType == "" {
				attrType = "String"
			}
			mutable := true
			if !ca.Mutable {
				mutable = ca.Mutable
			}
			schemaAttrs = append(schemaAttrs, &cognito.UserPoolSchemaArgs{
				Name:                   pulumi.String(ca.Name),
				AttributeDataType:      pulumi.String(attrType),
				Mutable:                pulumi.Bool(mutable),
				DeveloperOnlyAttribute: pulumi.Bool(false),
			})
		}
	}

	// ── 6. Build the user pool ─────────────────────────────────────────────
	poolMap := pulumi.Map{
		"name": pulumi.String(physicalName),
		// Tier 1: deletion protection always on.
		"deletionProtection": pulumi.String("ACTIVE"),
		"passwordPolicy":     passwordPolicyArg,
		"mfaConfiguration":   pulumi.String(mfaConfig),
		// Tier 1: account recovery via email by default.
		"accountRecoverySetting": pulumi.Map{
			"recoveryMechanisms": pulumi.Array{
				pulumi.Map{
					"name":     pulumi.String("verified_email"),
					"priority": pulumi.Int(1),
				},
			},
		},
		"usernameAttributes":     usernameAttrs,
		"autoVerifiedAttributes": autoVerifiedAttrs,
		"usernameConfiguration": pulumi.Map{
			// Case-insensitive usernames prevent duplicate accounts.
			"caseSensitive": pulumi.Bool(false),
		},
		"tags": pulumi.StringMap{
			"ManagedBy": pulumi.String("anvil"),
			"Stage":     pulumi.String(stage),
		},
	}

	if softwareTokenMfa != nil {
		poolMap["softwareTokenMfaConfiguration"] = softwareTokenMfa
	}
	if smsConfig != nil {
		poolMap["smsConfiguration"] = smsConfig
	}
	if emailConfig != nil {
		poolMap["emailConfiguration"] = emailConfig
	}
	if len(schemaAttrs) > 0 {
		poolMap["schemas"] = schemaAttrs
	}

	poolProps := transform.MergeTransform(args.Transform["userPool"], poolMap)

	pool := &cognito.UserPool{}
	if err := ctx.RegisterResource("aws:cognito/userPool:UserPool", name, poolProps, pool, pulumi.Parent(comp)); err != nil {
		return nil, err
	}

	// ── 7. Identity providers ──────────────────────────────────────────────
	// Build providers before the app client so we can reference their names
	// in supportedIdentityProviders.
	// DependsOn them, not just the pool. Prevents a race where the client
	// references a provider name before the provider resource exists in AWS.
	var idpNames []string
	var idpResources []pulumi.Resource
	idpResources = append(idpResources, pool)

	for i, idp := range args.IdentityProviders {
		idp := idp
		providerName := idp.Name
		if providerName == "" {
			providerName = idp.Type
		}

		details, err := buildIdpDetails(idp)
		if err != nil {
			return nil, fmt.Errorf("CognitoUserPool %q: identity provider %d (%s): %w", name, i, idp.Type, err)
		}

		attrMapping := pulumi.StringMap{}
		for k, v := range idp.AttributeMapping {
			attrMapping[k] = pulumi.String(v)
		}
		// Default: always map email if not explicitly provided.
		if _, ok := idp.AttributeMapping["email"]; !ok {
			attrMapping["email"] = pulumi.String("email")
		}

		idpResource := &cognito.IdentityProvider{}
		if err := ctx.RegisterResource("aws:cognito/identityProvider:IdentityProvider",
			fmt.Sprintf("%s-idp-%d", name, i),
			pulumi.Map{
				"userPoolId":       pool.ID(),
				"providerName":     pulumi.String(providerName),
				"providerType":     pulumi.String(idp.Type),
				"providerDetails":  details,
				"attributeMapping": attrMapping,
			},
			idpResource,
			pulumi.Parent(comp),
			pulumi.DependsOn([]pulumi.Resource{pool}),
		); err != nil {
			return nil, fmt.Errorf("CognitoUserPool %q: failed to create identity provider %s: %w", name, providerName, err)
		}

		idpNames = append(idpNames, providerName)
		idpResources = append(idpResources, idpResource)
	}

	// ── 8. App client ──────────────────────────────────────────────────────
	oauthFlows := pulumi.StringArray{pulumi.String("code")}
	oauthScopes := pulumi.StringArray{
		pulumi.String("openid"),
		pulumi.String("email"),
		pulumi.String("profile"),
	}
	generateSecret := false
	accessTokenValidity := 1
	idTokenValidity := 1
	refreshTokenValidity := 30
	enableOAuth := false

	// Supported IdPs for the client: always include COGNITO (native users).
	supportedIdPs := pulumi.StringArray{pulumi.String("COGNITO")}

	var callbackUrls pulumi.StringArray
	var logoutUrls pulumi.StringArray

	if args.AppClient != nil {
		ac := args.AppClient
		generateSecret = ac.GenerateSecret

		if len(ac.OAuthFlows) > 0 {
			oauthFlows = make(pulumi.StringArray, len(ac.OAuthFlows))
			for i, f := range ac.OAuthFlows {
				oauthFlows[i] = pulumi.String(f)
			}
		}
		if len(ac.OAuthScopes) > 0 {
			oauthScopes = make(pulumi.StringArray, len(ac.OAuthScopes))
			for i, s := range ac.OAuthScopes {
				oauthScopes[i] = pulumi.String(s)
			}
		}
		if len(ac.CallbackUrls) > 0 {
			enableOAuth = true
			callbackUrls = make(pulumi.StringArray, len(ac.CallbackUrls))
			for i, u := range ac.CallbackUrls {
				callbackUrls[i] = pulumi.String(u)
			}
		}
		if len(ac.LogoutUrls) > 0 {
			logoutUrls = make(pulumi.StringArray, len(ac.LogoutUrls))
			for i, u := range ac.LogoutUrls {
				logoutUrls[i] = pulumi.String(u)
			}
		}
		if ac.TokenValidity != nil {
			tv := ac.TokenValidity
			if tv.AccessTokenValidity > 0 {
				accessTokenValidity = tv.AccessTokenValidity
			}
			if tv.IdTokenValidity > 0 {
				idTokenValidity = tv.IdTokenValidity
			}
			if tv.RefreshTokenValidity > 0 {
				refreshTokenValidity = tv.RefreshTokenValidity
			}
		}
		if len(ac.SupportedIdentityProviders) > 0 {
			supportedIdPs = make(pulumi.StringArray, len(ac.SupportedIdentityProviders))
			for i, idpName := range ac.SupportedIdentityProviders {
				supportedIdPs[i] = pulumi.String(idpName)
			}
		}
	}

	// If identity providers exist, OAuth must be enabled for social sign-in.
	// Auto-append discovered IdP names when the caller hasn't overridden the list.
	if len(idpNames) > 0 {
		enableOAuth = true
		if args.AppClient == nil || len(args.AppClient.SupportedIdentityProviders) == 0 {
			supportedIdPs = pulumi.StringArray{pulumi.String("COGNITO")}
			for _, n := range idpNames {
				supportedIdPs = append(supportedIdPs, pulumi.String(n))
			}
		}
	}

	clientMap := pulumi.Map{
		"userPoolId":                      pool.ID(),
		"generateSecret":                  pulumi.Bool(generateSecret),
		"allowedOauthFlowsUserPoolClient": pulumi.Bool(enableOAuth),
		"allowedOauthFlows":               oauthFlows,
		"allowedOauthScopes":              oauthScopes,
		"supportedIdentityProviders":      supportedIdPs,
		"accessTokenValidity":             pulumi.Int(accessTokenValidity),
		"idTokenValidity":                 pulumi.Int(idTokenValidity),
		"refreshTokenValidity":            pulumi.Int(refreshTokenValidity),
		"tokenValidityUnits": pulumi.Map{
			"accessToken":  pulumi.String("hours"),
			"idToken":      pulumi.String("hours"),
			"refreshToken": pulumi.String("days"),
		},
		// Prevent user existence errors leaking enumerable information.
		"preventUserExistenceErrors": pulumi.String("ENABLED"),
	}
	if len(callbackUrls) > 0 {
		clientMap["callbackUrls"] = callbackUrls
	}
	if len(logoutUrls) > 0 {
		clientMap["logoutUrls"] = logoutUrls
	}

	clientProps := transform.MergeTransform(args.Transform["userPoolClient"], clientMap)
	client := &cognito.UserPoolClient{}
	// FIX #4: DependsOn all IdP resources, not just the pool.
	if err := ctx.RegisterResource("aws:cognito/userPoolClient:UserPoolClient", name+"-client", clientProps, client,
		pulumi.Parent(comp),
		pulumi.DependsOn(idpResources),
	); err != nil {
		return nil, err
	}

	// ── 9. Hosted UI domain ────────────────────────────────────────────────
	hostedUiDomain := pulumi.String("").ToStringOutput()
	cloudFrontDomain := pulumi.String("").ToStringOutput()

	if args.HostedUi != nil {
		hui := args.HostedUi

		// FIX #8: default to classic hosted UI (v1). Managed login (v2) has a
		// different visual design and regional feature gaps. Opt in via
		// transform.userPoolDomain.managedLoginVersion = 2.
		domainMap := pulumi.Map{
			"userPoolId":          pool.ID(),
			"domain":              pulumi.String(hui.Domain),
			"managedLoginVersion": pulumi.Int(1),
		}

		if hui.CustomDomain {
			if hui.AcmCertificateArn == "" {
				return nil, fmt.Errorf("CognitoUserPool %q: hostedUi.acmCertificateArn is required when customDomain is true — certificate must be in us-east-1", name)
			}
			domainMap["customDomainConfig"] = pulumi.Map{
				"certificateArn": pulumi.String(hui.AcmCertificateArn),
			}
		}

		domainProps := transform.MergeTransform(args.Transform["userPoolDomain"], domainMap)
		domainRes := &cognito.UserPoolDomain{}
		if err := ctx.RegisterResource("aws:cognito/userPoolDomain:UserPoolDomain", name+"-domain", domainProps, domainRes,
			pulumi.Parent(comp),
			pulumi.DependsOn([]pulumi.Resource{pool, client}),
		); err != nil {
			return nil, err
		}

		if hui.CustomDomain {
			hostedUiDomain = pulumi.Sprintf("https://%s", hui.Domain)
			// FIX #5: expose the CloudFront distribution domain so the caller can
			// complete DNS setup. Without this, custom domain wiring is impossible.
			cloudFrontDomain = domainRes.CloudfrontDistribution
		} else {
			hostedUiDomain = pulumi.Sprintf("https://%s.auth.%s.amazoncognito.com", hui.Domain, region)
		}
	}

	// ── 10. Wire outputs ───────────────────────────────────────────────────
	comp.UserPoolId = pool.ID().ToStringOutput()
	comp.UserPoolArn = pool.Arn
	comp.AppClientId = client.ID().ToStringOutput()
	comp.HostedUiDomain = hostedUiDomain
	comp.CloudFrontDomain = cloudFrontDomain
	comp.Endpoint = pool.ID().ToStringOutput().ApplyT(func(poolId string) string {
		return fmt.Sprintf("https://cognito-idp.%s.amazonaws.com/%s", region, poolId)
	}).(pulumi.StringOutput)

	// FIX #3: mark appClientSecret as a Pulumi secret — encrypted in state,
	// never stored or displayed in plaintext.
	if generateSecret {
		comp.AppClientSecret = pulumi.ToSecret(client.ClientSecret).(pulumi.StringOutput)
	} else {
		comp.AppClientSecret = pulumi.String("").ToStringOutput()
	}

	ctx.RegisterResourceOutputs(comp, pulumi.Map{
		"userPoolId":       pool.ID(),
		"userPoolArn":      pool.Arn,
		"appClientId":      client.ID(),
		"appClientSecret":  comp.AppClientSecret,
		"hostedUiDomain":   hostedUiDomain,
		"cloudFrontDomain": cloudFrontDomain,
		"endpoint":         comp.Endpoint,
	})

	return comp, nil
}

// ── Identity provider detail builders ─────────────────────────────────────
//
// buildIdpDetails returns the providerDetails map for a given IdP type.
// Adding a new provider = one new case here. No schema changes required.

func buildIdpDetails(idp IdentityProviderArgs) (pulumi.Map, error) {
	switch idp.Type {
	case "Google":
		if idp.ClientId == "" || idp.ClientSecret == "" {
			return nil, fmt.Errorf("clientId and clientSecret are required for Google")
		}
		return pulumi.Map{
			"client_id":        pulumi.String(idp.ClientId),
			"client_secret":    pulumi.String(idp.ClientSecret),
			"authorize_scopes": pulumi.String("profile email openid"),
		}, nil

	case "Facebook":
		if idp.ClientId == "" || idp.ClientSecret == "" {
			return nil, fmt.Errorf("clientId and clientSecret are required for Facebook")
		}
		return pulumi.Map{
			"client_id":        pulumi.String(idp.ClientId),
			"client_secret":    pulumi.String(idp.ClientSecret),
			"authorize_scopes": pulumi.String("public_profile,email"),
			"api_version":      pulumi.String("v17.0"),
		}, nil

	case "LoginWithAmazon":
		if idp.ClientId == "" || idp.ClientSecret == "" {
			return nil, fmt.Errorf("clientId and clientSecret are required for LoginWithAmazon")
		}
		return pulumi.Map{
			"client_id":        pulumi.String(idp.ClientId),
			"client_secret":    pulumi.String(idp.ClientSecret),
			"authorize_scopes": pulumi.String("profile postal_code"),
		}, nil

	case "SignInWithApple":
		if idp.ClientId == "" || idp.ClientSecret == "" {
			return nil, fmt.Errorf("clientId and clientSecret are required for SignInWithApple")
		}
		return pulumi.Map{
			"client_id":        pulumi.String(idp.ClientId),
			"client_secret":    pulumi.String(idp.ClientSecret),
			"authorize_scopes": pulumi.String("name email"),
		}, nil

	case "OIDC":
		if idp.ClientId == "" || idp.ClientSecret == "" {
			return nil, fmt.Errorf("clientId and clientSecret are required for OIDC")
		}
		if idp.OidcIssuer == "" {
			return nil, fmt.Errorf("oidcIssuer is required for OIDC providers")
		}
		return pulumi.Map{
			"client_id":        pulumi.String(idp.ClientId),
			"client_secret":    pulumi.String(idp.ClientSecret),
			"oidc_issuer":      pulumi.String(idp.OidcIssuer),
			"authorize_scopes": pulumi.String("openid email profile"),
		}, nil

	case "SAML":
		if idp.MetadataUrl == "" && idp.MetadataContent == "" {
			return nil, fmt.Errorf("metadataUrl or metadataContent is required for SAML providers")
		}
		details := pulumi.Map{}
		if idp.MetadataUrl != "" {
			details["MetadataURL"] = pulumi.String(idp.MetadataUrl)
		} else {
			details["MetadataFile"] = pulumi.String(idp.MetadataContent)
		}
		return details, nil

	default:
		return nil, fmt.Errorf("unsupported identity provider type %q — valid types: Google, Facebook, LoginWithAmazon, SignInWithApple, OIDC, SAML", idp.Type)
	}
}

// ── SNS IAM role for SMS MFA ───────────────────────────────────────────────
//
// createCognitoSnsRole creates a least-privilege IAM role for Cognito to
// publish SMS via SNS. Only grants sns:Publish — not AmazonSNSFullAccess.
// FIX #3 (SNS scope): inline policy instead of the AWS managed full-access policy.

func createCognitoSnsRole(ctx *pulumi.Context, name, stage, stageId string, parent pulumi.Resource) (*iam.Role, error) {
	assumeRolePolicy := `{
		"Version": "2012-10-17",
		"Statement": [{
			"Effect": "Allow",
			"Principal": { "Service": "cognito-idp.amazonaws.com" },
			"Action": "sts:AssumeRole"
		}]
	}`

	roleName := provider.PhysicalName(stage, name, "sms-role", stageId)

	role := &iam.Role{}
	if err := ctx.RegisterResource("aws:iam/role:Role", name+"-sms-role", pulumi.Map{
		"name":             pulumi.String(roleName),
		"assumeRolePolicy": pulumi.String(assumeRolePolicy),
		"tags": pulumi.StringMap{
			"ManagedBy": pulumi.String("anvil"),
			"Stage":     pulumi.String(stage),
		},
	}, role, pulumi.Parent(parent)); err != nil {
		return nil, err
	}

	// Scoped inline policy: sns:Publish only.
	type statement struct {
		Effect   string   `json:"Effect"`
		Action   []string `json:"Action"`
		Resource string   `json:"Resource"`
	}
	type doc struct {
		Version   string      `json:"Version"`
		Statement []statement `json:"Statement"`
	}
	policyJSON, err := json.Marshal(doc{
		Version: "2012-10-17",
		Statement: []statement{{
			Effect:   "Allow",
			Action:   []string{"sns:Publish"},
			Resource: "*",
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal SNS publish policy: %w", err)
	}

	if err := ctx.RegisterResource("aws:iam/rolePolicy:RolePolicy", name+"-sms-policy", pulumi.Map{
		"role":   role.Name,
		"policy": pulumi.String(string(policyJSON)),
	}, &iam.RolePolicy{}, pulumi.Parent(parent)); err != nil {
		return nil, err
	}

	return role, nil
}
