"""Anvil compliance scanner.

Runs inside the pinned Prowler image (CodeBuild in production, Docker locally)
and scans one app — the resources tagged ManagedBy=anvil + project + stage.
See docs/design/compliance-scanning.md.

Steps:
  1. Validate inputs (environment variables) against strict patterns.
  2. Resolve the app's resource ARNs: the Resource Groups Tagging API per region
     (always including us-east-1 for CloudFront, CloudFront-scoped WAF and
     Lambda@Edge), plus IAM roles by tag (the tagging API doesn't return IAM).
  3. Run Prowler with --resource-arns.
  4. Drop findings not labelled with this app's project + stage (account-level
     checks leak through the resource filter; they belong to org mode).
  5. Write findings, then the manifest (a scan is complete only once its
     manifest exists), then the latest.json pointer.

Inputs:
  ANVIL_PROJECT, ANVIL_STAGE   app scope (tag values)
  ANVIL_REGIONS                space/comma separated, e.g. "ap-southeast-2"
  ANVIL_FRAMEWORKS             Prowler compliance IDs, e.g. "soc2_aws cis_7.0_aws"
  ANVIL_RETENTION              30d | 90d | 180d | 1y | 2y | 7y
  ANVIL_TRIGGER                scheduled | deploy | manual
  ANVIL_RESULTS_BUCKET         results bucket name, or
  ANVIL_RESULTS_DIR            a local directory instead (local testing)
"""

import datetime
import json
import os
import re
import subprocess
import sys
import tempfile

def _find_prowler():
    # The image installs Prowler in the same venv as its Python, and that venv
    # isn't on PATH once the entrypoint is overridden.
    beside = os.path.join(os.path.dirname(sys.executable), "prowler")
    return beside if os.path.exists(beside) else "prowler"


PROWLER = os.environ.get("ANVIL_PROWLER_BIN") or _find_prowler()

GLOBAL_REGION = "us-east-1"
RETENTION_TIERS = ("30d", "90d", "180d", "1y", "2y", "7y")
TRIGGERS = ("scheduled", "deploy", "manual")
MANIFEST_SCHEMA = 1

# Tag values Anvil writes for project/stage. Kept tight on purpose: these end up
# in S3 keys and on the Prowler command line.
NAME_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$")
REGION_RE = re.compile(r"^[a-z]{2}(-[a-z]+)+-[0-9]$")
FRAMEWORK_RE = re.compile(r"^[a-z0-9][a-z0-9_.]{1,100}$")
MAX_REGIONS = 20
MAX_FRAMEWORKS = 20


class InputError(Exception):
    pass


def log(msg):
    print(f"[anvil-scan] {msg}", flush=True)


# ── Inputs ──────────────────────────────────────────────────


def split_list(value):
    return [v for v in re.split(r"[\s,]+", value or "") if v]


def parse_inputs(env):
    """Validate the environment and return the scan config. Raises InputError."""
    errors = []

    def name(key):
        value = env.get(key, "")
        if not NAME_RE.match(value):
            errors.append(f"{key} must match {NAME_RE.pattern}, got {value!r}")
        return value

    project = name("ANVIL_PROJECT")
    stage = name("ANVIL_STAGE")

    regions = split_list(env.get("ANVIL_REGIONS"))
    if not regions:
        errors.append("ANVIL_REGIONS is required")
    elif len(regions) > MAX_REGIONS:
        errors.append(f"ANVIL_REGIONS has more than {MAX_REGIONS} entries")
    errors += [f"invalid region {r!r}" for r in regions if not REGION_RE.match(r)]

    frameworks = split_list(env.get("ANVIL_FRAMEWORKS"))
    if not frameworks:
        errors.append("ANVIL_FRAMEWORKS is required")
    elif len(frameworks) > MAX_FRAMEWORKS:
        errors.append(f"ANVIL_FRAMEWORKS has more than {MAX_FRAMEWORKS} entries")
    errors += [f"invalid framework {f!r}" for f in frameworks if not FRAMEWORK_RE.match(f)]

    retention = env.get("ANVIL_RETENTION", "1y")
    if retention not in RETENTION_TIERS:
        errors.append(f"ANVIL_RETENTION must be one of {', '.join(RETENTION_TIERS)}, got {retention!r}")

    trigger = env.get("ANVIL_TRIGGER", "manual")
    if trigger not in TRIGGERS:
        errors.append(f"ANVIL_TRIGGER must be one of {', '.join(TRIGGERS)}, got {trigger!r}")

    bucket = env.get("ANVIL_RESULTS_BUCKET", "")
    local_dir = env.get("ANVIL_RESULTS_DIR", "")
    if bool(bucket) == bool(local_dir):
        errors.append("set exactly one of ANVIL_RESULTS_BUCKET or ANVIL_RESULTS_DIR")
    if bucket and not re.match(r"^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$", bucket):
        errors.append(f"invalid ANVIL_RESULTS_BUCKET {bucket!r}")

    if errors:
        raise InputError("; ".join(errors))

    return {
        "project": project,
        "stage": stage,
        # Dedupe, keep order, and always include the global region.
        "regions": list(dict.fromkeys(regions + [GLOBAL_REGION])),
        "frameworks": list(dict.fromkeys(frameworks)),
        "retention": retention,
        "trigger": trigger,
        "bucket": bucket,
        "local_dir": local_dir,
    }


