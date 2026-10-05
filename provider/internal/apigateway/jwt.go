package apigateway

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/apigatewayv2"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// API Gateway authorizers belong to exactly one API, so OAuthAuthorizer and
// CognitoAuth cannot create one up front. Instead they emit a JWT authorizer
// reference — the issuer and audience encoded as a string — which HttpApi
// decodes to create the real authorizer against its own API.

const jwtAuthorizerRefPrefix = "anvil-jwt:"

type jwtAuthorizerConfig struct {
	Issuer   string   `json:"issuer"`
	Audience []string `json:"audience"`
}

// EncodeJwtAuthorizerRef builds the reference string passed to HttpApi defaultAuthorizerId.
func EncodeJwtAuthorizerRef(issuer pulumi.StringInput, audience []string) pulumi.StringOutput {
	return issuer.ToStringOutput().ApplyT(func(iss string) (string, error) {
		b, err := json.Marshal(jwtAuthorizerConfig{Issuer: iss, Audience: audience})
		if err != nil {
			return "", err
		}
		return jwtAuthorizerRefPrefix + string(b), nil
	}).(pulumi.StringOutput)
}

func decodeJwtAuthorizerRef(ref string) (jwtAuthorizerConfig, error) {
	var cfg jwtAuthorizerConfig
	if !strings.HasPrefix(ref, jwtAuthorizerRefPrefix) {
		return cfg, fmt.Errorf("defaultAuthorizerId must be the authorizerId output of an OAuthAuthorizer or CognitoAuth component, got %q", ref)
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(ref, jwtAuthorizerRefPrefix)), &cfg); err != nil {
		return cfg, fmt.Errorf("invalid authorizer reference: %w", err)
	}
	return cfg, nil
}

// NewJwtAuthorizer creates the API Gateway JWT authorizer described by ref on the given API.
// API Gateway fetches JWKS from {issuer}/.well-known/jwks.json and validates
// signature, issuer, audience, and expiry on every request.
func NewJwtAuthorizer(ctx *pulumi.Context, name string, apiId pulumi.IDOutput, ref pulumi.StringInput, parent pulumi.Resource) (*apigatewayv2.Authorizer, error) {
	refOut := ref.ToStringOutput()
	issuer := refOut.ApplyT(func(r string) (string, error) {
		cfg, err := decodeJwtAuthorizerRef(r)
		return cfg.Issuer, err
	}).(pulumi.StringOutput)
	audience := refOut.ApplyT(func(r string) ([]string, error) {
		cfg, err := decodeJwtAuthorizerRef(r)
		return cfg.Audience, err
	}).(pulumi.StringArrayOutput)

	return apigatewayv2.NewAuthorizer(ctx, name, &apigatewayv2.AuthorizerArgs{
		ApiId:          apiId,
		AuthorizerType: pulumi.String("JWT"),
		// Standard Bearer token location. All major OIDC providers use this.
		IdentitySources: pulumi.StringArray{pulumi.String("$request.header.Authorization")},
		JwtConfiguration: &apigatewayv2.AuthorizerJwtConfigurationArgs{
			Issuer:    issuer,
			Audiences: audience,
		},
	}, pulumi.Parent(parent))
}
