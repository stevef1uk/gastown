#!/usr/bin/env python3
"""
Rig-flow implementation status for any Gas Town rig (gastown/scripts).

Reads $GT_ROOT orchestrator state, rig workflow-profile.json, bd beads, and on-disk files.

Usage (from gastown repo):
  ./scripts/rig_implementation_status.py RIG
  ./scripts/rig_implementation_status.py --list-rigs
  GT_ROOT=~/gt ./scripts/rig_implementation_status.py myrig
"""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path

# rig-flow.yaml state name → rig subdirectory with gt-agent (when present).
STATE_AGENT_DIRS: dict[str, str] = {
    "kickoff": "mayor",
    "design": "architect",
    "planning": "planner",
    "plan_review": "qa",
    "project_setup": "setup",
    "implementation": "polecat",
    "qa_review": "qa",
    "completed": "mayor",
}


def town_root_from_env(explicit: str | None) -> Path:
    root = explicit or os.environ.get("GT_ROOT") or os.environ.get("GASTOWN_TOWN_ROOT") or os.path.expanduser("~/gt")
    root = Path(root).expanduser().resolve()
    # Support config.json in root or in settings/ subdirectory
    config_paths = [root / "config.json", root / "settings" / "config.json"]
    if not any(p.is_file() for p in config_paths):
        sys.exit(f"FATAL: not a Gas Town root (no config.json in {root} or {root}/settings)")
    return root


def load_json(path: Path) -> dict | list | None:
    if not path.is_file():
        return None
    try:
        return json.loads(path.read_text(errors="replace"))
    except json.JSONDecodeError:
        return None


def list_rigs(town: Path) -> list[tuple[str, dict]]:
    snap = load_json(town / "orchestrator" / "instances.json")
    if not isinstance(snap, dict):
        return []
    out: list[tuple[str, dict]] = []
    for inst in snap.get("instances") or []:
        if not isinstance(inst, dict):
            continue
        vars_ = inst.get("variables") or {}
        rig = (vars_.get("rig") or vars_.get("RIG") or "").strip()
        if rig:
            out.append((rig, inst))
    return sorted(out, key=lambda x: x[0])


def find_workflow(town: Path, rig: str) -> dict | None:
    for name, inst in list_rigs(town):
        if name == rig:
            return inst
    return None


def profile_validation(mayor_rig: Path) -> dict:
    prof = load_json(mayor_rig / ".gastown" / "workflow-profile.json")
    if not isinstance(prof, dict):
        return {}
    v = prof.get("validation")
    return v if isinstance(v, dict) else {}


def active_required_files(val: dict) -> list[str]:
    """Scope to active delivery phase when delivery_phases is set."""
    phases = val.get("delivery_phases") or []
    if not isinstance(phases, list) or not phases:
        raw = val.get("required_files") or []
        return [str(x).replace("\\", "/").strip() for x in raw if str(x).strip()] if isinstance(raw, list) else []
    active = (val.get("active_phase_id") or "").strip()
    if not active and phases:
        first = phases[0]
        if isinstance(first, dict):
            active = (first.get("id") or "").strip()
    for ph in phases:
        if not isinstance(ph, dict):
            continue
        if (ph.get("id") or "").strip() == active:
            raw = ph.get("required_files") or []
            return [str(x).replace("\\", "/").strip() for x in raw if str(x).strip()] if isinstance(raw, list) else []
    raw = val.get("required_files") or []
    return [str(x).replace("\\", "/").strip() for x in raw if str(x).strip()] if isinstance(raw, list) else []


def workflow_uses_go(val: dict) -> bool:
    qa = (val.get("qa_verify_command") or "").lower()
    if any(x in qa for x in ("go test", "go vet", "go mod", "go build", "go run")):
        return True
    runner = (val.get("test_runner") or "").lower()
    return runner in ("go", "golang")


def workflow_uses_python(val: dict) -> bool:
    qa = (val.get("qa_verify_command") or "").lower()
    if "pytest" in qa or "python" in qa or "pip install" in qa:
        return True
    runner = (val.get("test_runner") or "").lower()
    return runner in ("pytest", "python", "python3")


