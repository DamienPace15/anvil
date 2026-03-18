package function

import (
	"github.com/pulumi/pulumi-azure/sdk/v6/go/azure/appservice"
	"github.com/pulumi/pulumi-azure/sdk/v6/go/azure/storage"
	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type FunctionArgs struct {
	Name              string                            `pulumi:"name"`
	Location          string                            `pulumi:"location"`
	ResourceGroupName string                            `pulumi:"resourceGroupName"`
	Runtime           string                            `pulumi:"runtime,optional"`
	Transform         map[string]map[string]interface{} `pulumi:"transform,optional"`
}

type Function struct {
	pulumi.ResourceState

	Endpoint     pulumi.StringOutput `pulumi:"endpoint"`
	FunctionName pulumi.StringOutput `pulumi:"functionName"`
}

func (f *Function) Annotate(a infer.Annotator) {
	a.Describe(&f, "An Anvil-managed Azure Function App with sensible defaults.")
}

func NewFunction(ctx *pulumi.Context, name string, args FunctionArgs, opts ...pulumi.ResourceOption) (*Function, error) {
	f := &Function{}
	err := ctx.RegisterComponentResource(p.GetTypeToken(ctx), name, f, opts...)
	if err != nil {
		return nil, err
	}

	runtime := args.Runtime
	if runtime == "" {
		runtime = "node"
	}

	// 1. Storage Account (required by Azure Functions)
	sa, err := storage.NewAccount(ctx, name+"-sa", &storage.AccountArgs{
		ResourceGroupName:      pulumi.String(args.ResourceGroupName),
		Location:               pulumi.String(args.Location),
		AccountTier:            pulumi.String("Standard"),
		AccountReplicationType: pulumi.String("LRS"),
	}, pulumi.Parent(f))
	if err != nil {
		return nil, err
	}

	// 2. Service Plan (Consumption)
	plan, err := appservice.NewServicePlan(ctx, name+"-plan", &appservice.ServicePlanArgs{
		ResourceGroupName: pulumi.String(args.ResourceGroupName),
		Location:          pulumi.String(args.Location),
		OsType:            pulumi.String("Linux"),
		SkuName:           pulumi.String("Y1"),
	}, pulumi.Parent(f))
	if err != nil {
		return nil, err
	}

	// 3. Linux Function App
	funcApp, err := appservice.NewLinuxFunctionApp(ctx, name, &appservice.LinuxFunctionAppArgs{
		ResourceGroupName:       pulumi.String(args.ResourceGroupName),
		Location:                pulumi.String(args.Location),
		ServicePlanId:           plan.ID(),
		StorageAccountName:      sa.Name,
		StorageAccountAccessKey: sa.PrimaryAccessKey,
		SiteConfig:              &appservice.LinuxFunctionAppSiteConfigArgs{},
	}, pulumi.Parent(f))
	if err != nil {
		return nil, err
	}

	f.Endpoint = funcApp.DefaultHostname
	f.FunctionName = funcApp.Name

	ctx.RegisterResourceOutputs(f, pulumi.Map{
		"endpoint":     funcApp.DefaultHostname,
		"functionName": funcApp.Name,
	})

	return f, nil
}
