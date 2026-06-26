# sdk/python/anvil_cloud/grants.py
# Hand-written. Backed up/restored during gen-sdk like app.py and block.py.
#
# Provides the runtime grant execution for all resource grant methods.

from typing import List, Optional, Protocol

import pulumi
import pulumi_aws as aws


class GrantTarget(Protocol):
    """Any Anvil compute resource that can receive IAM permissions."""

    def grant_name(self) -> str: ...
    def grant_role_arn(self) -> pulumi.Output[str]: ...


class GrantOptions:
    """Optional metadata for grant methods."""

    def __init__(self, justification: Optional[str] = None):
        self.justification = justification


def grant_resource_name(resource) -> str:
    """Return the logical name a component was constructed with.

    The name is stashed on the instance by the construction wrapper (stack.py),
    so grant methods can build synchronous child-resource names without the
    generated SDK class declaring the attribute itself.
    """
    name = getattr(resource, "_anvil_name", None)
    if not isinstance(name, str):
        raise ValueError(
            "Anvil grant: could not resolve the resource name. Construct "
            "components via the anvil namespace (e.g. anvil.aws.Bucket(...)) "
            "so grants work."
        )
    return name


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
    """
    import json
    import re

    policy_document = pulumi.Output.all(*resource_arns).apply(
        lambda arns: json.dumps({
            "Version": "2012-10-17",
            "Statement": [{
                "Effect": "Allow",
                "Action": actions,
                "Resource": list(arns),
            }],
        })
    )

    role_name = target.grant_role_arn().apply(
        lambda arn: arn[arn.rfind("/") + 1:] if "/" in arn else arn
    )

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
    """Builds the list of ARNs for a grant based on a base ARN and optional path scoping."""
    arns: List[pulumi.Output[str]] = [base_arn]

    if not paths:
        arns.append(base_arn.apply(lambda arn: f"{arn}/*"))
    else:
        for p in paths:
            arns.append(base_arn.apply(lambda arn, _p=p: f"{arn}/{_p}"))

    return arns