def scope_tags(cfg):
    return {"ManagedBy": "anvil", "project": cfg["project"], "stage": cfg["stage"]}


# ── Resource resolution ─────────────────────────────────────


def resolve_arns(session, cfg):
    """Return the sorted ARNs of every resource carrying the app's scope tags."""
    tags = scope_tags(cfg)
    filters = [{"Key": k, "Values": [v]} for k, v in tags.items()]
    arns = set()

    # Tag filters are ANDed by the tagging API.
    for region in cfg["regions"]:
        client = session.client("resourcegroupstaggingapi", region_name=region)
        for page in client.get_paginator("get_resources").paginate(TagFilters=filters):
            arns.update(r["ResourceARN"] for r in page["ResourceTagMappingList"])

    # IAM isn't indexed by the tagging API, and ListRoles doesn't return tags.
    iam = session.client("iam")
    for page in iam.get_paginator("list_roles").paginate():
        for role in page["Roles"]:
            role_tags = {
                t["Key"]: t["Value"]
                for t in iam.list_role_tags(RoleName=role["RoleName"])["Tags"]
            }
            if all(role_tags.get(k) == v for k, v in tags.items()):
                arns.add(role["Arn"])

    return sorted(arns)


# ── Prowler ─────────────────────────────────────────────────


def prowler_version():
    try:
        out = subprocess.run([PROWLER, "--version"], capture_output=True, text=True, check=False)
    except OSError:
        return "unknown"
    match = re.search(r"\d+\.\d+\.\d+", out.stdout + out.stderr)
    return match.group(0) if match else "unknown"


def prowler_command(cfg, arns, out_dir):
    # A list, never a shell string: values can't be reinterpreted.
    return [
        PROWLER, "aws",
        "--resource-arns", *arns,
        "--region", *cfg["regions"],
        "--compliance", *cfg["frameworks"],
        "--output-formats", "json-ocsf",
        "--output-directory", out_dir,
        "--output-filename", "scan",
        "--ignore-exit-code-3",
        "--no-banner",
        "--no-color",
    ]


def run_prowler(cfg, arns, out_dir):
    cmd = prowler_command(cfg, arns, out_dir)
    log(f"running Prowler on {len(arns)} resource(s) in {', '.join(cfg['regions'])}")
    result = subprocess.run(cmd, check=False)
    if result.returncode != 0:
        raise RuntimeError(f"Prowler exited with code {result.returncode}")
    with open(os.path.join(out_dir, "scan.ocsf.json")) as f:
        return json.load(f)


# ── Findings ────────────────────────────────────────────────


def labels_to_tags(labels):
    """Prowler OCSF labels are "Key:Value" strings; values may contain ':'."""
    tags = {}
    for label in labels or []:
        key, sep, value = label.partition(":")
        if sep:
            tags[key] = value
    return tags


def in_scope(finding, cfg):
    want = scope_tags(cfg)
    for resource in finding.get("resources") or []:
        tags = labels_to_tags(resource.get("labels"))
        if all(tags.get(k) == v for k, v in want.items()):
            return True
    return False


def summarise(findings):
    summary = {"total": len(findings), "pass": 0, "fail": 0, "manual": 0, "muted": 0, "failBySeverity": {}}
    for f in findings:
        if (f.get("status") or "").lower() == "suppressed" or f.get("unmapped", {}).get("muted"):
            summary["muted"] += 1
            continue
        status = (f.get("status_code") or "").lower()
        if status in ("pass", "fail", "manual"):
            summary[status] += 1
        if status == "fail":
            sev = (f.get("severity") or "unknown").lower()
            summary["failBySeverity"][sev] = summary["failBySeverity"].get(sev, 0) + 1
    return summary


