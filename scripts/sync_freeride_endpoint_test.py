#!/usr/bin/env python3
"""Regression coverage for Freeride/Ollama port mismatches at workflow startup."""

import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import sync_freeride_endpoint as sync


class EndpointTests(unittest.TestCase):
    def test_running_proxy_wins_over_stale_ollama_endpoint(self):
        commands = "/usr/local/bin/ollama serve\n./freeride -debug -port 9999\n"
        with patch.object(sync.subprocess, "check_output", return_value=commands):
            endpoint = sync.resolve_endpoint({"llm_endpoint": sync.DEFAULT_ENDPOINT}, {})
        self.assertEqual(endpoint, "http://localhost:9999/v1/chat/completions")

    def test_explicit_endpoint_and_port_override_process_discovery(self):
        with patch.object(sync.subprocess, "check_output", side_effect=AssertionError):
            self.assertEqual(sync.resolve_endpoint({}, {"LLM_ENDPOINT": "https://proxy/v1/chat/completions", "FREERIDE_PORT": "9999"}), "https://proxy/v1/chat/completions")
            self.assertEqual(sync.resolve_endpoint({}, {"FREERIDE_PORT": "9000"}), "http://localhost:9000/v1/chat/completions")

    def test_port_flag_forms_and_ambiguous_processes(self):
        for flag in ("-port 9999", "--port 9999", "-port=9999", "--port=9999"):
            self.assertEqual(sync.process_endpoints(f"/opt/freeride {flag}"), {sync.port_endpoint(9999)})
        with patch.object(sync.subprocess, "check_output", return_value="./freeride -port 9999\n./freeride -port 9000"):
            with self.assertRaisesRegex(ValueError, "Multiple Freeride"):
                sync.resolve_endpoint({}, {})

    def test_rejects_wrong_server_before_updating_config(self):
        settings = {"default_agent": "local", "agents": {"local": {"command": "gt-agent", "env": {"LLM_MODEL": "openrouter/free"}}}}
        with patch.object(sync, "urlopen") as request:
            request.return_value.__enter__.return_value.read.return_value = json.dumps({"data": [{"id": "gemma4:latest"}]})
            with self.assertRaisesRegex(ValueError, "does not serve configured models: openrouter/free"):
                sync.verify_endpoint(sync.DEFAULT_ENDPOINT, settings)

    def test_sync_updates_all_gt_agents_preserving_other_configuration(self):
        settings = {
            "llm_endpoint": sync.DEFAULT_ENDPOINT,
            "session_transport": "nats",
            "agents": {
                "local": {"command": "gt-agent", "env": {"LLM_ENDPOINT": sync.DEFAULT_ENDPOINT, "LLM_MODEL": "openrouter/free", "LLM_TIMEOUT": "1200s"}},
                "external": {"command": "claude", "env": {"LLM_ENDPOINT": "https://other"}},
            },
        }
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "config.json"
            path.write_text(json.dumps(settings))
            endpoint = sync.port_endpoint(9999)
            self.assertTrue(sync.sync_settings(path, settings, endpoint))
            updated = json.loads(path.read_text())
            self.assertEqual(updated["llm_endpoint"], endpoint)
            self.assertEqual(updated["agents"]["local"]["env"], {"LLM_ENDPOINT": endpoint, "LLM_MODEL": "openrouter/free", "LLM_TIMEOUT": "1200s"})
            self.assertEqual(updated["agents"]["external"], settings["agents"]["external"])
            self.assertEqual(updated["session_transport"], "nats")
            self.assertFalse(sync.sync_settings(path, updated, endpoint))


if __name__ == "__main__":
    unittest.main()
