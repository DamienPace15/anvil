"""
Re-exports of core Pulumi types so users never need to
import pulumi directly for standard operations.

Example::

    import anvil_cloud as anvil

    class MyComponent(anvil.ComponentResource):
        def __init__(self, name: str, opts: anvil.ResourceOptions | None = None):
            super().__init__("my:component:MyComponent", name, {}, opts)

    value: anvil.Output[str] = anvil.Output.from_input("hello")
"""

from typing import Any, Callable, Dict, List, Literal, Optional

# ── Resource base classes ──────────────────────────────────
# Note: Python Pulumi has no ``ComponentResourceOptions`` (that is a TypeScript
# concept) — component resources use ``ResourceOptions``.
from pulumi import (
    ComponentResource,
    CustomResource,
    ResourceOptions,
    ProviderResource,
)

# ── Input/Output types ─────────────────────────────────────
from pulumi import (
    Output,
    Input,
    Inputs,
)

# ── Utility functions ──────────────────────────────────────
# Note: Python Pulumi has no top-level ``secret`` — it is ``Output.secret(...)``.
from pulumi import (
    export,
    get_project,
    get_stack,
    Config,
    output,
)

#: Mark a value secret. Aliased to the Output staticmethod for API parity with
#: the TypeScript SDK's ``anvil.secret``.
secret = Output.secret

# ── Escape hatch ───────────────────────────────────────────
# For anything not re-exported above, users can access the
# full Pulumi namespace without adding a separate dependency.
import pulumi


# ── Compliance ─────────────────────────────────────────────

#: Supported compliance frameworks.
#: Mirrors the Go ComplianceFramework constants in
#: provider/internal/shared/compliance.go — keep in sync.
ComplianceFramework = Literal[
    "soc2",
    "iso27001",
    "cis",
    "pci-dss",
    "hipaa",
    "fedramp",
    "hitrust",
    "gdpr",
    "soc1",
    "irap",
    "nist-csf",
    "csa-star",
]


# ── Config Classes ─────────────────────────────────────────
# Typed configuration for App, providing intellisense and validation.


class DefaultsConfig:
    """Default configuration applied to all resources."""

    def __init__(
        self,
        tags: Optional[Dict[str, str]] = None,
        compliance: Optional[List[ComplianceFramework]] = None,
    ):
        self.tags = tags or {}
        self.compliance = compliance or []


class AssumeRoleConfig:
    """Configuration for assuming an IAM role."""

    def __init__(
        self,
        role_arn: str,
        session_name: Optional[str] = None,
        external_id: Optional[str] = None,
    ):
        self.role_arn = role_arn
        self.session_name = session_name
        self.external_id = external_id


class AwsProviderConfig:
    """Configuration for an AWS provider."""

    def __init__(
        self,
        region: Optional[str] = None,
        profile: Optional[str] = None,
        assume_role: Optional[AssumeRoleConfig] = None,
    ):
        self.region = region
        self.profile = profile
        self.assume_role = assume_role


class GcpProviderConfig:
    """Configuration for a GCP provider."""

    def __init__(
        self,
        project: Optional[str] = None,
        region: Optional[str] = None,
        zone: Optional[str] = None,
        credentials: Optional[str] = None,
    ):
        self.project = project
        self.region = region
        self.zone = zone
        self.credentials = credentials


class AppConfig:
    """
    Typed configuration for anvil.run().

    Example::

        anvil.run(anvil.AppConfig(
            defaults=anvil.DefaultsConfig(
                tags={"team": "platform"},
                compliance=["soc2", "iso27001"],
            ),
            aws_providers={
                "aws": anvil.AwsProviderConfig(region="ap-southeast-2"),
            },
            run=main,
        ))
    """

    def __init__(
        self,
        run: Callable[["Context"], None],
        defaults: Optional[DefaultsConfig] = None,
        aws_providers: Optional[Dict[str, AwsProviderConfig]] = None,
        gcp_providers: Optional[Dict[str, GcpProviderConfig]] = None,
        before_deploy: Optional[Callable[["Context"], None]] = None,
        after_deploy: Optional[Callable[["Context"], None]] = None,
        on_error: Optional[Callable[["Context", Exception], None]] = None,
    ):
        self.run = run
        self.defaults = defaults
        self.aws_providers = aws_providers
        self.gcp_providers = gcp_providers
        self.before_deploy = before_deploy
        self.after_deploy = after_deploy
        self.on_error = on_error