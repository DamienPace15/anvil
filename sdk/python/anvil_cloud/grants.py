# sdk/python/anvil_cloud/grants.py
# Hand-written. Backed up/restored during gen-sdk like app.py and block.py.
#
# Provides the runtime grant execution for all resource grant methods.

from typing import List, Optional, Protocol

import pulumi
import pulumi_aws as aws


class GrantTarget(Protocol):
    """Any Anvil compute resource that can receive IAM permissions."""

    def grant_name(self) -> str:
        """The logical resource name passed to the constructor."""
        ...

    def grant_role_arn(self) -> pulumi.Output[str]:
        """The ARN of the IAM execution role attached to this compute resource."""
        ...


class GrantOptions:
    """Optional metadata for grant methods."""

    def __init__(self, justification: Optional[str] = None):
        """
        Args:
            justification: Documents why this grant is needed.
                Stored as a tag on the generated IAM policy resource for audit purposes.
        """
        self.justification = justification


def create_grant(
    parent: pulumi.Resource,
    name: str,
    target: GrantTarget,
    actions: List[str],
    resource_arns: List[pulumi.Output[str]],
    opts: Optional[GrantOptions] = None,
) -> None:
    """
    Creates a scoped IAM RolePolicy granting the specified actions on the
    specified resource ARNs to the target's execution role.

    This is the core engine that all resource-specific grant methods delegate to.
    """
    import json
    import re

    policy_document = pulumi.Output.all(*resource_arns).apply(
        lambda arns: json.dumps(
            {
                "Version": "2012-10-17",
                "Statement": [
                    {
                        "Effect": "Allow",
                        "Action": actions,
                        "Resource": list(arns),
                    }
                ],
            }
        )
    )

    # Extract role name from ARN (everything after the last "/")
    role_name = target.grant_role_arn().apply(
        lambda arn: arn[arn.rfind("/") + 1 :] if "/" in arn else arn
    )

    # Encode justification in resource name for audit trail
    policy_name = name
    if opts and opts.justification:
        suffix = re.sub(r"[^a-z0-9]+", "-", opts.justification.lower())[:40]
        policy_name = f"{name}-{suffix}"

    aws.iam.RolePolicy(
        policy_name,
        role=role_name,
        policy=policy_document,
        opts=pulumi.ResourceOptions(parent=parent),
    )


def build_resource_arns(
    base_arn: pulumi.Output[str],
    paths: Optional[List[str]] = None,
) -> List[pulumi.Output[str]]:
    """
    Builds the list of ARNs for a grant based on a base ARN and optional path scoping.

    - No paths: grants access to the entire resource (base_arn + base_arn/*)
    - With paths: grants access to base_arn (for list operations) + each scoped path
    """
    arns: List[pulumi.Output[str]] = [base_arn]

    if not paths:
        arns.append(base_arn.apply(lambda arn: f"{arn}/*"))
    else:
        for p in paths:
            arns.append(base_arn.apply(lambda arn, _p=p: f"{arn}/{_p}"))

    return arns