def implementation_path_score(path: str) -> int:
    """Mirror gastown OrderRequiredFilesForImplementation (generic, not rig-specific)."""
    p = path.lower().replace("\\", "/")
    if p.endswith("go.mod"):
        return 0
    if p.endswith("requirements.txt") or p.endswith("pyproject.toml"):
        return 1
    if "/internal/store/" in p:
        return 10
    if "/internal/api/" in p:
        return 20
    if "/web/" in p:
        return 30
    if p.endswith("_test.go") or p.endswith("_test.py"):
        return 85
    if "/cmd/" in p:
        return 90
    if p.endswith("main.py") and "/backend/" in p:
        return 90
    return 50


def order_required_files(files: list[str]) -> list[str]:
    items = [(f.replace("\\", "/").strip(), implementation_path_score(f)) for f in files if f.strip()]
    items.sort(key=lambda x: (x[1], x[0]))
    return [p for p, _ in items]


def extract_path_from_title(title: str, prefix: str) -> str:
    t = title.strip()
    pfx = prefix.strip()
    if pfx and pfx.lower() in t.lower():
        idx = t.lower().index(pfx.lower())
        t = t[idx + len(pfx) :].strip()
    for sep in (" per architecture", " per arch"):
        if sep in t:
            t = t.split(sep, 1)[0].strip()
    return t.replace("\\", "/")


def path_matches_required(path: str, required: list[str]) -> str | None:
    """Return the matching required_files entry for path (exact or basename)."""
    path = path.replace("\\", "/").strip()
    if not path:
        return None
    base = os.path.basename(path)
    for want in required:
        want = want.replace("\\", "/").strip()
        if want == path or os.path.basename(want) == base:
            return want
    return None


def normalize_bead_path(path: str, layout_root: str, required: list[str]) -> str:
    """Mirror orchestrator.NormalizeBeadPathForLayout + required_files matching."""
    path = path.replace("\\", "/").strip()
    if not path:
        return path
    hit = path_matches_required(path, required)
    if hit:
        return hit
    layout = layout_root.strip().strip("/")
    if not layout:
        return path
    if path.startswith(layout + "/") or path == layout:
        return path
    if ".." in path:
        return path
    candidate = f"{layout}/{path}"
    if path_matches_required(candidate, required):
        return candidate
    if path in ("go.mod", "go.sum"):
        return candidate
    for prefix in ("internal/", "cmd/", "pkg/", "api/", "web/", "backend/"):
        if path.startswith(prefix):
            return candidate
    return path


def go_build_rel_package(layout_root: str, bead_path: str) -> str:
    layout = layout_root.strip().strip("/")
    p = bead_path.replace("\\", "/").strip()
    if layout and p.startswith(layout + "/"):
        p = p[len(layout) + 1 :]
    if not p.endswith(".go"):
        return ""
    return os.path.dirname(p).replace("\\", "/")


def normalize_pytest_command(cmd: str) -> str:
    """Mirror orchestrator.NormalizePytestCommand (bare pytest → python3 -m pytest)."""
    lower = cmd.lower()
    if "pytest" not in lower:
        return cmd
    if "import pytest" in lower:
        return cmd
    if "-c " in lower and "import " in lower:
        return cmd
    if "python3 -m pytest" in lower or "python -m pytest" in lower:
        return cmd
    return re.sub(r"(?i)(^|[;&|]\s*|\s+)pytest\b", r"\1python3 -m pytest", cmd)


def python_venv_rel(val: dict) -> str:
    d = (val.get("python_venv_dir") or "").strip()
    if d.lower() == "off":
        return ""
    return d if d else ".venv"


