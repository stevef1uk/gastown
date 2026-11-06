#!/usr/bin/env python3
"""Exercise workflow cleanup/selection with a mock CLI, never a live town."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SOURCE = Path(__file__).with_name("test-playwright-new-rig.sh").read_text()


class WorkflowControlTest(unittest.TestCase):
    def run_block(self, start, end, workflows, prefix=""):
        block = SOURCE.split(start, 1)[1].split(end, 1)[0]
        if start.startswith("#"):
            block = block.split("\n", 1)[1]
        if end == "\ndone\n":
            block += "\ndone\n"
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            fixture = root / "fixture.json"
            calls = root / "calls.jsonl"
            fixture.write_text(json.dumps({"workflows": workflows}))
            cli = root / "gt"
            cli.write_text('''#!/usr/bin/env python3
import json,os,sys
args=sys.argv[1:]
workflows=json.load(open(os.environ["WORKFLOW_TEST_FIXTURE"]))["workflows"]
if args[:3]==["mayor","workflow","list"]:
    print(json.dumps({"workflows":workflows}))
elif args[:3]==["mayor","workflow","status"]:
    print(json.dumps({"workflows":[w for w in workflows if w["id"]==args[3]]}))
elif args[:3] in (["mayor","workflow","pause"],["mayor","workflow","delete"]):
    with open(os.environ["WORKFLOW_TEST_CALLS"],"a") as out:
        out.write(json.dumps(args)+"\\n")
else:
    raise SystemExit("Unexpected CLI call: "+str(args))
''')
            cli.chmod(0o700)
            env = dict(os.environ, PATH=str(root) + os.pathsep + os.environ["PATH"],
                       WORKFLOW_TEST_FIXTURE=str(fixture), WORKFLOW_TEST_CALLS=str(calls))
            result = subprocess.run(["bash", "-c", "set -euo pipefail\n" + prefix + block],
                                    env=env, text=True, capture_output=True, timeout=10)
            actions = [json.loads(line) for line in calls.read_text().splitlines()] if calls.exists() else []
            return result, actions

    @staticmethod
    def workflow(identifier, status, rig="pwtest", state="implementation", reason=""):
        return dict(id=identifier, status=status, variables=dict(rig=rig),
                    current_state=state, pause_reason=reason)

    def test_cleanup_only_test_rig_and_handles_finished_runs(self):
        result, actions = self.run_block("# Select by structured rig identity", "# Stop/remove the old rig", [
            self.workflow("wf-1", "paused"), self.workflow("wf-2", "completed"),
            self.workflow("wf-8", "running", rig="other")])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(actions, [
            ["mayor", "workflow", "pause", "wf-1", "--shutdown=false"],
            ["mayor", "workflow", "delete", "wf-1"],
            ["mayor", "workflow", "delete", "wf-2"]])

    def test_selects_new_running_instance_not_paused_history(self):
        result, _ = self.run_block("# Follow exactly the newly running instance", 'echo "=== 4. Wait for workflow completion ==="', [
            self.workflow("wf-1", "paused"), self.workflow("wf-2", "running")])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Following workflow wf-2", result.stdout)

    def test_selects_single_new_run_even_if_it_paused_immediately(self):
        result, _ = self.run_block("# Follow exactly the newly running instance", 'echo "=== 4. Wait for workflow completion ==="', [
            self.workflow("wf-2", "paused")])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Following workflow wf-2", result.stdout)

    def test_reports_selected_run_pause_reason_and_exits(self):
        result, _ = self.run_block('echo "=== 4. Wait for workflow completion ==="', '\ndone\n', [
            self.workflow("wf-1", "paused", reason="Old error"),
            self.workflow("wf-2", "paused", reason="New budget error")], prefix="WORKFLOW_ID=wf-2\n")
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("Workflow wf-2 status: paused", result.stdout)
        self.assertIn("Paused because: New budget error", result.stdout)
        self.assertNotIn("Old error", result.stdout)


if __name__ == "__main__":
    unittest.main()
