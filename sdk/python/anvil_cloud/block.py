"""Block base class for organisational grouping of Anvil resources."""

from typing import Any, Mapping, Optional

import pulumi

class Block(pulumi.ComponentResource):
    """An optional organisational grouping for Anvil resources.

    Resources created inside a Block constructor are automatically parented
    to the Block via Pulumi's built-in auto-parenting. Blocks can expose
    public properties (outputs) for cross-Block references.

    Blocks are purely optional — flat top-level resources work identically.

    Example::

        import anvil_cloud as anvil

        class Storage(anvil.Block):
            bucket_name: pulumi.Output[str]

            def __init__(self, name: str, args: dict | None = None,
                         opts: pulumi.ResourceOptions | None = None):
                super().__init__(name, args, opts)

                bucket = anvil.aws.Bucket("data", ...)
                self.bucket_name = bucket.bucket_name

                self.register_outputs({"bucket_name": self.bucket_name})
    """

    def __init__(
        self,
        name: str,
        args: Optional[Mapping[str, Any]] = None,
        opts: Optional[pulumi.ResourceOptions] = None,
    ) -> None:
        """Create a new Block.

        Args:
            name: The unique name of this Block within the stack.
            args: Optional arguments (passed through for subclass use).
            opts: Standard Pulumi ResourceOptions (aliases, providers, etc.).
        """
        # Type URN uses "anvil:block:" prefix so Blocks are identifiable in state
        super().__init__(
            f"anvil:block:{name}",
            name,
            args or {},
            opts,
        )