# ── Results ─────────────────────────────────────────────────


class LocalStore:
    """Writes the results layout to a directory. Tags are recorded alongside."""

    def __init__(self, root):
        self.root = root

    def put(self, key, body, tags=None):
        path = os.path.join(self.root, key)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "wb") as f:
            f.write(body)
        if tags:
            with open(path + ".tags.json", "w") as f:
                json.dump(tags, f)


class S3Store:
    def __init__(self, session, bucket):
        self.s3 = session.client("s3")
        self.bucket = bucket
        # Refuse to write to a same-named bucket owned by another account.
        self.owner = session.client("sts").get_caller_identity()["Account"]

    def put(self, key, body, tags=None):
        args = {
            "Bucket": self.bucket,
            "Key": key,
            "Body": body,
            "ContentType": "application/json",
            "ExpectedBucketOwner": self.owner,
        }
        if tags:
            args["Tagging"] = "&".join(f"{k}={v}" for k, v in tags.items())
        self.s3.put_object(**args)


def utcnow():
    return datetime.datetime.now(datetime.timezone.utc)


def iso(ts):
    return ts.strftime("%Y-%m-%dT%H:%M:%SZ")


def write_results(store, cfg, scan_id, manifest, findings):
    prefix = f"results/{cfg['project']}/{cfg['stage']}"
    tags = {"retention": cfg["retention"]}

    if findings is not None:
        key = f"{prefix}/{scan_id}/findings.ocsf.json"
        store.put(key, json.dumps(findings).encode(), tags)
        manifest["files"] = {"findings": key}

    # Manifest last: its presence marks the scan as complete.
    store.put(f"{prefix}/{scan_id}/manifest.json", json.dumps(manifest, indent=2).encode(), tags)

    # The pointer is untagged so lifecycle rules never expire it.
    if manifest["status"] in ("complete", "empty"):
        pointer = {"scanId": scan_id, "manifest": f"{prefix}/{scan_id}/manifest.json", "finishedAt": manifest["finishedAt"]}
        store.put(f"{prefix}/latest.json", json.dumps(pointer, indent=2).encode())


# ── Main ────────────────────────────────────────────────────


def main():
    try:
        cfg = parse_inputs(os.environ)
    except InputError as e:
        log(f"invalid input: {e}")
        return 2

    import boto3  # bundled with Prowler

    session = boto3.Session()
    store = LocalStore(cfg["local_dir"]) if cfg["local_dir"] else S3Store(session, cfg["bucket"])

    started = utcnow()
    scan_id = f"{started.strftime('%Y%m%dT%H%M%SZ')}-{cfg['trigger']}"
    log(f"scan {scan_id} for {cfg['project']}/{cfg['stage']}")

    manifest = {
        "schema": MANIFEST_SCHEMA,
        "scanId": scan_id,
        "project": cfg["project"],
        "stage": cfg["stage"],
        "trigger": cfg["trigger"],
        "regions": cfg["regions"],
        "frameworks": cfg["frameworks"],
        "retention": cfg["retention"],
        "prowlerVersion": prowler_version(),
        "startedAt": iso(started),
    }

    findings = None
    exit_code = 0
    try:
        arns = resolve_arns(session, cfg)
        manifest["resources"] = {"count": len(arns), "arns": arns}
        log(f"resolved {len(arns)} resource(s)")

        if not arns:
            manifest["status"] = "empty"
        else:
            with tempfile.TemporaryDirectory() as out_dir:
                raw = run_prowler(cfg, arns, out_dir)
            findings = [f for f in raw if in_scope(f, cfg)]
            manifest["droppedOutOfScope"] = len(raw) - len(findings)
            manifest["findings"] = summarise(findings)
            manifest["status"] = "complete"
    except Exception as e:  # recorded in the manifest, then fail the build
        log(f"scan failed: {e}")
        manifest["status"] = "failed"
        manifest["error"] = str(e)
        findings = None
        exit_code = 1

    finished = utcnow()
    manifest["finishedAt"] = iso(finished)
    manifest["durationSeconds"] = round((finished - started).total_seconds(), 1)

    write_results(store, cfg, scan_id, manifest, findings)
    log(f"{manifest['status']}: {json.dumps(manifest.get('findings', {}))}")
    return exit_code


if __name__ == "__main__":
    sys.exit(main())