def python_qa_verify_command(val: dict, mayor_rig: Path) -> str:
    """QA verify for status script: venv python when present (mayor/rig), else profile cmd."""
    cmd = (val.get("qa_verify_command") or "").strip()
    if not cmd:
        cmd = (val.get("unittest_command_hint") or "").strip()
    if not cmd:
        return ""
    cmd = normalize_pytest_command(cmd)
    venv_rel = python_venv_rel(val)
    venv_py = mayor_rig / venv_rel / "bin" / "python3"
    layout = (val.get("layout_root") or "").strip().strip("/")
    test_scope = f"{layout}/tests" if layout else ""
    if venv_py.is_file():
        if re.search(r"(?i)\bpython3?\b", cmd):
            cmd = re.sub(r"(?i)\bpython3?\b", str(venv_py), cmd, count=1)
        else:
            extra = re.sub(r"(?i)^pytest\b", "", cmd).strip()
            cmd = f"{venv_py} -m pytest {extra}".strip()
        if test_scope and "pytest" in cmd.lower() and test_scope not in cmd:
            cmd = f"{cmd} {test_scope}"
        return cmd
    if layout and f"cd {layout}" not in cmd.lower() and (mayor_rig / layout).is_dir():
        if test_scope and "pytest" in cmd.lower() and test_scope not in cmd:
            return f"{cmd} {test_scope}"
        return f"cd {layout} && {cmd}"
    return cmd


def go_compile_verify_for_bead(val: dict, bead_path: str) -> str:
    layout = (val.get("layout_root") or "").strip() or "."
    p = bead_path.replace("\\", "/")
    if p.endswith("go.mod"):
        return f"cd {layout} && go mod tidy"
    pkg = go_build_rel_package(val.get("layout_root") or "", bead_path)
    if pkg and p.endswith("cmd/server/main.go"):
        qa = (val.get("qa_verify_command") or "").strip()
        if qa:
            return qa
    if pkg:
        return f"cd {layout} && go mod tidy && go build ./{pkg}/..."
    return f"cd {layout} && go mod tidy && go build ./..."


def find_go_module_dir(mayor_rig: Path, layout_root: str, required: list[str]) -> Path | None:
    layout = layout_root.strip().strip("/")
    if layout:
        d = mayor_rig / layout
        if (d / "go.mod").is_file():
            return d
    for rel in required:
        rel = rel.replace("\\", "/")
        if rel.endswith("go.mod"):
            d = mayor_rig / os.path.dirname(rel)
            if (d / "go.mod").is_file():
                return d
    for go_mod in sorted(mayor_rig.rglob("go.mod")):
        if any(part.startswith(".") for part in go_mod.parts):
            continue
        try:
            rel = go_mod.relative_to(mayor_rig)
        except ValueError:
            continue
        if len(rel.parts) <= 5:
            return go_mod.parent
    return None


def go_packages_from_required(required: list[str], layout_root: str) -> list[str]:
    layout = layout_root.strip().strip("/")
    seen: set[str] = set()
    for rel in required:
        rel = rel.replace("\\", "/").strip()
        if layout and rel.startswith(layout + "/"):
            rel = rel[len(layout) + 1 :]
        if rel.endswith(".go"):
            pkg = os.path.dirname(rel).replace("\\", "/")
            if pkg:
                seen.add(pkg)
    return sorted(seen)


def agent_dir_for_state(rig_dir: Path, state: str) -> Path | None:
    sub = STATE_AGENT_DIRS.get(state.strip())
    if sub:
        d = rig_dir / sub
        if d.is_dir():
            return d
    for child in sorted(rig_dir.iterdir()):
        if child.is_dir() and (child / "gt-agent-state.json").is_file():
            return child
    return None


def run(cmd: list[str], *, cwd: Path | None = None, env: dict | None = None) -> tuple[int, str]:
    try:
        p = subprocess.run(
            cmd,
            cwd=str(cwd) if cwd else None,
            env=env,
            capture_output=True,
            text=True,
            timeout=120,
        )
        out = (p.stdout or "") + (p.stderr or "")
        return p.returncode, out.strip()
    except (subprocess.TimeoutExpired, FileNotFoundError) as e:
        return 127, str(e)


def parse_bd_flat_lines(text: str, title_filter: str) -> list[dict]:
    rows: list[dict] = []
    if not title_filter:
        return rows
    title_lower = title_filter.lower()
    line_re = re.compile(r"^([○◐✓✗])\s+(\S+)\s+.*?\s+-\s+(.+)$")
    for line in text.splitlines():
        line = line.strip()
        if not line or title_lower not in line.lower():
            continue
        m = line_re.match(line)
        if not m:
            continue
        sym, bead_id, title = m.group(1), m.group(2), m.group(3).strip()
        status = {"○": "open", "◐": "in_progress", "✓": "closed", "✗": "blocked"}.get(sym, sym)
        rows.append({"id": bead_id, "status": status, "title": title})
    return rows


