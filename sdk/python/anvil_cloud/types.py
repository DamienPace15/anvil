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

# ── Resource base classes ──────────────────────────────────
from pulumi import (
    ComponentResource,
    ComponentResourceOptions,
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
from pulumi import (
    export,
    get_project,
    get_stack,
    Config,
    output,
    secret,
)

# ── Escape hatch ───────────────────────────────────────────
# For anything not re-exported above, users can access the
# full Pulumi namespace without adding a separate dependency.
import pulumi