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


def create_egress_grant(
    parent: pulumi.Resource,
    name: str,
    security_group_id: pulumi.Output[str],
    internet: bool = False,
    ports: Optional[List[int]] = None,
    all_ports: bool = False,
    cidrs: Optional[List[dict]] = None,
) -> None:
    """
    Creates internet or CIDR-scoped egress SecurityGroupEgressRule(s) on a
    compute resource's dedicated security group.

    Must specify either internet=True or cidrs — not both, not neither.
    """
    import re

    if internet and cidrs:
        raise ValueError(
            f"{name}: grant_egress cannot specify both internet and cidrs — choose one."
        )
    if not internet and not cidrs:
        raise ValueError(
            f"{name}: grant_egress requires either internet=True or cidrs."
        )
    if all_ports and not internet:
        raise ValueError(
            f"{name}: grant_egress all_ports is only valid with internet=True."
        )

    # ── Internet egress ───────────────────────────────────────────────────────
    if internet:
        if all_ports:
            aws.vpc.SecurityGroupEgressRule(
                f"{name}-egress-all",
                security_group_id=security_group_id,
                cidr_ipv4="0.0.0.0/0",
                from_port=0,
                to_port=65535,
                ip_protocol="tcp",
                tags={"ManagedBy": "anvil"},
                opts=pulumi.ResourceOptions(parent=parent),
            )
            return

        resolved_ports = ports if ports else [443]
        for port in resolved_ports:
            aws.vpc.SecurityGroupEgressRule(
                f"{name}-egress-{port}",
                security_group_id=security_group_id,
                cidr_ipv4="0.0.0.0/0",
                from_port=port,
                to_port=port,
                ip_protocol="tcp",
                tags={"ManagedBy": "anvil"},
                opts=pulumi.ResourceOptions(parent=parent),
            )
        return

    # ── CIDR egress ───────────────────────────────────────────────────────────
    for cidr_rule in cidrs:
        cidr_range = cidr_rule.get("range", "")
        cidr_ports = cidr_rule.get("ports", [])

        if not cidr_ports:
            raise ValueError(
                f"{name}: grant_egress cidr \"{cidr_range}\" requires ports — "
                "be explicit about which ports this range needs."
            )

        sanitized = re.sub(r"[^0-9a-z]", "-", cidr_range.lower())
        for port in cidr_ports:
            aws.vpc.SecurityGroupEgressRule(
                f"{name}-egress-{sanitized}-{port}",
                security_group_id=security_group_id,
                cidr_ipv4=cidr_range,
                from_port=port,
                to_port=port,
                ip_protocol="tcp",
                tags={"ManagedBy": "anvil"},
                opts=pulumi.ResourceOptions(parent=parent),
            )


class VpcEndpointTarget(Protocol):
    """
    Any Anvil VPC endpoint resource that exposes a security group ID and
    logical name for grant rule naming.

    Satisfied by VpcEndpoint (endpoint_name() injected by fix-sdk-grants.ts).
    """

    @property
    def security_group_id(self) -> pulumi.Output[str]: ...

    def endpoint_name(self) -> str: ...


def create_endpoint_access_grant(
    parent: pulumi.Resource,
    name: str,
    compute_sg_id: pulumi.Output[str],
    endpoints: List,
) -> None:
    """
    Creates the paired SG rules that open the network path between a compute
    resource and one or more Interface VPC Endpoints.

    For each endpoint:
      - SecurityGroupEgressRule on the compute SG  — port 443 out to endpoint SG
      - SecurityGroupIngressRule on the endpoint SG — port 443 in from compute SG

    Both rules are required — the endpoint SG has zero rules by default.
    """
    for endpoint in endpoints:
        ep_name = endpoint.endpoint_name()
        endpoint_sg_id = endpoint.security_group_id

        # 1. Egress rule on the compute SG — port 443 out to the endpoint SG.
        aws.vpc.SecurityGroupEgressRule(
            f"{name}-{ep_name}-endpoint-egress",
            security_group_id=compute_sg_id,
            referenced_security_group_id=endpoint_sg_id,
            from_port=443,
            to_port=443,
            ip_protocol="tcp",
            tags={"ManagedBy": "anvil"},
            opts=pulumi.ResourceOptions(parent=parent),
        )

        # 2. Ingress rule on the endpoint SG — port 443 in from the compute SG.
        aws.vpc.SecurityGroupIngressRule(
            f"{name}-{ep_name}-endpoint-ingress",
            security_group_id=endpoint_sg_id,
            referenced_security_group_id=compute_sg_id,
            from_port=443,
            to_port=443,
            ip_protocol="tcp",
            tags={"ManagedBy": "anvil"},
            opts=pulumi.ResourceOptions(parent=parent),
        )