def list_implement_beads(mayor_rig: Path, beads_dir: Path, title_contains: str) -> tuple[list[dict], bool]:
    beads_env = os.environ.copy()
    beads_env["BEADS_DIR"] = str(beads_dir)
    beads_raw: list[dict] = []
    seen_ids: set[str] = set()
    bd_failed = False
    for status_flag in ("--status=open", "--status=in_progress", "--status=closed"):
        code, bd_out = run(
            ["bd", "list", "--flat", "--limit=0", status_flag],
            cwd=mayor_rig,
            env=beads_env,
        )
        if code != 0:
            bd_failed = True
            continue
        for row in parse_bd_flat_lines(bd_out, title_contains):
            if row["id"] not in seen_ids:
                seen_ids.add(row["id"])
                beads_raw.append(row)
    if not beads_raw:
        code, bd_out = run(
            ["bd", "list", "--flat", "--limit=0"],
            cwd=mayor_rig,
            env=beads_env,
        )
        if code == 0:
            beads_raw = parse_bd_flat_lines(bd_out, title_contains)
        else:
            bd_failed = True
    return beads_raw, bd_failed


@dataclass
class BeadRow:
    step: int
    path: str
    bead_id: str
    status: str
    on_disk: bool
    size: int
    verify_hint: str


def bead_rows(
    mayor_rig: Path,
    required: list[str],
    title_contains: str,
    layout: str,
    val: dict,
    beads_by_path: dict[str, dict],
) -> list[BeadRow]:
    ordered = order_required_files(required)
    out: list[BeadRow] = []
    for i, want in enumerate(ordered, start=1):
        bead = beads_by_path.get(want)
        if bead is None:
            for p, b in beads_by_path.items():
                if os.path.basename(p) == os.path.basename(want):
                    bead = b
                    break
        full = mayor_rig / want
        exists = full.is_file()
        size = full.stat().st_size if exists else 0
        verify = ""
        if workflow_uses_go(val):
            if want.endswith(".go") or want.endswith("go.mod"):
                verify = go_compile_verify_for_bead(val, want)
            else:
                verify = "(non-Go bead — see qa_verify_command)"
        elif (val.get("qa_verify_command") or "").strip():
            verify = (val.get("qa_verify_command") or "").strip()[:80]
        out.append(
            BeadRow(
                step=i,
                path=want,
                bead_id=bead.get("id", "—") if bead else "—",
                status=bead.get("status", "missing") if bead else "no bead",
                on_disk=exists,
                size=size,
                verify_hint=verify,
            )
        )
    return out


def go_checks(mayor_rig: Path, layout_root: str, required: list[str]) -> list[tuple[str, int, str]]:
    mod_dir = find_go_module_dir(mayor_rig, layout_root, required)
    if mod_dir is None:
        return []
    layout = layout_root.strip().strip("/") or mod_dir.name
    results: list[tuple[str, int, str]] = []
    code, out = run(["go", "build", "./..."], cwd=mod_dir)
    summary = "ok" if code == 0 else (out.splitlines()[-1] if out else f"exit {code}")
    results.append((f"cd {layout} && go build ./...", code, summary[:140]))
    for pkg in go_packages_from_required(required, layout_root):
        sub = f"./{pkg}/..."
        if not (mod_dir / pkg).is_dir():
            continue
        code, out = run(["go", "build", sub], cwd=mod_dir)
        summary = "ok" if code == 0 else (out.splitlines()[-1] if out else f"exit {code}")
        results.append((f"go build {sub}", code, summary[:140]))
    return results


def python_checks(val: dict, mayor_rig: Path, layout_root: str) -> list[tuple[str, int, str]]:
    _ = layout_root
    cmd = python_qa_verify_command(val, mayor_rig)
    if not cmd:
        return []
    cwd = mayor_rig
    code, out = run(["bash", "-lc", cmd], cwd=cwd, env=os.environ.copy())
    summary = "ok" if code == 0 else (out.splitlines()[-1] if out else f"exit {code}")
    label = cmd if len(cmd) <= 100 else cmd[:97] + "..."
    return [(label, code, summary[:140])]


