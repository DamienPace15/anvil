# Hand-written exports merged into the generated __init__.py.
#
# gen-sdk regenerates __init__.py on every build with no knowledge of the
# hand-written modules in this overlay. fix-sdk.ts appends a single
# `from ._extras import *` to the generated __init__ — everything it pulls
# into the anvil_cloud namespace lives here, as real code (not strings).
from .app import run, App, Context
from .block import Block
from .types import (
    AppConfig,
    DefaultsConfig,
    AwsProviderConfig,
    GcpProviderConfig,
    AssumeRoleConfig,
)
from .grants import GrantTarget, GrantOptions, create_grant, build_resource_arns

# Re-export core Pulumi functions so users never need to import pulumi directly.
from pulumi import export

__all__ = [
    "run",
    "App",
    "Context",
    "Block",
    "AppConfig",
    "DefaultsConfig",
    "AwsProviderConfig",
    "GcpProviderConfig",
    "AssumeRoleConfig",
    "GrantTarget",
    "GrantOptions",
    "create_grant",
    "build_resource_arns",
    "export",
]
