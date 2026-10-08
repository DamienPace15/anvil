# Compliance Scanning (Prowler) and the Local Dashboard

**Status:** Built and verified end to end on `testWaf` (2026-10-08): shared
scanner created on first deploy, a scan on deploy, a scheduled scan, a manual
scan, and the dashboard reading all three. Not yet done: idle shutdown for the
dashboard, a teardown hint on `anvil destroy`, and an import test of the Python
SDK path. See [Known limitations](#known-limitations) and
[Future work](#future-work).

---

## Contents

1. [What it is](#what-it-is)
2. [Using it](#using-it)
3. [How it fits together](#how-it-fits-together)
4. [The shared scanner (per account and region)](#the-shared-scanner-per-account-and-region)
5. [The per-app schedule](#the-per-app-schedule)
6. [How `anvil deploy` handles compliance](#how-anvil-deploy-handles-compliance)
7. [What happens during a scan](#what-happens-during-a-scan)
8. [Results and retention](#results-and-retention)
9. [The local dashboard](#the-local-dashboard)
10. [Commands](#commands)
11. [Security model](#security-model)
12. [Costs](#costs)
13. [Known limitations](#known-limitations)
14. [Code map](#code-map)
15. [Testing](#testing)
16. [Upgrading Prowler](#upgrading-prowler)
17. [Decisions and rejected alternatives](#decisions-and-rejected-alternatives)
18. [Test history](#test-history)
19. [Future work](#future-work)

---

## What it is

Anvil's components are aligned to SOC 2 and ISO 27001 controls. Compliance
scanning checks that claim against the **live account**: an app opts in, Anvil
schedules [Prowler](https://github.com/prowler-cloud/prowler) scans of that
app's deployed resources, and `anvil compliance dashboard` shows the results on
a local web page, grouped by the Anvil component that created each resource.

It is a **post-deploy posture check**. Prowler inspects deployed resources, not
code or Pulumi plans, so it complements rather than replaces pre-deploy checks.

Two constraints shaped everything:

- **Prowler never ships in the CLI.** It's a large Python application. It runs
  in AWS CodeBuild inside your account, in Prowler's own unmodified container
  image. The CLI only starts scans and reads results.
- **The scanner can't change or read your data.** The scan role reads
  configuration only, is explicitly denied data reads (object contents, logs,
  function code, secrets), and can write nothing except its own results.

## Using it

### Turning it on

Add `compliance` to your `App`:

```ts
export default new App({
  providers: { aws: { region: 'ap-southeast-2' } },
  compliance: {
    frameworks: ['soc2', 'iso27001', 'cis'], // required
    schedule: 'daily', // default
    retention: '1y', // default
    scanOnDeploy: true, // default false
  },
  run(ctx) {
    /* ... */
  },
});
```

<<<<<<< HEAD
Then deploy:

```bash
anvil deploy
```

The first deploy in an account and region asks before creating the shared
scanner (see [First-time confirmation](#first-time-confirmation)). From then on
the app is scanned on its schedule, and after each deploy if `scanOnDeploy` is
set. To view results:

```bash
anvil compliance dashboard
```

Python and Go take the same settings: `compliance=anvil.ComplianceConfig(...)`
(with `anvil.ComplianceCron` for custom schedules) and
`Compliance: &anvil.ComplianceConfig{...}`.

### Options

| Option         | Default                  | Values                                                   |
| -------------- | ------------------------ | -------------------------------------------------------- |
| `frameworks`   | **required, no default** | Friendly names or raw Prowler IDs (below)                |
| `schedule`     | `'daily'`                | `'daily'`, `'weekly'`, `'none'`, or `{ cron, timezone }` |
| `retention`    | `'1y'`                   | `'30d'`, `'90d'`, `'180d'`, `'1y'`, `'2y'`, `'7y'`       |
| `scanOnDeploy` | `false`                  | Start a scan after each successful deploy                |

### Frameworks: friendly names and raw IDs

`frameworks` accepts **two forms**, mixed freely:

- **Friendly names** are short and always point at the newest version Anvil
  pins. Upgrading Anvil can move `cis` from CIS 7.0 to a later CIS, in step with
  the pinned Prowler image.
- **Raw Prowler IDs** are any AWS compliance ID in the pinned Prowler version
  (`prowler aws --list-compliance`). They stay fixed across Anvil upgrades, so
  use them to pin an older version (`cis_6.0_aws`) or for frameworks without a
  friendly name (`ens_rd2022_aws`, `ffiec_aws`, `mitre_attack_aws`, …).

```ts
compliance: {
  frameworks: ['soc2', 'cis', 'ens_rd2022_aws'];
}
```

Unknown values fail at deploy with a "did you mean …?" suggestion, never later
inside the scan. Raw IDs are checked against the **exact list** for the pinned
Prowler version, not a pattern, because several real IDs don't end in `_aws`
(`dora_2022_2554`, `cmmc_2.0`, `csa_ccm_4.0`, `cis_controls_8.1`,
`fedramp_20x_*`).

Friendly names (Prowler 5.44.0):

| Friendly       | Prowler ID                                     | Friendly           | Prowler ID                                           |
| -------------- | ---------------------------------------------- | ------------------ | ---------------------------------------------------- |
| `soc2`         | `soc2_aws`                                     | `gdpr`             | `gdpr_aws`                                           |
| `iso27001`     | `iso27001_2022_aws`                            | `nis2`             | `nis2_aws`                                           |
| `cis`          | `cis_7.0_aws`                                  | `dora`             | `dora_2022_2554`                                     |
| `nist-800-53`  | `nist_800_53_revision_5_aws`                   | `fedramp-low`      | `fedramp_low_revision_4_aws`                         |
| `nist-800-171` | `nist_800_171_revision_2_aws`                  | `fedramp-moderate` | `fedramp_moderate_revision_4_aws`                    |
| `nist-csf`     | `nist_csf_2.0_aws`                             | `cmmc`             | `cmmc_2.0`                                           |
| `pci`          | `pci_4.0_aws`                                  | `essential-eight`  | `asd_essential_eight_aws`                            |
| `hipaa`        | `hipaa_aws`                                    | `well-architected` | `aws_well_architected_framework_security_pillar_aws` |
| `fsbp`         | `aws_foundational_security_best_practices_aws` | `c5`               | `c5_aws`                                             |
|                |                                                | `csa-ccm`          | `csa_ccm_4.0`                                        |

**More frameworks cost little.** Prowler runs _checks_; a framework maps its
requirements onto checks. Several frameworks run the union of their checks, and
SOC 2, ISO 27001 and CIS overlap heavily. The worst case equals a scan with no
framework filter.

### Schedules

- **Presets** (`daily`, `weekly`) run at a fixed time derived from a hash of
  the project and stage. Apps in one account don't all scan at midnight, and an
  app's time stays the same across deploys.
- **`none`** means scans run only after deploys (`scanOnDeploy`) or on demand.
- **Custom:** `{ cron: '0 3 * * ? *', timezone: 'Australia/Sydney' }`. Six AWS
  cron fields (minutes, hours, day of month, month, day of week, year). At most
  once an hour: the minutes field must be a single value. Time zone defaults to
  UTC.

### What gets scanned

Exactly the resources tagged `ManagedBy=anvil`, `project=<project>` and
`stage=<stage>`, in the app's regions plus `us-east-1` (for CloudFront,
CloudFront-scoped WAF and Lambda@Edge). Every Anvil component sets those tags,
and so does `anvil bootstrap` on the stage's state bucket. Several apps and
stages in one account are scanned separately and never see each other's
resources.

Raw Pulumi resources created directly in `run()` don't carry `ManagedBy` and
aren't scanned. Account-level checks (CloudTrail, GuardDuty, password policy)
aren't about any one app and are left out; they belong to a future org mode.

## How it fits together

There are **two layers with different owners**. A resource shared by every app
can't belong to any one app's Pulumi stack: a second app would fail with
"already exists", and destroying the first app would delete it for everyone.

```
                ┌──────────────── shared, one per account + region ─────────────────┐
                │  managed by the CLI with direct AWS calls (no Pulumi state)        │
 anvil deploy ──┼─► results bucket   scan role (read-only)   invoke role             │
 (first time,   │   CodeBuild project (Prowler image)   build log group             │
  or upgrade)   │   schedule group "anvil-compliance"                                │
                └────────────────────────────▲───────────────────────────────────────┘
                                             │ codebuild:StartBuild (app's settings)
                ┌────────────────────────────┴──── per app (project + stage) ────────┐
 anvil deploy ──┼─► anvil:aws:ComplianceScanner (Pulumi component in the app stack)  │
 (pulumi up)    │     └─ EventBridge Scheduler schedule in "anvil-compliance"        │
                └─────────────────────────────────────────────────────────────────────┘

 Scans are started by: the schedule · anvil deploy (scanOnDeploy) · anvil compliance scan
                       · the dashboard's Rescan button

 anvil compliance dashboard ── reads results from S3 + Pulumi state ──► 127.0.0.1
```

## The shared scanner (per account and region)

Created and updated by the CLI with direct AWS SDK calls, in the same style as
`anvil bootstrap`. There is no Pulumi stack and no state to store.

### Resources

For account `A` and region `R`. Every resource is tagged `ManagedBy=anvil` and
`Component=Compliance`.

| Resource          | Name                                  | Configuration                                                                                                                                                                                                                                                  |
| ----------------- | ------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Results bucket    | `anvil-compliance-A-R`                | Public access fully blocked; SSE-KMS with the AWS-managed key and Bucket Keys; versioning on; bucket owner owns every object; TLS-only bucket policy; one lifecycle rule per retention tier plus a cleanup rule ([Retention](#retention))                      |
| Scan role         | `anvil-compliance-scan-R`             | Assumable only by CodeBuild for this account's project ([Security model](#security-model))                                                                                                                                                                     |
| Invoke role       | `anvil-compliance-invoke-R`           | Assumable only by EventBridge Scheduler in this account; can only start builds of this project                                                                                                                                                                 |
| Build log group   | `/aws/codebuild/anvil-compliance-A-R` | 30-day retention                                                                                                                                                                                                                                               |
| CodeBuild project | `anvil-compliance-A-R`                | ARM, small; no VPC, no source; Prowler image pulled from AWS's public registry, pinned by digest; `scan.py` embedded in the build file; 30-minute timeout; up to 3 builds at once; `ANVIL_RESULTS_BUCKET` set; tagged `anvil:compliance-version=<CLI version>` |
| Schedule group    | `anvil-compliance`                    | Holds each app's schedule; its contents are the list of apps using the scanner                                                                                                                                                                                 |

IAM is global to an account, so role names carry the region. S3 names are
global, so the bucket name carries the account ID.

### Why CodeBuild, without a VPC

Prowler calls AWS APIs in every region it scans, so it needs outbound internet
access. CodeBuild without a VPC runs on AWS-managed networking that includes it.
Fargate would need a VPC (often deleted in security-conscious accounts), an
internet gateway and subnets, and a public IP on the task, which fails Security
Hub's ECS.2 control: the scanner would flag itself. CodeBuild also gives each
run its own logged build record and a per-project concurrency limit, and the
build file can wrap the upstream image without a custom image to publish.

### The build file

The project's build file unpacks and runs `scan.py`:

```yaml
version: 0.2
phases:
  build:
    commands:
      - |
        /home/prowler/.venv/bin/python -c "import base64, gzip, sys; open('/tmp/anvil-scan.py', 'wb').write(gzip.decompress(base64.b64decode(sys.argv[1])))" <gzipped, base64 scan.py>
      - /home/prowler/.venv/bin/python -B /tmp/anvil-scan.py
```

The script travels compressed (about 6 KB) inside the build file and is unpacked
with the image's own Python, so the build depends on nothing but the pinned
image.

### Create, update and the version tag

`anvil compliance setup` and `anvil deploy` (for apps with `compliance`) read the
CodeBuild project's `anvil:compliance-version` tag:

| Tag                 | Action                                                           |
| ------------------- | ---------------------------------------------------------------- |
| Project missing     | Create everything (asks first, see below)                        |
| Older than this CLI | Update everything (new Prowler image, policies, lifecycle rules) |
| Same or newer       | Skip. An older CLI never downgrades the scanner                  |
| CLI is a dev build  | Always update (development convenience)                          |

Every step is **idempotent**: "already exists" counts as success, then the
desired settings are applied. Most of these calls replace a whole document
(lifecycle rules, role policies, the project definition), so re-applying also
undoes manual changes. A partly failed run is completed by the next one. Two
first deploys at once race harmlessly. Two waits are built in:

- **New IAM roles** take a few seconds to become usable, so creating the project
  retries for up to 90 seconds.
- **Accounts with a CodeBuild concurrency limit below 3** get a project with no
  per-project limit, plus a warning that scans will run one at a time.

Because there's no Pulumi state, a later release that removes a policy statement
or resource needs an explicit cleanup step.

### Protection against existing and lookalike resources

- **Bucket names are global**, so anyone who knows the account ID could create
  `anvil-compliance-A-R` first. Every S3 call passes `ExpectedBucketOwner`. If
  the name belongs to another account, the CLI stops with an error and never
  writes to it.
- **Roles and projects** with the expected names but without Anvil's tags are
  never taken over. The CLI stops with an error instead.

### First-time confirmation

Only when the scanner would be **created** (not on updates):

```
  No compliance scanner exists yet in account 904233095613 (ap-southeast-2).
  Anvil will create shared resources, used by every Anvil app in this account and region:

    • CodeBuild project + read-only scan role   anvil-compliance-904233095613-ap-southeast-2, anvil-compliance-scan-ap-southeast-2
    • Results bucket (encrypted, versioned)     anvil-compliance-904233095613-ap-southeast-2
    • Scheduler group + invoke role             anvil-compliance, anvil-compliance-invoke-ap-southeast-2
    • Build log group (30-day retention)        /aws/codebuild/anvil-compliance-904233095613-ap-southeast-2

? Create them [y/N]
```

- `--yes` (on `deploy` and `setup`) skips the prompt, for CI and scripts.
- **Without a terminal and without `--yes`, it stops with an error.** CI never
  creates account-wide resources silently, and `CI=true` doesn't count as a yes.
- Answering no changes nothing.

### Teardown

`anvil compliance teardown` deletes the shared resources **by name**, and only
those carrying Anvil's tags. Anything with the same name but without the tags is
skipped with a warning.

- It refuses while the schedule group still has schedules, i.e. while any app
  still has `compliance` set. Remove it from those apps and deploy first.
- The results bucket is kept unless `--delete-results` is passed. Then every
  object version and delete marker is removed before the bucket.

## The per-app schedule

### The component

`anvil:aws:ComplianceScanner` is a provider component (`provider/aws/complianceScanner/`).
`App` creates it, named `anvil-compliance`, when `compliance` is set. Users don't
construct it directly; its schema description says so.

It lives under `anvil:aws` rather than an `internal` namespace for two reasons:
the generated Go SDK already uses an `internal` package (a Go-restricted name),
and the Python SDK patch script expects `aws` and `gcp` to be the only generated
namespaces. Under `aws` it also inherits the app's default AWS provider
automatically (TypeScript and Python), so it uses the same credentials and
region as the rest of the app. The Go `App` passes the provider explicitly.

### What it does

1. **Resolves settings:** friendly names to Prowler IDs (exact list, "did you
   mean"), the retention tier, the schedule expression, and the time zone.
2. **Works out regions:** the region of the provider it runs under (via an
   `aws.GetRegion` call), plus every region in the app's AWS providers.
3. **In discovery mode** (`ANVIL_BUILD_MODE=true`), writes the `compliance`
   section of `.anvil/build-manifest.json` and stops ([below](#the-discovery-manifest)).
4. **Otherwise**, unless `schedule` is `none`, creates one EventBridge Scheduler
   schedule:

| Setting         | Value                                                                                                     |
| --------------- | --------------------------------------------------------------------------------------------------------- |
| Name            | `anvil-<project>-<stage>` (invalid characters replaced; over 64 characters, truncated with a hash suffix) |
| Group           | `anvil-compliance`                                                                                        |
| Expression      | From the schedule setting, e.g. `cron(27 14 * * ? *)`                                                     |
| Time zone       | The custom schedule's time zone, else UTC                                                                 |
| Flexible window | Off (start times are already spread)                                                                      |
| Target          | `arn:aws:scheduler:::aws-sdk:codebuild:startBuild` (a "universal target" that calls the API directly)     |
| Role            | The shared invoke role, ARN built from account and region (no cross-stack reference)                      |
| Retries         | Up to 5 attempts within an hour, for temporary failures such as the account's CodeBuild limit             |

The schedule's input is the `StartBuild` request. **EventBridge Scheduler
requires PascalCase parameter names here**, not CodeBuild's own camelCase, and
checks them when the schedule is created:

```json
{
  "ProjectName": "anvil-compliance-904233095613-ap-southeast-2",
  "EnvironmentVariablesOverride": [
    { "Name": "ANVIL_PROJECT", "Value": "testwaf", "Type": "PLAINTEXT" },
    { "Name": "ANVIL_STAGE", "Value": "damienpace", "Type": "PLAINTEXT" },
    { "Name": "ANVIL_REGIONS", "Value": "ap-southeast-2", "Type": "PLAINTEXT" },
    {
      "Name": "ANVIL_FRAMEWORKS",
      "Value": "soc2_aws iso27001_2022_aws cis_7.0_aws",
      "Type": "PLAINTEXT"
    },
    { "Name": "ANVIL_RETENTION", "Value": "1y", "Type": "PLAINTEXT" },
    { "Name": "ANVIL_TRIGGER", "Value": "scheduled", "Type": "PLAINTEXT" }
  ]
}
```

Because each app owns its own schedule, apps' settings never conflict, and the
schedule follows the app: created, updated and deleted with its stack.

### The discovery manifest

Before deploying, `anvil deploy` runs the program once in discovery mode. The
component writes its resolved settings there, which tells the CLI **before**
deploying that the shared scanner must exist, and in which region:

```json
"compliance": {
  "frameworks": ["soc2_aws", "iso27001_2022_aws", "cis_7.0_aws"],
  "schedule": "daily",
  "scheduleExpression": "cron(27 14 * * ? *)",
  "timezone": "UTC",
  "retention": "1y",
  "scanOnDeploy": true,
  "regions": ["ap-southeast-2"],
  "region": "ap-southeast-2"
}
```

The Lambda and DSQL components write to the same file. All writers keep the
other sections intact and share a lock (`provider/internal/shared/manifest.go`),
since components can be built at the same time in one provider process.

## How `anvil deploy` handles compliance

```
anvil deploy [--yes]
  1. discovery                 → .anvil/build-manifest.json, incl. compliance
  2. build Lambda functions
  3. ensure shared scanner     only if compliance is set; skipped when current
  4. pulumi up                 → creates or updates the app's schedule
  5. scan on deploy            only if scanOnDeploy: starts a scan, doesn't wait
       ✔ Compliance scan started — run `anvil compliance status` to see the result
```

Step 3 comes before step 4 because the schedule points at the shared project. A
failure to start the scan in step 5 is only a warning: the deploy itself has
already succeeded.

| Change               | What the next deploy does                                            |
| -------------------- | -------------------------------------------------------------------- |
| `compliance` added   | Creates the shared scanner if needed (asks first), then the schedule |
| Settings changed     | Updates the schedule in place                                        |
| `compliance` removed | Deletes the schedule; the shared scanner and past results stay       |
| `anvil destroy`      | Deletes the schedule with the rest of the stack                      |

## What happens during a scan

Every scan, however it's started, is a CodeBuild build running `scan.py`
(`cmd/anvil/compliance/scan.py`). It uses only Python's standard library and
boto3, both already in the Prowler image.

1. **Checks inputs.** Project and stage must match
   `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`; regions and frameworks must match strict
   patterns; retention and trigger must be allowed values; exactly one of
   `ANVIL_RESULTS_BUCKET` or `ANVIL_RESULTS_DIR` must be set. Prowler is called
   with a list of arguments rather than a shell string, so no input can run as a
   command. Invalid input exits with code 2.
2. **Collects the app's resource ARNs:**
   - the AWS Resource Groups Tagging API in each region (always adding
     `us-east-1`), with the three tags required together;
   - IAM roles listed and filtered by their tags, because the tagging API
     doesn't return IAM roles.
3. **Runs Prowler** on exactly those resources:
   ```bash
   prowler aws --resource-arns <arn> <arn> … --region <regions> \
     --compliance <framework IDs> --output-formats json-ocsf \
     --ignore-exit-code-3 --no-banner --no-color
   ```
   `--ignore-exit-code-3` stops failing checks from counting as a failed run.
4. **Drops findings that don't belong to the app.** Some account-level checks
   appear even when scanning specific ARNs; their resources don't carry the
   app's tags.
5. **Writes results** in order: the findings, then the manifest, then the
   `latest.json` pointer ([below](#results-and-retention)).

If nothing matches the app's tags, the manifest records `status: empty` and
Prowler doesn't run. If anything fails, the manifest records `status: failed`
with the error, `latest.json` is left alone, and the build exits with code 1.

The same script runs locally with Docker for development
([Testing](#testing)).

### Ways a scan starts

| Trigger            | Started by                                     | `ANVIL_TRIGGER` |
| ------------------ | ---------------------------------------------- | --------------- |
| Schedule           | EventBridge Scheduler, through the invoke role | `scheduled`     |
| After deploy       | `anvil deploy` with `scanOnDeploy: true`       | `deploy`        |
| On demand          | `anvil compliance scan`                        | `manual`        |
| From the dashboard | The Rescan button                              | `manual`        |

## Results and retention

### Layout

```
s3://anvil-compliance-<account>-<region>/
  results/<project>/<stage>/
    <scan-id>/                  scan-id = <UTC timestamp>-<trigger>, e.g. 20261008T123221Z-scheduled
      findings.ocsf.json        Prowler's findings, filtered to the app (OCSF format)
      manifest.json             written LAST: a scan only counts as finished once this exists
    latest.json                 pointer to the newest finished scan
```

Scan IDs start with the timestamp, so they sort by time.

### The manifest

```json
{
  "schema": 1,
  "scanId": "20261008T123221Z-scheduled",
  "project": "testwaf",
  "stage": "damienpace",
  "trigger": "scheduled",
  "regions": ["ap-southeast-2", "us-east-1"],
  "frameworks": ["soc2_aws", "iso27001_2022_aws", "cis_7.0_aws"],
  "retention": "1y",
  "prowlerVersion": "5.44.0",
  "startedAt": "2026-10-08T12:32:21Z",
  "resources": { "count": 9, "arns": ["arn:aws:cloudfront::…", "…"] },
  "droppedOutOfScope": 4,
  "findings": {
    "total": 36,
    "pass": 16,
    "fail": 20,
    "manual": 0,
    "muted": 0,
    "failBySeverity": { "medium": 9, "low": 11 }
  },
  "status": "complete",
  "finishedAt": "2026-10-08T12:33:34Z",
  "durationSeconds": 73.0,
  "files": {
    "findings": "results/testwaf/damienpace/20261008T123221Z-scheduled/findings.ocsf.json"
  }
}
```

`status` is `complete`, `empty` or `failed` (with `error`). Only `complete` and
`empty` scans move `latest.json`.

### Retention

Retention is per object, using **object tags and fixed lifecycle rules**. A
bucket has one lifecycle configuration, so per-app rules would overwrite each
other; tiers avoid any coordination between apps.

- Each findings file and manifest is tagged `retention=<tier>` when uploaded.
- The bucket has one rule per tier (`30d`, `90d`, `180d`, `1y` = 365 days, `2y`
  = 730, `7y` = 2,555) that expires matching objects and removes their old
  versions a day later. On a versioned bucket, expiry alone would only add a
  delete marker and nothing would actually be deleted.
- A cleanup rule removes leftover delete markers, deletes old versions of the
  untagged `latest.json` pointers after 30 days, and aborts incomplete uploads.
- The pointers themselves are untagged, so they never expire.
- **Changing an app's retention only affects new scans.** Existing results keep
  their tier, so shortening retention never suddenly deletes audit evidence.
- CodeBuild logs are kept 30 days. The findings in S3 are the evidence; the logs
  are for debugging.

## The local dashboard

```bash
anvil compliance dashboard
```

```
  ✔ Latest scan: 20261008T123221Z-scheduled
  ✔ Mapped 56 resources to Anvil components

  Dashboard running at http://127.0.0.1:53817/?token=4f9c…#/overview
  Press Ctrl+C to stop.
```

### What happens when it starts

1. **Finds the app:** project from `anvil.yaml`, stage from `--stage` or the
   active stage, region from the stage.
2. **Checks there's something to show.** If the shared scanner doesn't exist or
   the app has no scans, it says so and exits.
3. **Maps resources to components.** It exports the stage's Pulumi state and,
   for every resource with an ARN, walks up its parents to the outermost Anvil
   component (the SvelteKitSite, not a Lambda nested inside it). ARNs are
   normalised (a trailing `:*` on log groups is dropped). It also reads the stack
   history for the last deploy time. If the state can't be read, the dashboard
   still works and falls back to Anvil's `Component` tags.
4. **Starts a server** on `127.0.0.1` (a free port, or `--port`) with a random
   token, prints the URL and opens the browser (`--no-open` skips that).
5. **Runs until Ctrl+C.**

The page is a single self-contained HTML file embedded in the CLI
(`cmd/anvil/compliance/dashboard/index.html`), with styles, script and logo
inline. It loads nothing from the internet and works offline.

### Local API

Every call needs the token in an `X-Anvil-Token` header. The CLI makes every AWS
call with your credentials; the browser never sees credentials.

| Endpoint                     | Returns                                                                       |
| ---------------------------- | ----------------------------------------------------------------------------- |
| `GET /`                      | The page (no data)                                                            |
| `GET /api/scans`             | The app's 50 most recent scans (ID, trigger, time), newest first              |
| `GET /api/scan?id=<scan-id>` | The view data for a scan (default: the latest)                                |
| `POST /api/rescan`           | Starts a manual scan with the latest scan's frameworks, regions and retention |

Stored scans never change, so each scan's view data is cached in memory for the
life of the server. Findings are never written to disk.

### How the view data is built

`compliance.BuildView` (`cmd/anvil/compliance/view.go`) combines:

- **The scan's findings**, reduced to what the page uses: check ID and title,
  status, severity, result detail, risk, remediation, references, the resource,
  and its framework requirements.
- **Framework labels** from a table generated from the Prowler image
  (`frameworkinfo.go`). Findings reference frameworks by a key such as `SOC2`,
  `CIS-7.0` or `ISO27001-2022`; the table maps scanned framework IDs to those
  keys and display names.
- **The component for each resource:** from Pulumi state; otherwise from Anvil's
  `Component` tag (the bootstrap state bucket shows as "State bucket ·
  Bootstrap"); otherwise "Other".
- **Changes since the previous scan:** findings failing now but not in the next
  older complete scan ("new"), and the reverse ("fixed").
- **The last deploy time**, for the freshness badge.

### What it shows

- **Header:** the app and stage, account and region; a scan picker; a freshness
  badge ("Up to date" when the scan ran after the last deploy, amber
  "Deployed since this scan" otherwise); Rescan; a light/dark toggle.
- **Overview:**
  - A card per framework: `% of checked requirements passing` with a bar.
  - Failing checks by severity; each count opens Findings filtered to it.
  - Changes since the previous scan (or "First scan").
  - Coverage: resources scanned, which had no applicable checks, and how many
    account-level findings were left out.
  - Components with the most failures.
- **Components:** a tree of components and their resources with failure counts;
  the selected component's failing checks by resource.
- **Findings:** every check result with search, severity and framework filters,
  grouping by check, component or resource, and a toggle for passing checks.
  Rows show up to three framework chips plus "+N more".
- **Frameworks:** a tab per framework, each requirement passing or failing, and
  the checks behind it. A note explains that Prowler only automates some
  requirements, so a passing score isn't certification.
- **Finding detail:** a side drawer with the result, the resource and its
  component (with a copyable ARN), why it matters, how to fix it, its framework
  requirements, and reference links.
- **Credit:** "Scans powered by Prowler <version> · Open source · Apache-2.0" in
  the sidebar, using Prowler's name only, not its logo. It isn't required by the
  licence, since Anvil runs Prowler's image unmodified and doesn't redistribute
  its code, but auditors want to know which tool produced the evidence.

**Scores are strict:** a requirement passes only if every check mapped to it
passes for this app. One failing bucket fails every requirement that check maps
to, which is the honest reading but can look harsh.

**Scans that didn't complete** show a message instead of results: "No resources
matched" for `empty`, or the error for `failed`.

### Branding

Anvil's colours: Charcoal `#17181A` and Steel `#E8E6E3` for the dark theme,
Light `#ECEBE8` for the light theme, Forge `#FF6A1A` as the accent (active
navigation, Rescan, links, focus rings). Severity colours are kept away from
Forge so orange always means Anvil. The wordmark and favicon come from the Anvil
logo pack, inlined as SVG.

## Commands

| Command                      | Does                                                                                                                                                                       | Flags                                                           |
| ---------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------- |
| `anvil compliance dashboard` | Starts the local dashboard                                                                                                                                                 | `--stage`, `--port`, `--no-open`                                |
| `anvil compliance scan`      | Starts a scan of this app. Settings come from the App's `compliance` (read by running discovery); flags override them, and without `compliance` `--frameworks` is required | `--frameworks`, `--regions`, `--retention`, `--wait`, `--stage` |
| `anvil compliance status`    | Shows the scanner's version, the apps with schedules, and this app's latest scan                                                                                           | `--stage`                                                       |
| `anvil compliance setup`     | Creates or updates the shared scanner explicitly                                                                                                                           | `--yes`, `--stage`                                              |
| `anvil compliance teardown`  | Removes the shared scanner                                                                                                                                                 | `--yes`, `--delete-results`, `--stage`                          |
| `anvil deploy`               | Also ensures the scanner and schedules scans when `compliance` is set                                                                                                      | `--yes`                                                         |

`scan --wait` checks the build every 5 seconds and prints each phase. On success
it prints the summary; on failure the last 25 log lines and a link to the full
log.

## Security model

### The scan role

| Statement                                       | Effect                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| ----------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `SecurityAudit`, `ViewOnlyAccess` (AWS managed) | Read configuration                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| `ProwlerReadOnly`                               | Prowler's published extra read-only policy (5.44.0), **minus every action that reads data or writes**: `lambda:GetFunction*` (replaced by configuration-only Lambda actions), `lambda:GetLayerVersion`, `logs:FilterLogEvents`, `ecr:BatchGetImage`, `ecr:GetDownloadUrlForLayer`, `s3:ListBucket`, `ssm:GetDocument`, `datapipeline:GetPipelineDefinition`, `securityhub:BatchImportFindings` (a write), `securityhub:GetFindings`. Adds `tag:GetResources` |
| `ProwlerAPIGatewayReadOnly`                     | `apigateway:GET` on REST and HTTP APIs                                                                                                                                                                                                                                                                                                                                                                                                                       |
| `NoDataReads` (**Deny**)                        | Object reads and listings, log contents and queries, Lambda code and layers, ECR images, SSM parameters and documents, Secrets Manager values, DynamoDB items, SQS messages, Kinesis records, Athena results, CloudFormation templates, pipeline definitions. An explicit Deny overrides any Allow, so this holds even if AWS widens its managed policies                                                                                                    |
| `WriteResults`                                  | `s3:PutObject` and `s3:PutObjectTagging` on `results/*` in the results bucket only                                                                                                                                                                                                                                                                                                                                                                           |
| `WriteBuildLogs`                                | Its own log group only                                                                                                                                                                                                                                                                                                                                                                                                                                       |

Its trust policy allows only `codebuild.amazonaws.com`, and only for this
account (`aws:SourceAccount`) and this project (`aws:SourceArn`), which protects
against the "confused deputy" problem.

Prowler's checks for secrets in code, logs and images can't run under this
role. None of them are part of SOC 2, ISO 27001 or CIS.

### The invoke role

Allows only `codebuild:StartBuild` on this one project. Its trust policy allows
only `scheduler.amazonaws.com` from this account.

### Starting builds

Anyone allowed `codebuild:StartBuild` on the project can also replace its build
file when starting a build, and so run any command as the scan role. The scan
role's limits cap what that could do (read configuration, write results), but
`StartBuild` should stay limited to the invoke role and deployers. Whether an
IAM condition can block build-file replacement is still untested.

### The image

Prowler's image is pinned **by digest** and pulled from AWS's public registry
(`public.ecr.aws/prowler-cloud/prowler`), which avoids Docker Hub's pull limits
from shared CodeBuild addresses. The digest changes only with an Anvil release,
which keeps the dashboard and Prowler's output format in step.

### The dashboard

| Risk                                                  | Protection                                                                                                             |
| ----------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------- |
| Reachable from the network                            | Listens on `127.0.0.1` only                                                                                            |
| Another user or process on the machine                | A random token per run is needed for any data                                                                          |
| A malicious website calling the API from your browser | The token travels in a custom header, which another site can't send without a CORS preflight, and none is ever allowed |
| A website reaching it through DNS rebinding           | Requests for any host other than `127.0.0.1:<port>` are refused                                                        |
| The token leaking through links                       | `Referrer-Policy: no-referrer`; the token is removed from the address bar after loading and kept only for that tab     |
| The page loading or sending anything elsewhere        | A strict content security policy: no external scripts, styles, images or connections                                   |
| Findings left on disk                                 | Held in memory only                                                                                                    |

Remaining gaps: the first URL, with the token, may be saved in browser history
(the token stops working when the server stops), and a forgotten server keeps
running until Ctrl+C. See [Future work](#future-work).

## Costs

| Item                                     | Cost                                                                        |
| ---------------------------------------- | --------------------------------------------------------------------------- |
| CodeBuild project, roles, schedule group | Nothing while idle                                                          |
| Each scan                                | CodeBuild minutes: about 1–2 minutes on a small ARM build, under a cent     |
| A daily schedule                         | Roughly 10–30 cents a month per app                                         |
| Results                                  | ~100 KB per scan; a year of daily scans is well under 50 MB                 |
| Encryption                               | Free (AWS-managed key, Bucket Keys)                                         |
| Build logs                               | A few KB per scan, kept 30 days                                             |
| Schedules                                | Billed per run; EventBridge Scheduler includes 14 million free runs a month |
| Image pull                               | Free from AWS's public registry into CodeBuild                              |

Check current CodeBuild pricing for your region.

## Known limitations

- **Six cross-cloud frameworks aren't scored in the dashboard:** `dora`, `cmmc`,
  `csa-ccm`, `cis_controls_8.1`, `fedramp_20x_frr_class_c_2026`,
  `fedramp_20x_ksi_2026`. Their checks run, but Prowler doesn't attach their
  requirements to findings, so their cards say so instead of scoring.
- **CloudFront-scoped WAF WebACLs get no findings,** even though they're scanned.
  Prowler probably doesn't recognise `global/webacl` ARNs. Not yet investigated.
- **IAM role checks run, but aren't part of SOC 2, ISO 27001 or CIS,** so they
  only appear with frameworks that map them.
- **Raw Pulumi resources** created directly in `run()` don't carry `ManagedBy`
  and aren't scanned.
- **New resources can take a few minutes** to appear in AWS's tagging API, so a
  scan straight after a first deploy may miss them. CodeBuild's startup usually
  covers the gap.
- **The dashboard reads the stage's region,** not the region in the discovery
  manifest. They're the same unless an app's default AWS provider uses a
  different region from its stage.
- **Accounts with a CodeBuild concurrency limit of 1** run scans one at a time,
  and the schedule's retries cover collisions. CodeBuild CI in the same account
  competes for the same limit.

## Code map

| Path                                                        | What                                                                              |
| ----------------------------------------------------------- | --------------------------------------------------------------------------------- |
| `cmd/anvil/compliance/scan.py`                              | The scanner run inside CodeBuild                                                  |
| `cmd/anvil/compliance/scan_test.py`                         | Its unit tests (run inside the Prowler image)                                     |
| `cmd/anvil/compliance/embed.go`                             | Embeds `scan.py`; pins the Prowler image; builds the build file                   |
| `cmd/anvil/compliance/shared.go`                            | Creates, updates, inspects and tears down the shared scanner                      |
| `cmd/anvil/compliance/policies.go`                          | IAM trust and inline policies, bucket policy                                      |
| `cmd/anvil/compliance/names.go`                             | Resource names and ARNs from account and region                                   |
| `cmd/anvil/compliance/frameworks.go`                        | Friendly names, the exact Prowler ID list, retention tiers                        |
| `cmd/anvil/compliance/frameworkinfo.go`                     | Framework display names and finding keys (generated from the image)               |
| `cmd/anvil/compliance/scan.go`                              | Starting and following scans, reading the latest manifest                         |
| `cmd/anvil/compliance/results.go`                           | Listing and loading stored scans                                                  |
| `cmd/anvil/compliance/view.go`                              | Building the dashboard's view data                                                |
| `cmd/anvil/compliance/dashboard.go`, `dashboard/index.html` | The embedded dashboard page                                                       |
| `cmd/anvil/cmd/compliance.go`                               | `setup`, `scan`, `status`, `teardown`; deploy helpers                             |
| `cmd/anvil/cmd/compliance_dashboard.go`                     | `dashboard`: state mapping, server, API                                           |
| `cmd/anvil/cmd/deploy.go`, `build.go`                       | Deploy steps and the manifest's `compliance` section                              |
| `provider/aws/complianceScanner/`                           | The `anvil:aws:ComplianceScanner` component, its settings logic, schema and tests |
| `provider/internal/shared/manifest.go`                      | The lock shared by every discovery-manifest writer                                |
| `sdk/overlays/{nodejs,python,go}/app.*`                     | The `compliance` option on `App`                                                  |

## Testing

**Unit tests:**

```bash
go test ./cmd/anvil/compliance ./cmd/anvil/cmd
```

```bash
cd provider && go test ./aws/complianceScanner
```

```bash
docker run --rm -v "$PWD/cmd/anvil/compliance":/scan:ro -w /scan --entrypoint /home/prowler/.venv/bin/python prowlercloud/prowler:5.44.0 -B -m unittest scan_test
```

These cover input checking and injection attempts, tag filtering, the Prowler
command line, the embedded scanner round trip, the IAM policies (no data reads,
writes limited to results), lifecycle rules, framework mapping and suggestions,
schedule expressions and names, the scheduler input's PascalCase format,
version comparison, scan ID parsing, and the view builder (component mapping,
fallbacks, framework mapping, new and fixed findings).

**Running the scanner locally** against a real stack, writing results to a
folder instead of S3 (read-only against your account):

```bash
docker run --rm -v ~/.aws:/home/prowler/.aws:ro -v "$PWD/cmd/anvil/compliance":/scan:ro -v "$PWD/out":/results -e ANVIL_PROJECT=testwaf -e ANVIL_STAGE=damienpace -e ANVIL_REGIONS=ap-southeast-2 -e "ANVIL_FRAMEWORKS=soc2_aws iso27001_2022_aws cis_7.0_aws" -e ANVIL_RESULTS_DIR=/results --entrypoint /home/prowler/.venv/bin/python prowlercloud/prowler:5.44.0 -B /scan/scan.py
```

Object tags are written next to each file as `*.tags.json`.

## Upgrading Prowler

When moving to a new Prowler release:

1. Update `ProwlerImage` (the digest) and `ProwlerVersion` in
   `cmd/anvil/compliance/embed.go`. Use the digest from
   `public.ecr.aws/prowler-cloud/prowler` and check it matches Docker Hub's.
2. Regenerate the framework list from `prowler aws --list-compliance`, and update
   it in **both** `cmd/anvil/compliance/frameworks.go` and
   `provider/aws/complianceScanner/frameworks.go`. Point friendly names at the
   newest versions. Both packages have a test that every friendly name maps to a
   listed ID.
3. Regenerate `frameworkinfo.go` from the image's `prowler/compliance/**/*.json`
   (each framework's `Framework`, `Version` and `Name`).
4. Diff Prowler's `permissions/prowler-additions-policy.json` against
   `prowlerReadActions` in `policies.go`. Add new read-only configuration
   actions; leave out anything that reads data or writes.
5. Run the unit tests and a local scan, and check the OCSF fields `view.go`
   relies on haven't changed.

Releasing a CLI with a newer version makes every deploy update the shared
scanner automatically, via the version tag.
=======
Prowler scans **deployed resources**, not code or plans. This is a post-deploy
posture check, not a pre-deploy policy gate.

### Hard constraints

- **Prowler never ships in the CLI.** It's Python with a large dependency tree.
  It runs in the cloud, in the upstream container image; the CLI only reads
  results.
- **Nothing in the scanner writes to scanned resources.** The scan role is
  read-only, plus writes to its own results prefix.

### Non-goals (v1)

- Org-wide scanning (management / delegated-admin account). Planned as a separate
  app type later — see [Future work](#future-work).
- GCP. Prowler supports GCP, but label-scoped scanning is unverified.
- A CI gate (`--fail-on`). Explicitly dropped.
- Object Lock / immutable results. Deferred; enabling it on an existing bucket is
  supported by S3, so nothing here blocks it.
- Pre-deploy (IaC) scanning.

## Config API

`compliance` is a **top-level `App` option**, not a provider entry. `providers`
maps to cloud credentials/regions and rejects unknown prefixes.

```ts
compliance: {
  frameworks: ['soc2', 'iso27001', 'cis'],  // required, non-empty — explicit opt-in
  schedule: 'daily',                        // 'daily' | 'weekly' | 'none' | { cron, timezone }
  retention: '1y',                          // '30d' | '90d' | '180d' | '1y' | '2y' | '7y'
  scanOnDeploy: false,
}
```

| Option         | Default             | Notes                                                            |
| -------------- | ------------------- | ---------------------------------------------------------------- |
| `frameworks`   | **none — required** | Friendly names mapped to pinned Prowler IDs per Anvil release    |
| `schedule`     | `'daily'`           | Presets, or `{ cron, timezone }` passed to EventBridge Scheduler |
| `retention`    | `'1y'`              | Fixed tiers. SOC 2 Type II audits typically look back 12 months  |
| `scanOnDeploy` | `false`             | Fire-and-forget scan after a successful `anvil deploy`           |

### Frameworks

> **For the user docs:** `frameworks` accepts **two forms**, mixed freely:
>
> - **Friendly names** — short, and always point at the newest version Anvil
>   pins. Upgrading Anvil can move e.g. `cis` from CIS 7.0 to a later CIS in
>   lockstep with the pinned Prowler image.
> - **Raw Prowler IDs** — any AWS compliance ID from the pinned Prowler version
>   (`prowler aws --list-compliance`). These stay fixed across Anvil upgrades, so
>   use them to pin an older version (`cis_6.0_aws`) or for frameworks without a
>   friendly name (`ens_rd2022_aws`, `ffiec_aws`, `mitre_attack_aws`, …).
>
> ```ts
> compliance: {
>   frameworks: ['soc2', 'cis', 'ens_rd2022_aws'];
> }
> ```
>
> Unknown values fail at deploy with a "did you mean …?" suggestion — never
> later inside the scan.

Friendly names (Prowler 5.44.0):

| Friendly           | Prowler ID                                           |
| ------------------ | ---------------------------------------------------- |
| `soc2`             | `soc2_aws`                                           |
| `iso27001`         | `iso27001_2022_aws`                                  |
| `cis`              | `cis_7.0_aws`                                        |
| `nist-800-53`      | `nist_800_53_revision_5_aws`                         |
| `nist-800-171`     | `nist_800_171_revision_2_aws`                        |
| `nist-csf`         | `nist_csf_2.0_aws`                                   |
| `pci`              | `pci_4.0_aws`                                        |
| `hipaa`            | `hipaa_aws`                                          |
| `fsbp`             | `aws_foundational_security_best_practices_aws`       |
| `gdpr`             | `gdpr_aws`                                           |
| `nis2`             | `nis2_aws`                                           |
| `dora`             | `dora_2022_2554`                                     |
| `fedramp-low`      | `fedramp_low_revision_4_aws`                         |
| `fedramp-moderate` | `fedramp_moderate_revision_4_aws`                    |
| `cmmc`             | `cmmc_2.0`                                           |
| `essential-eight`  | `asd_essential_eight_aws`                            |
| `well-architected` | `aws_well_architected_framework_security_pillar_aws` |
| `c5`               | `c5_aws`                                             |
| `csa-ccm`          | `csa_ccm_4.0`                                        |

Raw IDs are validated against the **exact list** for the pinned Prowler version,
not a pattern: several real IDs don't end in `_aws` (`dora_2022_2554`,
`cmmc_2.0`, `csa_ccm_4.0`, `cis_controls_8.1`, `fedramp_20x_*`). The map and list
live in `provider/aws/complianceScanner/frameworks.go` and
`cmd/anvil/compliance/frameworks.go` — keep them identical, and update both when
the Prowler image is bumped.

**Cost of multiple frameworks is small.** Prowler runs _checks_; frameworks are
mappings of requirements onto checks. `--compliance a b c` runs the **union** of
mapped checks, and SOC 2 / ISO / CIS overlap heavily. Worst case equals an
unfiltered run.

### Schedule

- Presets: `'daily'`, `'weekly'`, `'none'` (deploy-triggered and on-demand only).
- Advanced: `{ cron: '0 3 * * ? *', timezone: 'Australia/Sydney' }`.
- **Jitter:** presets get a deterministic start time derived from
  `hash(project + stage)`, so many apps don't all fire at 00:00 and an app's time
  is stable across deploys.
- **Minimum interval** ~6h; more frequent is rejected at construction.

## Architecture

Two layers with **different owners**, because a shared resource can't be owned by
any one app's Pulumi stack (the second app would fail with `AlreadyExists`, and
destroying the first would delete it from under everyone).

```
                ┌────────────────────── shared, per account + region ──────────────────────┐
                │  (managed by the CLI via direct AWS SDK calls — no Pulumi state)          │
anvil deploy ───┼─► CodeBuild project ── scan role (read-only)                              │
 (ensure step)  │   Invoke role ── Scheduler group `anvil-compliance` ── Log group (30d)    │
                │   Results bucket (encrypted, versioned, lifecycle tiers)                  │
                └───────────────────────────────────────────────────────────────────────────┘
                                ▲ StartBuild(env overrides)
                ┌───────────────┴──────── per app (project + stage) ────────────────────────┐
anvil deploy ───┼─► anvil:internal:ComplianceScanner (Pulumi component in the app stack)    │
 (pulumi up)    │     └─ EventBridge Scheduler schedule in group `anvil-compliance`          │
                └───────────────────────────────────────────────────────────────────────────┘

anvil compliance ── read latest results → filter to project/stage → 127.0.0.1 dashboard
```

## Shared layer (per account + region)

Managed **imperatively by the CLI**, in the same style as `anvil bootstrap`. No
Pulumi stack, no state bucket.

### Resources

All named `anvil-compliance-<account>-<region>` unless noted, and tagged
`ManagedBy=anvil`, `anvil:component=compliance`.

| Resource          | Converge steps                                                                                                                                                 |
| ----------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Results bucket    | create → public access block, SSE-KMS, versioning, ownership controls, bucket policy (TLS-only), lifecycle tiers                                               |
| Scan role         | create → attach `SecurityAudit` + `ViewOnlyAccess` → inline policy (Prowler's additional read-only permissions, `tag:GetResources`, results bucket write, KMS) |
| Invoke role       | create → inline policy (`codebuild:StartBuild` on the project only)                                                                                            |
| Log group         | create → retention 30 days                                                                                                                                     |
| CodeBuild project | create / `UpdateProject` — no VPC, upstream Prowler image pinned by digest, inline buildspec, ~30 min timeout, `concurrentBuildLimit` 3–5                      |
| Scheduler group   | `anvil-compliance` — create                                                                                                                                    |

The results bucket's secure defaults must match the `Bucket` component's. Extract
bootstrap's state-bucket hardening into a shared helper and use it for both.

### Converge semantics

- Every step is **idempotent**: `AlreadyExists` is success, then desired settings
  are applied. Most calls are whole-document replaces (`PutBucketLifecycleConfiguration`,
  `PutRolePolicy`, `UpdateProject`), so re-applying also **corrects drift**.
- **Partial failure self-heals** on the next deploy.
- **Concurrent first deploys** race harmlessly — the loser sees `AlreadyExists`.
  No locks.
- **Removals need explicit code.** If a release drops a policy statement or
  resource, it ships a matching cleanup step. (This is the main thing given up by
  not using Pulumi here.)

### Version marker and fast path

Tag on the CodeBuild project: `anvil:compliance-version=<cli version>`.

| Marker              | Action                                                            |
| ------------------- | ----------------------------------------------------------------- |
| Missing             | Full converge (first-time create — **prompts**, see below)        |
| Older than this CLI | Full converge (upgrade: new Prowler digest, tiers, policy)        |
| Same or newer       | Skip entirely. An older CLI never downgrades; warn on a large gap |

### Bucket-name squatting

S3 names are global, so anyone who knows the account ID could pre-create
`anvil-compliance-<account>-<region>`. **Every S3 call passes
`ExpectedBucketOwner`.** If the name is owned by another account, fail with a
clear error — never write to it.

### Teardown

`anvil compliance teardown` deletes **by deterministic name** (no tag search —
the tagging API doesn't reliably return IAM roles). Tags are the **safety check**:
a same-named resource without `anvil:component=compliance` is skipped with a
warning.

- Refuses while the `anvil-compliance` schedule group contains any schedules
  (apps still registered).
- Keeps the results bucket unless `--delete-results` (which must empty all
  versions first).

## Per-app layer

`anvil:internal:ComplianceScanner` — a **provider component** defined once in Go
and the schema, so TS / Python / Go all get it from codegen. Namespaced
`internal`; `App` constructs it only when `compliance` is set. Users aren't
expected to construct it directly.

It creates **one EventBridge Scheduler schedule** in group `anvil-compliance`:

- **Target:** universal target `codebuild:startBuild` on the shared project, using
  the shared invoke role. ARNs are built from account + region — no cross-stack
  references.
- **Input:** the app's scope as environment variable overrides (below).
- **Retry policy** covers transient `StartBuild` failures (e.g. account build
  concurrency).
- **Lifecycle:** a normal Pulumi resource — added, updated and removed with the
  app.

Because each app owns its schedule, per-app settings **never conflict**.

### Build-mode manifest

In `ANVIL_BUILD_MODE`, the component writes a `compliance` section to
`.anvil/build-manifest.json` (same mechanism as Lambda discovery in
`cmd/anvil/cmd/build.go`). This tells the CLI **before deploy** that the shared
layer must exist, and gives `anvil compliance scan` the app's settings.

```json
"compliance": {
  "frameworks": ["soc2_aws", "iso27001_2022_aws", "cis_3.0_aws"],
  "schedule": "daily",
  "retention": "1y",
  "scanOnDeploy": false,
  "regions": ["ap-southeast-2"]
}
```

## The scan

### Scope (tags)

Every Anvil resource carries `ManagedBy`, `project`, `stage`. Prowler's
`--resource-tags` **ANDs** multiple tags, so each scan targets exactly one app:

```bash
prowler aws \
  --resource-tags ManagedBy=anvil project=testwaf stage=damienpace \
  --region ap-southeast-2 \
  --compliance soc2_aws iso27001_2022_aws cis_3.0_aws \
  --output-formats json-ocsf
```

Multiple apps in one account are isolated by `project` + `stage`. Scoping to the
app's regions is the biggest runtime win.

**`ManagedBy` coverage.** Every Anvil component sets `ManagedBy=anvil` on the
resources it creates. Only raw Pulumi resources added directly in `run()` lack it
(`App`'s auto tags are `{ stage, project }`), and those fall out of scope. That's
arguably correct — they aren't Anvil-managed — but it's worth stating in the user
docs. New components must keep setting it.

### Scanner script

`cmd/anvil/compliance/scan.py` — stdlib + boto3 only, so it runs in the
unmodified Prowler image. Resolves ARNs, runs Prowler with `--resource-arns`
and `--ignore-exit-code-3`, drops out-of-scope findings, writes findings →
manifest → `latest.json`. `ANVIL_RESULTS_DIR` swaps S3 for a local directory
(object tags written as `*.tags.json` sidecars). A failed scan still writes a
manifest (`status: failed`) but never moves `latest.json`. Run locally:

```bash
docker run --rm -v ~/.aws:/home/prowler/.aws:ro -v "$PWD/cmd/anvil/compliance":/scan:ro -v "$PWD/out":/results \
  -e ANVIL_PROJECT=testwaf -e ANVIL_STAGE=damienpace -e ANVIL_REGIONS=ap-southeast-2 \
  -e "ANVIL_FRAMEWORKS=soc2_aws iso27001_2022_aws cis_7.0_aws" -e ANVIL_RESULTS_DIR=/results \
  --entrypoint /home/prowler/.venv/bin/python prowlercloud/prowler:5.44.0 -B /scan/scan.py
```

First run on `testwaf`: 9 resources, 36 findings (16 pass / 20 fail), 4
account-level findings dropped, 57s.

### Build inputs

Passed as `environmentVariablesOverride` by both the schedule and the CLI:

| Variable           | Example                                  |
| ------------------ | ---------------------------------------- |
| `ANVIL_PROJECT`    | `testwaf`                                |
| `ANVIL_STAGE`      | `damienpace`                             |
| `ANVIL_REGIONS`    | `ap-southeast-2`                         |
| `ANVIL_FRAMEWORKS` | `soc2_aws iso27001_2022_aws cis_3.0_aws` |
| `ANVIL_RETENTION`  | `1y`                                     |
| `ANVIL_TRIGGER`    | `scheduled` \| `deploy` \| `manual`      |

**The buildspec validates every variable against a strict pattern** (and
frameworks/retention against allowlists) before it reaches the Prowler command
line. A crafted tag value must not become shell injection.

### Results layout

```
s3://anvil-compliance-<account>-<region>/
  results/<project>/<stage>/<scan-id>/     # scan-id = <utc timestamp>-<trigger>
    findings.ocsf.json
    manifest.json                          # written LAST
  results/<project>/<stage>/latest.json    # pointer to newest complete scan
```

- `manifest.json`: scope, regions, frameworks, Prowler version, status, duration.
  **A scan counts as complete only once its manifest exists** — the dashboard
  never reads a half-written result.
- Every object is tagged `retention=<tier>` at upload.

## Retention

Per-object, via **object tags + fixed lifecycle rules**. A bucket has one
lifecycle configuration, so per-app rules would clobber each other; tiers avoid
any cross-stack coordination.

- One rule per tier: `retention=30d|90d|180d|1y|2y|7y`. Adding a tier is cheap.
- Rules must include **`NoncurrentVersionExpiration`** and **expired delete marker
  cleanup** — on a versioned bucket, expiration alone only adds a delete marker
  and nothing is actually removed.
- Changing an app's retention **applies to new scans only**. Existing results
  keep their tier — shortening retention never suddenly deletes audit evidence.
- CloudWatch build logs: fixed **30 days**. Findings in S3 are the evidence; logs
  are for debugging.

## Compute: CodeBuild (no VPC)

Prowler calls AWS APIs across every scanned region, so it needs internet egress;
per-service, per-region VPC endpoints aren't practical.

|             | CodeBuild, no VPC (**chosen**)                                          | Fargate, public subnet                                               |
| ----------- | ----------------------------------------------------------------------- | -------------------------------------------------------------------- |
| Networking  | AWS-managed, egress included                                            | Needs a VPC (often deleted per CIS), IGW, subnets, routes, flow logs |
| Self-scan   | Clean                                                                   | Fails Security Hub **ECS.2** (public IP on tasks)                    |
| Footprint   | Project + role                                                          | Cluster, task def, 2 roles, SG, networking                           |
| Wrapper     | Inline buildspec around the upstream image — no custom image to publish | Custom image or sidecar                                              |
| Concurrency | `concurrentBuildLimit`                                                  | Build it yourself                                                    |
| Cost        | ~$0.01/min (medium) — a 20 min daily scan ≈ $6/mo                       | Cheaper per minute; irrelevant at this scale                         |

If a future org requires VPC-only compute, add `compliance: { network: 'vpc' }`
(CodeBuild-in-VPC + NAT) rather than changing the default.

**Gotcha:** new AWS accounts can have a low account-level CodeBuild concurrency
quota. Scans compete with any CodeBuild CI in the account. Document it; the CLI
reports "scan already running / throttled" rather than failing.

## Permissions and security

- **Scan role:** read-only (`SecurityAudit`, `ViewOnlyAccess`, Prowler's
  additional read-only policy, `tag:GetResources`) + `s3:PutObject` /
  `s3:PutObjectTagging` on `results/*` + KMS data key on the bucket key.
- **Invoke role:** `codebuild:StartBuild` on the one project. Assumable only by
  `scheduler.amazonaws.com` with an `aws:SourceAccount` condition.
- **`StartBuild` implies buildspec override** — any principal with it can run
  arbitrary commands as the scan role. Keep `StartBuild` limited to the invoke
  role and deployers. Investigate whether an IAM condition can deny buildspec
  overrides.
- **Image supply chain:** upstream Prowler image **pinned by digest**, bumped only
  with an Anvil release (keeps the dashboard parser and Prowler's output format
  in lockstep). Later: mirror into a private ECR repo (immutable tags, image
  scanning) for availability and rate limits.

## CLI

### Deploy flow

```
anvil deploy
  1. discovery          (existing)  → manifest, now incl. compliance
  2. build functions    (existing)
  3. ensure shared      (new)       only if manifest has compliance; fast path via version tag
  4. up app stack       (existing)  → creates/updates the app's schedule
  5. scan on deploy     (new)       only if scanOnDeploy → StartBuild, don't wait
                                    "Compliance scan started — run `anvil compliance` to view"
```

Step 3 must precede step 4: the schedule targets the shared project.

### First-time confirmation

Only on **first creation** of the shared layer (not upgrades):

```
  This app enables compliance scanning. No scanner exists yet in
  account 123456789012 (ap-southeast-2), so Anvil will create shared resources:

    • CodeBuild project + read-only scan role   anvil-compliance-123456789012-ap-southeast-2
    • Results bucket (encrypted, versioned)     anvil-compliance-123456789012-ap-southeast-2
    • Scheduler group + invoke role             anvil-compliance

  These are shared by every Anvil app in this account and region.
  Create them? (y/N)
```

- `--yes` skips the prompt (CI, scripts) — matching `destroy` / `unlock`.
- **Non-TTY without `--yes` → error**, never silent creation:
  `Compliance requires shared account resources. Re-run with --yes to create them.`
- No → abort before any change; hint to remove `compliance` or re-run with
  `--yes`.
- `CI=true` does **not** auto-accept. Creating account-wide resources should be
  an explicit decision in a pipeline.

### Commands

| Command                      | Does                                                                                                                                                                                                                      |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `anvil compliance dashboard` | Local dashboard on 127.0.0.1 (random port, per-run token): latest scan for this project/stage, scan picker, components from Pulumi state, changes vs the previous complete scan, Rescan. `--stage`, `--port`, `--no-open` |
| `anvil compliance scan`      | `StartBuild` scoped to this app using manifest settings. `--wait` streams progress; `--all` scans every registered app                                                                                                    |
| `anvil compliance status`    | Running builds, last scan per app (time, result, duration), next scheduled run                                                                                                                                            |
| `anvil compliance teardown`  | Remove the shared layer. Refuses while apps are registered. `--delete-results`                                                                                                                                            |

**Registry:** the schedules in group `anvil-compliance` _are_ the list of
registered apps — used by `status`, `scan --all`, and teardown's safety check.

### Lifecycle edge cases

| Change                                | Result                                                                                                 |
| ------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| `compliance` added to an existing app | Next deploy ensures shared layer (prompt if new) and creates the schedule                              |
| Settings changed                      | Schedule updated in place                                                                              |
| `compliance` removed                  | Schedule removed; shared layer and past results remain                                                 |
| `anvil destroy`                       | Schedule removed; if the group is now empty, hint at `anvil compliance teardown` — never auto-teardown |

## Local dashboard

**Built:** `anvil compliance dashboard` (`cmd/anvil/cmd/compliance_dashboard.go`, page in
`cmd/anvil/compliance/dashboard/index.html`, data model in `compliance/view.go`). Verified on
`testWaf` against manual, deploy and scheduled scans. Scores are strict: a requirement passes only
when every mapped check passes. Cross-provider frameworks (DORA, CMMC, CSA CCM, CIS Controls,
FedRAMP 20x) aren't scored: Prowler doesn't attach their requirements to findings. "Powered by
Prowler" credit in the sidebar (name only, no logo).

A small static HTML/JS bundle **embedded in the binary** (`embed`), served by the
CLI. No Python, no Prowler locally.

- Binds **`127.0.0.1` only**, random port, **random token in the URL**. Opens the
  browser; Ctrl+C stops it.
- The CLI downloads findings with the user's AWS credentials and serves them via
  local `/api/*`. **The browser never sees AWS credentials.**
- Findings are a list of the account's weaknesses — **held in memory, not cached
  to disk**. (`.anvil/` is gitignored if caching is ever needed.)
- **Component mapping:** the CLI exports the app's Pulumi state (`s.Export`) and
  joins finding ARN → resource URN → parent component.

### Data

From Prowler (OCSF, per finding): check ID + title, status (PASS / FAIL / MANUAL
/ muted), status detail, severity, resource (ARN, name, type, service, region,
tags), risk + remediation + references, compliance requirement mappings, scan
metadata.

From Anvil: owning component, component inputs (to classify _why_), resources in
state vs. resources scanned (coverage), last deploy time (staleness).

### Views

v1:

1. **Overview** — score per framework, FAILs by severity, new/fixed since last
   scan, scan age (warn if older than last deploy), coverage %.
2. **By component** — component → resources → failing checks. Each failure
   classified **default** (Anvil's secure default didn't hold → Anvil bug) or
   **override** (user's explicit choice, e.g. `protection: 'none'` → accept or
   fix). The differentiator over Prowler's own dashboard.
3. **Finding detail** — everything above, plus first/last seen and mute reason.

Later: **By framework** (requirements with pass/fail/manual — MANUAL needs human
evidence), **History** (trends across scans). The results layout supports
history from day one.

Filters: severity, status, service, region, framework, stage (+ project with
`--all`).

> > > > > > > @{-1}

## Decisions and rejected alternatives

| Decision | Rejected | Why |
| -------- | -------- | --- |

<<<<<<< HEAD
| Prowler runs in your account | Running Prowler from the CLI (installed, Docker, managed Python) | CLI size; a Python dependency; different results on different machines |
| CodeBuild without a VPC | Fargate in a public subnet | Needs a VPC, fails ECS.2 on itself, much more infrastructure |
| Shared scanner managed by direct AWS calls | A Pulumi stack with an account-level state bucket | A small fixed set of resources; no state to host, lock or orphan; bootstrap already works this way |
| Delete by name, tags as a safety check | Finding resources by tag search | The tagging API doesn't reliably list IAM roles; names are predictable anyway |
| One schedule per app | One account-wide scan split by tags | No settings conflicts; the schedule follows the app; similar total work |
| Collect ARNs, scan with `--resource-arns` | Prowler's `--resource-tags` | Tags miss IAM roles, which the tagging API doesn't return |
| Object-tag retention tiers | One bucket-wide rule ("longest wins") | Exact retention per app with no coordination |
| Settings via the discovery manifest | A hidden stack output read after deploy | Known **before** deploy, when the shared scanner must exist |
| `anvil:aws:ComplianceScanner` | `anvil:internal:ComplianceScanner` | `internal` clashes with the generated Go SDK and breaks the Python SDK patch script |
| Our own embedded dashboard | `prowler dashboard` | Prowler's needs Python and can't group by Anvil component |
| A local server for the dashboard | A static HTML file, a terminal UI, Security Hub, a hosted dashboard | Data stays on the machine, nothing to install or host, and it can read live results, switch scans and start rescans |
| `frameworks` required | Defaulting to SOC 2 + ISO 27001 | Explicit opt-in |
| Explicit Deny on data reads | Trusting the managed policies | Holds even if AWS widens `SecurityAudit` or `ViewOnlyAccess` |
| Results bucket versioned | Unversioned | Results can't be silently overwritten; required for Object Lock later |
| Strict requirement scoring | Share of passing checks | Honest: a requirement only passes if all its checks pass |
| Object Lock deferred | Shipping it now | Not needed yet; can be enabled on the existing bucket later |

## Test history

**Spike (2026-10-08),** Prowler 5.44.0 in local Docker against `testwaf`:

- Findings carry resource tags as `resources[].labels` (`"Key:Value"`), which
  makes per-app filtering possible.
- `--compliance` takes several frameworks; `--output-bucket` exists but can't tag
  objects, so the scanner uploads its own results.
- Exit code 3 means "checks failed", not an error.
- CloudFront, CloudFront-scoped WAF and Lambda@Edge only appear when `us-east-1`
  is scanned.
- IAM roles are invisible to tag-scoped scans; `--resource-arns` scans them.
- Runtime: 14–57 seconds for one app.

**Bugs found and fixed along the way:**

- Internal `us-east-1` providers didn't carry the app's default tags, so the WAF,
  edge Lambda and ACM certificate fell out of scans (and lost any user tags, and
  ignored `assumeRole`/`profile`). Fixed by setting `region: "us-east-1"` per
  resource on the app's own provider.
- Bootstrap resources had no tags. The state bucket now carries the app's tags;
  the logging bucket and trail carry `ManagedBy` and `Component`.
- The Lambda and DSQL components would have deleted other sections of the
  discovery manifest.
- Raw framework IDs not ending in `_aws` were wrongly rejected.
- EventBridge Scheduler rejected camelCase `StartBuild` parameters.

**End to end on `testWaf`:** shared scanner created on first deploy, a scan on
deploy (12:31), a scheduled scan (12:32), a manual scan, and the dashboard
showing all three with components mapped from 56 ARNs in Pulumi state.

**Still untested:** how CodeBuild handles a build beyond the concurrency limit
(queued or rejected); whether IAM can block build-file replacement on
`StartBuild`; why CloudFront-scoped WAFs get no findings.

## Future work

- **Dashboard:** stop after a period with no requests; document use over SSH and
  in remote dev environments (`--no-open` plus port forwarding); show "No
  applicable checks" instead of `0%` for frameworks with nothing to check; group
  requirements by section and level (e.g. CIS "2 Storage, Level 1"); region and
  service filters; scan history and trends.
- **Export:** `anvil compliance export --format csv|html` for auditors. Writes
  findings to disk, so explicit only.
- **Accepted exceptions (designed, deferred).** Accepted findings show as muted
  with a reason, never hidden. Two sources, merged into one Prowler mutelist at
  scan time:
  - _Anvil's own design choices:_ a mutelist shipped with Anvil, keyed by
    `Component=<type>` tags, plus small tags recording relevant inputs (e.g.
    `anvil:protection=none`).
  - _User exceptions:_ `compliance: { accept: [{ check, reason, until? }] }` on a
    component (its resources) or on `App` (the whole app). Recorded during
    discovery, passed back as stack config, and written by the component to
    `config/<project>/<stage>/accept.yaml`.
  - Needs every component to tag resources with `Component=<type>` and a
    logical-name tag.
- **`anvil destroy`** suggests `anvil compliance teardown` when the last app's
  schedule is removed.
- **Org mode** as a separate app type: a scanner in a delegated-admin security
  account (not the management account), a scan role in every member account via
  a StackSet, and the account-level checks that app scans leave out.
- **Object Lock** (`immutable: 'governance' | 'compliance'`), applied per object
  so only apps that opt in are locked. Possibly conditional writes so results
  can be created but never overwritten.
- **Coverage report:** Prowler's inventory compared with Pulumi state, to show
  resources that can't be tagged or were missed.
- **Failure notifications:** EventBridge on failed builds → SNS
  (`compliance: { notify }`).
- **A private ECR mirror** of the Prowler image.
- **GCP.**

## Sources

- [Prowler: tag-based scans](https://docs.prowler.com/user-guide/providers/aws/tag-based-scan)
- [Prowler: reporting and output formats](https://docs.prowler.com/user-guide/cli/tutorials/reporting)
- [Prowler: outputs developer guide](https://docs.prowler.com/developer-guide/outputs)
- # [DefectDojo: Prowler v3+ parser](https://documentation.defectdojo.com/integrations/parsers/file/aws_prowler_v3plus)
  | Scanner runs in-account | Run Prowler from the CLI (PATH / Docker / managed venv) | CLI size; Python dependency; inconsistent environments |
  | CodeBuild, no VPC | Fargate in a public subnet | Needs a VPC, fails ECS.2 on itself, much more infra |
  | Shared layer via direct SDK calls | Pulumi shared stack + account state bucket | Small fixed resource set; no state to host, lock or orphan; bootstrap already uses this style |
  | Delete by name, tags as safety check | Find by tag search | Tagging API doesn't reliably list IAM roles; names are already deterministic |
  | Per-app schedules | One account-wide scan split by tags | No settings conflicts; lifecycle follows the app; similar total work |
  | Object-tag retention tiers | Bucket-wide "longest wins" | Exact per-app retention with no cross-stack coordination |
  | Discovery manifest | Hidden stack output read after deploy | Known **before** deploy, when the shared layer must be ensured |
  | Custom embedded dashboard | `prowler dashboard` | Reintroduces Python; can't show Anvil component mapping |
  | Explicit `frameworks` | Default to SOC 2 + ISO | Opt-in only |
  | Object Lock deferred | Ship governance/compliance modes now | Not needed while testing; can be enabled later on the existing bucket |

## Spike results (2026-10-08)

Ran `prowlercloud/prowler:5.44.0` (digest `sha256:9dbcc3c9abd7…`) locally in
Docker against `testwaf` / `damienpace`:

```bash
prowler aws --resource-tags ManagedBy=anvil project=testwaf stage=damienpace \
  --region ap-southeast-2 [us-east-1] [--compliance soc2_aws iso27001_2022_aws cis_3.0_aws] \
  --output-formats json-ocsf
```

| #   | Question                                 | Result                                                                                                                                                                                                                                                   |
| --- | ---------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | Tags in OCSF output                      | ✅ `resources[].labels` as `"Key:Value"` strings, e.g. `["ManagedBy:anvil","project:testwaf","stage:damienpace","Component:SvelteKitSite"]`. Per-app split on `--all` works. Some resources also carry `Component:<type>` — useful for component mapping |
| 2   | Multiple `--compliance`                  | ✅ Accepts a list. All target IDs exist; CIS is now up to `cis_7.0_aws`, so pin newer than 3.0                                                                                                                                                           |
| 3   | Account-level checks                     | Mostly excluded by the tag filter. **One leaked:** `s3_account_level_public_access_blocks` (empty `labels`). The dashboard drops findings whose labels don't match the app                                                                               |
| 4   | CodeBuild at `concurrentBuildLimit`      | **Untested** (needs throwaway resources in the account)                                                                                                                                                                                                  |
| 5   | Direct-to-S3 output                      | ✅ `--output-bucket/-B` (and `-D`, no-assume). **But it can't tag objects**, and retention depends on object tags → the buildspec uploads itself (`put-object` with `Tagging`)                                                                           |
| 6   | IAM condition to deny buildspec override | **Untested**                                                                                                                                                                                                                                             |
| 7   | Runtime                                  | **14s** (3 frameworks, 1 region, 19 checks) / **23s** (all checks, 2 regions, 48 checks) for 3 resources. Cost is negligible; timeout can be far below 2h                                                                                                |

### New findings

- **Exit code 3 = FAIL findings present**, not an error. The buildspec must
  treat `0` and `3` as success.
- **Global services live in `us-east-1`.** CloudFront (and CloudFront-scoped WAF,
  Lambda@Edge) only appeared once `us-east-1` was added. Scan regions must be
  `app regions ∪ us-east-1`.
- **IAM roles are invisible to tag-scoped scans.** Prowler's tag filter uses the
  Resource Groups Tagging API, which returned exactly the 3 resources Prowler
  scanned — and not the stack's 2 IAM roles, despite correct tags. Option: the
  buildspec resolves ARNs itself (tagging API per region + IAM roles filtered by
  tag) and passes `--resource-arns` instead of `--resource-tags` (the two flags
  are mutually exclusive). **Needs a follow-up test** that IAM checks run under
  `--resource-arns`.
- **Anvil bug — `us-east-1` resources lack `project`/`stage` tags.** The WAF
  WebACL and edge-signer Lambda carry only `ManagedBy=anvil`. Internal us-east-1
  providers are created without `DefaultTags`
  (`provider/internal/awssite/util.go` `CreateUSEast1Provider`,
  `provider/aws/waf/waf.go`), so `App`'s default tags (incl. user `defaults.tags`)
  don't reach them. They fall out of every app-scoped scan. Fix independent of
  this feature.
- **First real findings against Anvil defaults** (see the spike output): the
  SvelteKitSite assets bucket fails `s3_bucket_secure_transport_policy`,
  `s3_bucket_object_versioning`, `s3_bucket_server_access_logging_enabled`,
  `s3_bucket_kms_encryption`; CloudFront fails `logging_enabled`; the site Lambda
  fails `using_cross_account_layers` (High), `no_dead_letter_queue`,
  `env_vars_not_encrypted_with_cmk`. Each needs a default-vs-intentional
  decision — exactly what the dashboard's default/override split is for.

### `--resource-arns` follow-up (after the us-east-1 tagging fix)

- The tagging API now returns the WAF WebACL, its log group and the edge Lambda
  (6 resources, up from 3); IAM roles still come from `iam:ListRoles` + tags.
- **`--resource-arns` scans IAM roles.** Both stack roles got findings
  (`iam_role_cross_service_confused_deputy_prevention`, High) — but only without
  `--compliance`: none of SOC 2 / ISO 27001 / CIS 7.0 map role-level checks.
- **WAF WebACL produced no findings** even with all checks (Prowler has
  `wafv2_webacl_logging_enabled`, `wafv2_webacl_with_rules`). Likely Prowler
  doesn't match CLOUDFRONT-scoped (`global/webacl`) ARNs. Open.
- More untagged account-level findings leak under `--resource-arns`
  (`iam_support_role_created`, `iam_securityaudit_role_created`, a CloudWatch
  metric-filter check). The "drop untagged findings" rule covers them.
- Runtime ~45s (IAM adds overhead).
- **Decision:** the buildspec resolves ARNs (tagging API per region + IAM roles
  by tag) and passes `--resource-arns`. Shell-quoting note: pass ARNs as
  separate arguments.

### Still to verify

1. CodeBuild behaviour at `concurrentBuildLimit` (queued vs rejected).
2. IAM condition to deny buildspec override on `StartBuild`.
3. Why CLOUDFRONT-scoped WAF WebACLs get no findings.

## Settled edge cases

- **Zero resources matched** (new stack, tagging-API index lag, tagging mistake):
  the manifest records `resources: 0`. No special handling in v1.
- **Untagged findings** (e.g. `s3_account_level_public_access_blocks`) are
  dropped from app reports — an app report contains only findings labelled with
  that app's `project` + `stage`. Account-level findings belong to org mode.

## Future work

- **Accepted exceptions (designed, deferred from v1).** Accepted findings are
  shown as MUTED with a reason, never hidden. Two sources, merged into one
  Prowler mutelist at scan time (Prowler mutelists match on resource tags):

  - _Anvil's own design choices_ — a mutelist shipped and versioned with Anvil,
    keyed by `Component=<type>` tags, plus small input tags (e.g.
    `anvil:protection=none`) for input-dependent cases.
  - _User exceptions_ — `compliance: { accept: [{ check, reason, until? }] }` on a
    **component** (applies to that component's resources) or on `App` (whole app;
    no `resource` field, so no string references). Components record accepts in
    the build manifest during discovery; the CLI passes them back as stack config
    (as `setFunctionArtifacts` does for Lambda zips); `ComplianceScanner` writes
    `config/<project>/<stage>/accept.yaml`. `check` is a plain string validated
    against the pinned Prowler check list ("did you mean …").
  - Needs every component to tag resources with `Component=<type>` and a logical
    name tag (e.g. `ComponentName=web`).

- **Org mode** as a separate app type (not a flag on `App`): scanner in a
  **delegated-admin security/audit account** (not the management account), scan
  role deployed to every member via a service-managed StackSet, Prowler assuming
  into each account. Includes account-level checks excluded from app scans.
- **Object Lock** (`immutable: 'governance' | 'compliance'`), applied per object
  at upload so only opted-in apps are locked.
- **Coverage report:** Prowler inventory vs. Pulumi state, to surface untagged /
  untaggable resources.
- **Failure notifications:** EventBridge on build `FAILED` → SNS
  (`compliance: { notify }`).
- **Export** (`anvil compliance export --format csv|html`) for auditors.
- **Private ECR mirror** of the Prowler image.
- **GCP** support.

## Sources

- [Prowler — tag-based scan](https://docs.prowler.com/user-guide/providers/aws/tag-based-scan)
- [Prowler — reporting / output formats](https://docs.prowler.com/user-guide/cli/tutorials/reporting)
- [Prowler — outputs developer guide](https://docs.prowler.com/developer-guide/outputs)
- [DefectDojo — Prowler v3+ parser (v3 `ResourceTags`)](https://documentation.defectdojo.com/integrations/parsers/file/aws_prowler_v3plus)
  > > > > > > > @{-1}