def print_table(headers: list[str], rows: list[list[str]]) -> None:
    if not rows:
        print("  (none)")
        return
    widths = [len(h) for h in headers]
    for row in rows:
        for j, cell in enumerate(row):
            widths[j] = max(widths[j], len(cell))
    sep = "+".join("-" * (w + 2) for w in widths)

    def fmt_row(cells: list[str]) -> str:
        return "| " + " | ".join(c.ljust(widths[i]) for i, c in enumerate(cells)) + " |"

    print(sep)
    print(fmt_row(headers))
    print(sep)
    for row in rows:
        print(fmt_row(row))
    print(sep)


def cmd_list_rigs(town: Path) -> int:
    rigs = list_rigs(town)
    if not rigs:
        print(f"No workflow instances in {town / 'orchestrator' / 'instances.json'}")
        return 0
    print_table(
        ["rig", "workflow", "state", "status", "template"],
        [
            [
                rig,
                inst.get("id", "?"),
                inst.get("current_state", "?"),
                inst.get("status", "?"),
                inst.get("template_id", "?"),
            ]
            for rig, inst in rigs
        ],
    )
    return 0


def main() -> int:
    ap = argparse.ArgumentParser(description="Rig-flow implementation status (any rig).")
    ap.add_argument("rig", nargs="?", help="Rig name")
    ap.add_argument("--town-root", help="Town root (default: $GT_ROOT or ~/gt)")
    ap.add_argument("--list-rigs", action="store_true", help="List rigs with active workflows")
    ap.add_argument("--no-build", action="store_true", help="Skip compile/test commands")
    ap.add_argument("--tail", type=int, default=6, help="Agent log lines (0=off)")
    args = ap.parse_args()

    town = town_root_from_env(args.town_root)

    if args.list_rigs:
        return cmd_list_rigs(town)

    if not args.rig:
        ap.error("rig name required (or use --list-rigs)")

    rig_name = args.rig.strip()
    rig_dir = town / rig_name
    mayor_rig = rig_dir / "mayor" / "rig"

    if not rig_dir.is_dir():
        print(f"error: rig not found: {rig_dir}", file=sys.stderr)
        print("hint: gastown/scripts/rig_implementation_status.py --list-rigs", file=sys.stderr)
        return 1

    print(f"Town:  {town}")
    print(f"Rig:   {rig_name}")
    print(f"Mayor: {mayor_rig}")
    print()

    wf = find_workflow(town, rig_name)
    wf_state = ""
    if wf:
        wf_state = (wf.get("current_state") or "").strip()
        print("Workflow")
        print(f"  id:       {wf.get('id', '?')}")
        print(f"  template: {wf.get('template_id', '?')}")
        print(f"  state:    {wf_state}")
        print(f"  status:   {wf.get('status', '?')}")
        rework = wf.get("pending_rework")
        if rework:
            print(f"  rework:   {rework}")
    else:
        print("Workflow: (no instance in orchestrator/instances.json for this rig)")
    print()

    val = profile_validation(mayor_rig)
    layout = (val.get("layout_root") or "").strip()
    title_contains = (val.get("bead_title_contains") or "Implement ").strip()
    union_raw = val.get("required_files") or []
    union_count = len(union_raw) if isinstance(union_raw, list) else 0
    required = active_required_files(val)
    phases = val.get("delivery_phases") or []
    active_phase = (val.get("active_phase_id") or "").strip()

    print("Profile")
    print(f"  layout_root:         {layout or '(none)'}")
    print(f"  bead_title_contains: {title_contains!r}")
    print(f"  test_runner:         {(val.get('test_runner') or '(unset)')}")
    if isinstance(phases, list) and phases:
        print(f"  delivery_phases:     {len(phases)} (active: {active_phase or '(first)'})")
        print(f"  required_files:      {len(required)} active / {union_count} union")
    else:
        print(f"  required_files:      {len(required)}")
    kind = "go" if workflow_uses_go(val) else ("python" if workflow_uses_python(val) else "other")
    print(f"  detected_stack:      {kind}")
    print()

    beads_raw, bd_failed = list_implement_beads(mayor_rig, rig_dir / ".beads", title_contains)
    beads_by_path: dict[str, dict] = {}
    for b in beads_raw:
        path = extract_path_from_title(b["title"], title_contains)
        path = normalize_bead_path(path, layout, required)
        if path:
            beads_by_path[path] = b

    if bd_failed:
        print("warning: some bd list calls failed (is BEADS_DIR/dolt up?)")
        print()

    closed = sum(1 for b in beads_raw if b["status"] == "closed")
    in_prog = [b for b in beads_raw if b["status"] == "in_progress"]
    open_ = sum(1 for b in beads_raw if b["status"] == "open")
    total = len(order_required_files(required)) if required else len(beads_raw)

    print("Implement beads summary")
    print(f"  closed:      {closed}/{total}")
    print(f"  in_progress: {len(in_prog)}")
    print(f"  open:        {open_}")
    if in_prog:
        for b in in_prog:
            print(f"    → {b['id']}: {b['title']}")
    extras = [b for b in beads_raw if not path_matches_required(normalize_bead_path(extract_path_from_title(b["title"], title_contains), layout, required), required)]
    if extras and required:
        print(f"  extra beads: {len(extras)} (not in required_files)")
        for b in extras[:5]:
            print(f"    ! {b['id']}: {b['title']}")
    print()

    if required:
        rows = bead_rows(mayor_rig, required, title_contains, layout, val, beads_by_path)
        table = [
            [
                str(r.step),
                r.bead_id,
                r.status,
                "yes" if r.on_disk else "no",
                str(r.size) if r.on_disk else "—",
                r.path,
            ]
            for r in rows
        ]
        print_table(["#", "bead", "status", "file", "bytes", "path"], table)
        if workflow_uses_go(val):
            print()
            print("Per-bead verify (from profile; polecat uses Next bead line)")
            for r in rows:
                if r.verify_hint:
                    print(f"  {r.path}: {r.verify_hint}")
        print()
    elif beads_raw:
        print_table(
            ["bead", "status", "title"],
            [[b["id"], b["status"], b["title"][:70]] for b in beads_raw],
        )
        print()

    if not args.no_build:
        if workflow_uses_go(val):
            checks = go_checks(mayor_rig, layout, required)
            if checks:
                print("Go build (packages from required_files)")
                for label, exit_code, summary in checks:
                    mark = "OK" if exit_code == 0 else "FAIL"
                    print(f"  [{mark}] {label}")
                    if exit_code != 0 and summary:
                        print(f"         {summary}")
                print()
            elif required:
                print("Go build: no go.mod found under mayor/rig for this profile")
                print()
        elif workflow_uses_python(val):
            checks = python_checks(val, mayor_rig, layout)
            if checks:
                print("Python verify (qa_verify_command from profile)")
                for label, exit_code, summary in checks:
                    mark = "OK" if exit_code == 0 else "FAIL"
                    print(f"  [{mark}] {label}")
                    if exit_code != 0 and summary:
                        print(f"         {summary}")
                print()

    agent_dir = agent_dir_for_state(rig_dir, wf_state) if wf_state else None
    if agent_dir is None:
        agent_dir = rig_dir / "polecat" if (rig_dir / "polecat").is_dir() else None
    if agent_dir:
        state_path = agent_dir / "gt-agent-state.json"
        state = load_json(state_path)
        if isinstance(state, dict):
            print(f"Agent ({agent_dir.name})")
            print(f"  path:          {agent_dir}")
            print(f"  last_activity: {state.get('last_activity', '—')}")
            retry = state.get("orchestrated_retry")
            if retry:
                print(f"  retry.state:   {retry.get('state', '—')}")
                summ = (retry.get("summary") or "").strip()
                print(f"  retry.summary: {summ[:200] if summ else '(empty)'}")
                at = retry.get("at", "")
                if at:
                    print(f"  retry.at:      {at}")
            else:
                print("  orchestrated_retry: (none)")
            print()

        typescript = agent_dir / "typescript"
        if args.tail > 0 and typescript.is_file():
            lines = typescript.read_text(errors="replace").splitlines()
            tail = lines[-args.tail :]
            if tail:
                print(f"Agent log (last {len(tail)} lines)")
                for line in tail:
                    print(f"  {line}")
                print()

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
