#!/usr/bin/env python3
"""Synchronize town agent endpoints before launching a Freeride workflow.

Resolution order: LLM_ENDPOINT, FREERIDE_PORT, running Freeride -port flag,
saved town endpoint, default. Print the resolved endpoint on stdout.
"""

import argparse
import json
import os
from pathlib import Path
import shlex
import subprocess
import sys
from urllib.request import urlopen


DEFAULT_ENDPOINT = "http://localhost:11434/v1/chat/completions"


def port_endpoint(port):
    number = int(port)
    if not 1 <= number <= 65535:
        raise ValueError("Freeride port must be between 1 and 65535")
    return f"http://localhost:{number}/v1/chat/completions"


def process_endpoints(commands):
    endpoints = set()
    for command in commands.splitlines():
        try:
            args = shlex.split(command)
        except ValueError:
            continue
        if not args or Path(args[0]).name != "freeride":
            continue
        for index, arg in enumerate(args[1:], 1):
            if arg in ("-port", "--port") and index + 1 < len(args):
                endpoints.add(port_endpoint(args[index + 1]))
            elif arg.startswith(("-port=", "--port=")):
                endpoints.add(port_endpoint(arg.split("=", 1)[1]))
    return endpoints


def resolve_endpoint(settings, env):
    if env.get("LLM_ENDPOINT", "").strip():
        return env["LLM_ENDPOINT"].strip()
    if env.get("FREERIDE_PORT", "").strip():
        return port_endpoint(env["FREERIDE_PORT"])
    commands = subprocess.check_output(["ps", "-ax", "-o", "command="], text=True)
    endpoints = process_endpoints(commands)
    if len(endpoints) > 1:
        raise ValueError("Multiple Freeride ports found; set LLM_ENDPOINT or FREERIDE_PORT")
    return next(iter(endpoints), settings.get("llm_endpoint") or DEFAULT_ENDPOINT)


def verify_endpoint(endpoint, settings):
    suffix = "/chat/completions"
    if not endpoint.startswith(("http://", "https://")) or not endpoint.endswith(suffix):
        raise ValueError("LLM_ENDPOINT must be an HTTP(S) chat/completions URL")
    models_url = endpoint[:-len(suffix)] + "/models"
    with urlopen(models_url, timeout=10) as response:
        models = {model["id"] for model in json.load(response)["data"]}
    agents = settings.get("agents", {})
    selected = set(settings.get("role_agents", {}).values())
    selected.add(settings.get("default_agent"))
    required = {
        agent.get("env", {}).get("LLM_MODEL")
        for name, agent in agents.items()
        if name in selected and agent.get("command") == "gt-agent"
    } - {None, ""}
    missing = required - models
    if missing:
        raise ValueError(f"{models_url} does not serve configured models: {', '.join(sorted(missing))}")


def sync_settings(path, settings, endpoint):
    updated = json.loads(json.dumps(settings))
    updated["llm_endpoint"] = endpoint
    for agent in updated.get("agents", {}).values():
        if agent.get("command") == "gt-agent":
            agent.setdefault("env", {})["LLM_ENDPOINT"] = endpoint
    if updated == settings:
        return False
    # Replace atomically so a concurrent agent launch cannot read partial JSON.
    temporary = path.with_name(path.name + ".tmp")
    try:
        temporary.write_text(json.dumps(updated, indent=2) + "\n")
        temporary.chmod(path.stat().st_mode & 0o777)
        temporary.replace(path)
    finally:
        temporary.unlink(missing_ok=True)
    return True


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--town-root", type=Path, default=Path.home() / "gt")
    args = parser.parse_args()
    path = args.town_root / "settings" / "config.json"
    try:
        settings = json.loads(path.read_text())
        endpoint = resolve_endpoint(settings, os.environ)
        verify_endpoint(endpoint, settings)
        changed = sync_settings(path, settings, endpoint)
    except (OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        parser.exit(1, f"Freeride endpoint preflight failed: {error}\n")
    print(f"Freeride endpoint {'updated' if changed else 'verified'}: {endpoint}", file=sys.stderr)
    print(endpoint)


if __name__ == "__main__":
    main()
