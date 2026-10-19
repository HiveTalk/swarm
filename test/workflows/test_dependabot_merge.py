import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import textwrap
import unittest


ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = ROOT / ".github/workflows/dependabot-auto-merge.yml"


def merge_script():
    step = WORKFLOW.read_text().split("      - name: Merge dependency update\n", 1)[1]
    script = step.split("        run: |\n", 1)[1].split("\n      - name:", 1)[0]
    return textwrap.dedent(script)


MOCK_GH = """
import json
import os
from pathlib import Path
import sys

path = Path(os.environ["MOCK_STATE"])
state = json.loads(path.read_text())
args = sys.argv[1:]
scenario = state["scenario"]
result = ""
code = 0

if args[:2] == ["pr", "merge"]:
    state["merge_attempts"] += 1
    state["merge_args"].append(args)
    if not state["updated"]:
        result, code = "Head branch is behind main", 1
    elif not state["ready"] or not state["required_checked"]:
        state["premature_merge"] = True
        result, code = "4 required status checks are expected", 1
    else:
        state["merged"] = True
elif args[:2] == ["pr", "view"]:
    state["views"] += 1
    ready = state["views"] >= 3
    head = "old-head" if scenario == "stale-head" and not ready else "new-head"
    checks = []
    if ready or scenario in ("missing-required", "failed", "pending", "stale-head"):
        checks = [{"name": "Go", "status": "COMPLETED", "conclusion": "SUCCESS"}]
    if scenario == "failed":
        checks[0]["conclusion"] = "FAILURE"
    if scenario == "pending" and not ready:
        checks.append({"name": "Security", "status": "IN_PROGRESS", "conclusion": None})
    if scenario == "status-context":
        checks.append({"context": "External security", "state": "SUCCESS" if ready else "PENDING"})
    if scenario == "status-context-failed":
        checks.append({"context": "External security", "state": "ERROR"})
    state["ready"] = ready and head == "new-head" and scenario not in ("failed", "status-context-failed")
    result = json.dumps({"headRefOid": head, "statusCheckRollup": checks})
elif args[:2] == ["pr", "checks"]:
    state["required_reads"] += 1
    if state["ready"] or scenario in ("stale-head", "pending", "status-context"):
        state["required_checked"] = True
        result = "Go\\tpass\\t1s\\thttps://example.test/check"
    elif scenario == "missing-required":
        result = "no checks reported"
    else:
        result, code = "Go\\tpending\\t0\\thttps://example.test/check", 8
elif args[0] == "api":
    endpoint = args[1]
    if endpoint.endswith("/update-branch"):
        state["updates"] += 1
        if state["updates"] > 1:
            result, code = "There are no new commits on the base branch", 1
        else:
            state["updated"] = True
            result = json.dumps({"message": "Updating pull request branch."})
    elif "/actions/" in endpoint:
        runs = [{"conclusion": "action_required", "html_url": "https://example.test/run"}] if scenario == "approval" else []
        result = json.dumps({"workflow_runs": runs})
    else:
        state["head_reads"] += 1
        head = "new-head" if state["updated"] and (scenario != "async-head" or state["head_reads"] >= 4) else "old-head"
        result = head if "--jq" in args else json.dumps({"head": {"sha": head}})
else:
    raise AssertionError(args)

path.write_text(json.dumps(state))
print(result)
sys.exit(code)
"""


class DependabotMergeTests(unittest.TestCase):
    def run_scenario(self, scenario):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            state_path = root / "state.json"
            state_path.write_text(json.dumps({
                "scenario": scenario,
                "updated": scenario == "current",
                "ready": scenario == "current",
                "required_checked": scenario == "current",
                "merged": False,
                "premature_merge": False,
                "updates": 0,
                "views": 0,
                "head_reads": 0,
                "required_reads": 0,
                "merge_attempts": 0,
                "merge_args": [],
            }))
            mock = root / "mock_gh.py"
            mock.write_text(textwrap.dedent(MOCK_GH))
            for name, content in {
                "gh": 'exec "$MOCK_PYTHON" "$MOCK_GH" "$@"\n',
                "sleep": "exit 0\n",
            }.items():
                shim = root / name
                shim.write_text(content)
                shim.chmod(0o755)
            environment = {
                **os.environ,
                "PATH": str(root) + os.pathsep + os.environ["PATH"],
                "MOCK_STATE": str(state_path),
                "MOCK_GH": str(mock),
                "MOCK_PYTHON": sys.executable,
                "GITHUB_REPOSITORY": "HiveTalk/swarm",
                "PR_NUMBER": "49",
                "PR_URL": "https://example.test/pr/49",
            }
            result = subprocess.run(
                ["bash", "-eo", "pipefail", "-c", merge_script()],
                env=environment,
                capture_output=True,
                text=True,
                timeout=5,
            )
            state = json.loads(state_path.read_text())
            return result, state

    def assert_safe_merge(self, scenario):
        result, state = self.run_scenario(scenario)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertTrue(state["merged"], state)
        self.assertFalse(state["premature_merge"], state)
        self.assertEqual(state["updates"], 1, state)
        self.assertGreaterEqual(state["views"], 3, state)
        final_args = state["merge_args"][-1]
        self.assertIn("--match-head-commit", final_args)
        self.assertEqual(final_args[final_args.index("--match-head-commit") + 1], "new-head")

    def test_waits_for_checks_to_appear(self):
        self.assert_safe_merge("delayed")

    def test_waits_for_asynchronous_branch_update(self):
        self.assert_safe_merge("async-head")

    def test_does_not_accept_checks_from_previous_head(self):
        self.assert_safe_merge("stale-head")

    def test_requires_required_checks_even_when_other_checks_exist(self):
        self.assert_safe_merge("missing-required")

    def test_waits_for_non_required_security_checks(self):
        self.assert_safe_merge("pending")

    def test_handles_legacy_status_contexts(self):
        self.assert_safe_merge("status-context")

    def test_current_verified_branch_needs_no_update(self):
        result, state = self.run_scenario("current")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertTrue(state["merged"], state)
        self.assertEqual(state["updates"], 0, state)
        self.assertIn("--match-head-commit", state["merge_args"][0])

    def test_failing_check_blocks_merge(self):
        result, state = self.run_scenario("failed")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(state["merged"], state)
        self.assertIn("Failing check: Go", result.stdout + result.stderr)

    def test_run_requiring_approval_stops_with_actionable_error(self):
        result, state = self.run_scenario("approval")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(state["merged"], state)
        self.assertIn("approval", (result.stdout + result.stderr).lower())
        self.assertEqual(state["updates"], 1, state)

    def test_failing_legacy_status_blocks_merge(self):
        result, state = self.run_scenario("status-context-failed")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(state["merged"], state)
        self.assertIn("Failing check: External security", result.stdout + result.stderr)


class VulnerabilityToolchainTests(unittest.TestCase):
    def assert_scanner_uses_repository_toolchain(self, workflow):
        text = (ROOT / ".github/workflows" / workflow).read_text()
        scanner = text.split("uses: golang/govulncheck-action@", 1)[1].split("\n      - ", 1)[0]
        self.assertIn("go-version-input: ''", scanner)
        self.assertIn("go-version-file: .go-version", scanner)
        self.assertIn("repo-checkout: false", scanner)

    def test_ci_scans_the_build_toolchain(self):
        self.assert_scanner_uses_repository_toolchain("ci.yml")

    def test_scheduled_review_scans_the_build_toolchain(self):
        self.assert_scanner_uses_repository_toolchain("security.yml")


if __name__ == "__main__":
    unittest.main()
