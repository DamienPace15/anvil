package apigateway

import (
	"reflect"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type noopMocks struct{}

func (noopMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	return args.Name + "_id", args.Inputs, nil
}

func (noopMocks) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return resource.PropertyMap{}, nil
}

func TestJwtAuthorizerRefRoundTrip(t *testing.T) {
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		ref := EncodeJwtAuthorizerRef(pulumi.String("https://tenant.auth0.com/"), []string{"https://api.example.com"})
		ref.ApplyT(func(r string) error {
			cfg, err := decodeJwtAuthorizerRef(r)
			if err != nil {
				t.Fatalf("decode failed: %v", err)
			}
			if cfg.Issuer != "https://tenant.auth0.com/" {
				t.Errorf("issuer = %q", cfg.Issuer)
			}
			if !reflect.DeepEqual(cfg.Audience, []string{"https://api.example.com"}) {
				t.Errorf("audience = %v", cfg.Audience)
			}
			return nil
		})
		return nil
	}, pulumi.WithMocks("project", "stack", noopMocks{}))
	if err != nil {
		t.Fatal(err)
	}
}

func TestDecodeJwtAuthorizerRefRejectsRawId(t *testing.T) {
	if _, err := decodeJwtAuthorizerRef("abc123"); err == nil {
		t.Fatal("expected error for a raw authorizer ID")
	}
}
