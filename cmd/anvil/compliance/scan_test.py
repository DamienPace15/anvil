"""Unit tests for scan.py. Run: python -m unittest scan_test (from this directory)."""

import unittest

import scan

VALID = {
    "ANVIL_PROJECT": "testwaf",
    "ANVIL_STAGE": "damienpace",
    "ANVIL_REGIONS": "ap-southeast-2",
    "ANVIL_FRAMEWORKS": "soc2_aws cis_7.0_aws",
    "ANVIL_RETENTION": "1y",
    "ANVIL_TRIGGER": "manual",
    "ANVIL_RESULTS_DIR": "/out",
}


def env(**overrides):
    e = dict(VALID)
    for k, v in overrides.items():
        if v is None:
            e.pop(k, None)
        else:
            e[k] = v
    return e


class ParseInputs(unittest.TestCase):
    def test_valid(self):
        cfg = scan.parse_inputs(env())
        self.assertEqual(cfg["project"], "testwaf")
        self.assertEqual(cfg["frameworks"], ["soc2_aws", "cis_7.0_aws"])

    def test_always_adds_global_region_once(self):
        self.assertEqual(scan.parse_inputs(env())["regions"], ["ap-southeast-2", "us-east-1"])
        cfg = scan.parse_inputs(env(ANVIL_REGIONS="us-east-1,ap-southeast-2"))
        self.assertEqual(cfg["regions"], ["us-east-1", "ap-southeast-2"])

    def test_rejects_shell_metacharacters(self):
        for key, value in [
            ("ANVIL_PROJECT", "x; rm -rf /"),
            ("ANVIL_STAGE", "$(whoami)"),
            ("ANVIL_REGIONS", "ap-southeast-2 `id`"),
            ("ANVIL_FRAMEWORKS", "soc2_aws --push-to-cloud"),
        ]:
            with self.subTest(key=key), self.assertRaises(scan.InputError):
                scan.parse_inputs(env(**{key: value}))

    def test_rejects_unknown_retention_and_trigger(self):
        with self.assertRaises(scan.InputError):
            scan.parse_inputs(env(ANVIL_RETENTION="9m"))
        with self.assertRaises(scan.InputError):
            scan.parse_inputs(env(ANVIL_TRIGGER="cron"))

    def test_requires_exactly_one_destination(self):
        with self.assertRaises(scan.InputError):
            scan.parse_inputs(env(ANVIL_RESULTS_DIR=None))
        with self.assertRaises(scan.InputError):
            scan.parse_inputs(env(ANVIL_RESULTS_BUCKET="anvil-compliance-123-ap-southeast-2"))

    def test_requires_frameworks(self):
        with self.assertRaises(scan.InputError):
            scan.parse_inputs(env(ANVIL_FRAMEWORKS=""))


class Scope(unittest.TestCase):
    cfg = {"project": "testwaf", "stage": "damienpace"}

    def finding(self, labels):
        return {"resources": [{"labels": labels}]}

    def test_labels_split_on_first_colon(self):
        self.assertEqual(scan.labels_to_tags(["url:https://x", "stage:a"]), {"url": "https://x", "stage": "a"})

    def test_in_scope(self):
        f = self.finding(["ManagedBy:anvil", "project:testwaf", "stage:damienpace", "Component:SvelteKitSite"])
        self.assertTrue(scan.in_scope(f, self.cfg))

    def test_untagged_account_level_finding_dropped(self):
        self.assertFalse(scan.in_scope(self.finding([]), self.cfg))

    def test_other_stage_dropped(self):
        f = self.finding(["ManagedBy:anvil", "project:testwaf", "stage:prod"])
        self.assertFalse(scan.in_scope(f, self.cfg))


class Command(unittest.TestCase):
    def test_arguments_are_separate_and_exit_code_3_ignored(self):
        cfg = {"regions": ["ap-southeast-2", "us-east-1"], "frameworks": ["soc2_aws"]}
        cmd = scan.prowler_command(cfg, ["arn:a", "arn:b"], "/tmp/out")
        i = cmd.index("--resource-arns")
        self.assertEqual(cmd[i + 1 : i + 3], ["arn:a", "arn:b"])
        self.assertIn("--ignore-exit-code-3", cmd)
        self.assertNotIn("--push-to-cloud", cmd)


class Summary(unittest.TestCase):
    def test_counts(self):
        findings = [
            {"status_code": "FAIL", "severity": "High", "status": "New"},
            {"status_code": "FAIL", "severity": "Low", "status": "New"},
            {"status_code": "PASS", "severity": "Low", "status": "New"},
        ]
        s = scan.summarise(findings)
        self.assertEqual((s["total"], s["pass"], s["fail"]), (3, 1, 2))
        self.assertEqual(s["failBySeverity"], {"high": 1, "low": 1})


if __name__ == "__main__":
    unittest.main()
