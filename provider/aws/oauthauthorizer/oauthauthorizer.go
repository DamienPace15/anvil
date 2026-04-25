package oauthauthorizer

import (
	provider "github.com/DamienPace15/anvil/provider/internal/shared"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/apigatewayv2"
	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// ── Args ───────────────────────────────────────────────────────────────────

// OAuthAuthorizerArgs defines the inputs for an Anvil-managed OAuth JWT authorizer.
type OAuthAuthorizerArgs struct {
	// Issuer is the OIDC issuer URL of your identity provider.
	// API Gateway fetches public keys from {issuer}/.well-known/jwks.json to verify tokens.
	// Examples:
	//   Auth0:   "https://your-tenant.auth0.com/"
	//   Clerk:   "https://your-instance.clerk.accounts.dev"
	//   Google:  "https://accounts.google.com"
	Issuer string `pulumi:"issuer"`

	// Audience is the list of intended recipients for the JWT.
	// API Gateway rejects tokens whose 'aud' claim does not match one of these values.
	// Typically your API's client ID registered with the identity provider.
	Audience []string `pulumi:"audience"`
}

// ── Component ──────────────────────────────────────────────────────────────

// OAuthAuthorizer is the Anvil-managed JWT authorizer component resource.
// Works with any OIDC-compliant identity provider — Auth0, Clerk, Google, Okta, etc.
// Pass authorizerId to HttpApi.defaultAuthorizerId to protect your API routes.
type OAuthAuthorizer struct {
	pulumi.ResourceState

	// AuthorizerId is the API Gateway authorizer ID.
	// Pass this to HttpApi defaultAuthorizerId to protect routes.
	AuthorizerId pulumi.StringOutput `pulumi:"authorizerId"`
}

func (o *OAuthAuthorizer) Annotate(a infer.Annotator) {
	a.SetToken("aws", "OAuthAuthorizer")
	a.Describe(&o, "An Anvil-managed JWT authorizer for HTTP API Gateway. Works with any OIDC-compliant identity provider (Auth0, Clerk, Google, Okta, Cognito). API Gateway verifies the JWT signature, issuer, audience, and expiry on every request — your Lambda only runs if the token is valid. Pass authorizerId to HttpApi defaultAuthorizerId.")
}

// NewOAuthAuthorizer creates a new Anvil-managed JWT authorizer.
// The authorizer verifies JWTs issued by the given OIDC provider on every inbound request.
// No Lambda or custom code required — verification is handled natively by API Gateway.
func NewOAuthAuthorizer(ctx *pulumi.Context, name string, args OAuthAuthorizerArgs, opts ...pulumi.ResourceOption) (*OAuthAuthorizer, error) {
	o := &OAuthAuthorizer{}

	provider.NewContext(ctx)

	opts = provider.WithDefault(opts, false)

	if err := ctx.RegisterComponentResource(p.GetTypeToken(ctx), name, o, opts...); err != nil {
		return nil, err
	}

	// Build audience as a pulumi.StringArray.
	audience := make(pulumi.StringArray, len(args.Audience))
	for i, a := range args.Audience {
		audience[i] = pulumi.String(a)
	}

	// API Gateway JWT authorizer. Verification is entirely native —
	// API Gateway fetches JWKS from {issuer}/.well-known/jwks.json and
	// validates signature, issuer, audience, and expiry on every request.
	authorizer, err := apigatewayv2.NewAuthorizer(ctx, name+"-authorizer", &apigatewayv2.AuthorizerArgs{
		// ApiId is intentionally omitted here — the authorizer is created
		// standalone and attached to an HttpApi via authorizerId.
		// This allows one authorizer to be referenced by multiple APIs.
		AuthorizerType: pulumi.String("JWT"),
		IdentitySources: pulumi.StringArray{
			// Standard Bearer token location. All major OIDC providers use this.
			// Override via transform if your provider uses a different header.
			pulumi.String("$request.header.Authorization"),
		},
		JwtConfiguration: &apigatewayv2.AuthorizerJwtConfigurationArgs{
			Issuer:    pulumi.String(args.Issuer),
			Audiences: audience,
		},
	}, pulumi.Parent(o))
	if err != nil {
		return nil, err
	}

	o.AuthorizerId = authorizer.ID().ToStringOutput()

	ctx.RegisterResourceOutputs(o, pulumi.Map{
		"authorizerId": authorizer.ID(),
	})

	return o, nil
}
