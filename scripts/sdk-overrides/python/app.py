"""
Anvil App class — entry point for infrastructure programs.

Wraps Pulumi's runtime so users never import pulumi directly.
"""

import pulumi
import pulumi_aws as aws
import pulumi_gcp as gcp
from typing import Any, Callable, Dict, Optional


class Context:
    """Context passed to the App's run callback."""

    def __init__(self, stage: str, project: str, providers: Dict[str, pulumi.ProviderResource]):
        self._stage = stage
        self._project = project
        self._providers = providers

    @property
    def stage(self) -> str:
        """The current deployment stage (e.g. "dev", "staging", "prod", or OS username)."""
        return self._stage

    @property
    def project(self) -> str:
        """The project name from anvil.yaml."""
        return self._project

    @property
    def providers(self) -> Dict[str, pulumi.ProviderResource]:
        """Named providers, keyed by config name (e.g. "aws", "aws.us", "gcp.eu")."""
        return self._providers

    def export(self, name: str, value: Any) -> None:
        """Export a stack output value without importing Pulumi."""
        pulumi.export(name, value)


class App:
    """
    Entry point for an Anvil infrastructure program.

    Wraps Pulumi's runtime so users never import pulumi directly.
    The constructor reads stage/project from Pulumi config, creates providers
    with default tags, and invokes the user's run callback with a Context.

    Example::

        import anvil_cloud as anvil

        def infra(ctx):
            bucket = anvil.aws.Bucket("my-data",
                data_classification="sensitive",
            )
            ctx.export("bucketName", bucket.bucket_name)

        anvil.App(run=infra)
    """

    def __init__(
        self,
        run: Callable[[Context], None],
        defaults: Optional[Dict[str, Any]] = None,
        providers: Optional[Dict[str, Dict[str, Any]]] = None,
        before_deploy: Optional[Callable[[Context], None]] = None,
        after_deploy: Optional[Callable[[Context], None]] = None,
        on_error: Optional[Callable[[Context, Exception], None]] = None,
    ):
        # ── Read config from Pulumi ────────────────────────
        anvil_config = pulumi.Config("anvil")
        stage = anvil_config.require("stage")
        project = pulumi.get_project()

        # ── Build default tags ─────────────────────────────
        auto_tags: Dict[str, str] = {"stage": stage, "project": project}
        user_tags = (defaults or {}).get("tags", {})
        merged_tags: Dict[str, str] = {**auto_tags, **user_tags}

        # ── Create providers ───────────────────────────────
        provider_map: Dict[str, pulumi.ProviderResource] = {}
        default_providers: Dict[str, pulumi.ProviderResource] = {}

        if providers:
            for key, config in providers.items():
                cloud = _get_cloud(key)
                is_default = "." not in key
                provider_name = f"anvil-provider-{key}"

                if cloud == "aws":
                    aws_args: Dict[str, Any] = {
                        "default_tags": aws.ProviderDefaultTagsArgs(tags=merged_tags),
                    }
                    if config.get("region"):
                        aws_args["region"] = config["region"]
                    if config.get("profile"):
                        aws_args["profile"] = config["profile"]
                    if config.get("assume_role"):
                        role = config["assume_role"]
                        aws_args["assume_roles"] = [aws.ProviderAssumeRoleArgs(
                            role_arn=role["role_arn"],
                            session_name=role.get("session_name"),
                            external_id=role.get("external_id"),
                        )]

                    provider = aws.Provider(provider_name, **aws_args)

                elif cloud == "gcp":
                    gcp_args: Dict[str, Any] = {
                        "default_labels": merged_tags,
                    }
                    if config.get("project"):
                        gcp_args["project"] = config["project"]
                    if config.get("region"):
                        gcp_args["region"] = config["region"]
                    if config.get("zone"):
                        gcp_args["zone"] = config["zone"]
                    if config.get("credentials"):
                        gcp_args["credentials"] = config["credentials"]

                    provider = gcp.Provider(provider_name, **gcp_args)

                else:
                    raise ValueError(
                        f'Unknown cloud provider "{cloud}" in providers config key "{key}".\n'
                        f'  Supported prefixes: "aws", "gcp".'
                    )

                provider_map[key] = provider
                if is_default:
                    default_providers[cloud] = provider

        # If no providers configured but defaults.tags is set,
        # create implicit default providers to carry the tags.
        if not providers and defaults and defaults.get("tags"):
            aws_provider = aws.Provider("anvil-provider-aws",
                default_tags=aws.ProviderDefaultTagsArgs(tags=merged_tags),
            )
            provider_map["aws"] = aws_provider
            default_providers["aws"] = aws_provider

            gcp_provider = gcp.Provider("anvil-provider-gcp",
                default_labels=merged_tags,
            )
            provider_map["gcp"] = gcp_provider
            default_providers["gcp"] = gcp_provider

        # ── Register default provider injection ────────────
        if default_providers:
            def _transform(args: pulumi.ResourceTransformationArgs):
                type_parts = args.type_.split(":")
                cloud = None

                if type_parts[0] == "anvil" and len(type_parts) >= 3:
                    cloud = type_parts[1]
                elif len(type_parts) >= 2:
                    cloud = type_parts[0]

                if cloud and cloud in default_providers and not args.opts.provider:
                    return pulumi.ResourceTransformationResult(
                        props=args.props,
                        opts=pulumi.ResourceOptions.merge(
                            args.opts,
                            pulumi.ResourceOptions(provider=default_providers[cloud]),
                        ),
                    )

                return None

            pulumi.runtime.register_stack_transformation(_transform)

        # ── Create Context ─────────────────────────────────
        ctx = Context(stage=stage, project=project, providers=provider_map)

        # ── Execute ────────────────────────────────────────
        try:
            run(ctx)
        except Exception as error:
            raise


def _get_cloud(key: str) -> str:
    """Determine which cloud a provider key belongs to."""
    dot = key.find(".")
    return key if dot == -1 else key[:dot]