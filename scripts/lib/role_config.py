"""Validated role profiles and client launch adapters (Python 3.11+, no dependencies)."""
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import tomllib

REPO = Path(__file__).resolve().parents[2]
NAME = re.compile(r"[a-z][a-z0-9_-]{0,31}\Z")
BUILTINS = {"claude-code": "claude", "codex": "codex", "kimi-cli": "kimi"}


def require(ok, message):
    if not ok:
        raise ValueError(message)


def string(value, label):
    require(isinstance(value, str) and bool(value.strip()) and not any(ord(c) < 32 for c in value),
            f"{label} must be a non-empty string without control characters")
    return value


def keys(table, allowed, label):
    require(isinstance(table, dict), f"{label} must be a table")
    require(not (table.keys() - allowed), f"unknown fields in {label}: {', '.join(sorted(table.keys() - allowed))}")


def load():
    path = Path(os.environ.get("ROLES_TOML", REPO / "roles.toml")).resolve()
    with path.open("rb") as f:
        data = tomllib.load(f)
    require(type(data.get("schema_version", 1)) is int and data.get("schema_version", 1) in (1, 2),
            "unsupported schema_version (expected 1 or 2)")
    require(isinstance(data.get("roles"), dict), "roles must be a table")
    for role, info in data["roles"].items():
        require(NAME.fullmatch(role), "invalid role name")
        require(isinstance(info, dict), f"role {role} must be a table")
        require(info.get("tier") in ("boot", "pop"), f"role {role}: tier must be boot or pop")
        skills = info.get("skills", [])
        require(isinstance(skills, list) and all(isinstance(s, str) and NAME.fullmatch(s) for s in skills),
                f"role {role}: invalid skills")
    if data.get("schema_version", 1) == 2:
        keys(data, {"schema_version", "defaults", "profiles", "roles", "adapters"}, "manifest")
        keys(data.get("defaults", {}), {"profile"}, "defaults")
        require(isinstance(data.get("profiles"), dict) and data["profiles"], "profiles must be a non-empty table")
        for name in data["profiles"]:
            require(NAME.fullmatch(name), "invalid profile name")
        for role in data["roles"]:
            resolve(data, role, path)
    return data, path


def resolve(data, role, path):
    require(role in data["roles"], f"unknown role: {role}")
    info = data["roles"][role]
    legacy = data.get("schema_version", 1) == 1
    if legacy:
        profile = {"client": "claude-code", "provider": "anthropic", "model": info.get("model"),
                   "options": {"permission": info.get("permission")}}
        if info.get("fallback"):
            profile["options"]["fallback"] = info["fallback"]
        name = "legacy"
    else:
        keys(info, {"profile", "tier", "skills"}, f"role {role} (move execution fields into a profile)")
        name = info.get("profile", data.get("defaults", {}).get("profile"))
        require(isinstance(name, str) and name in data["profiles"], f"role {role}: unknown or missing profile")
        profile = data["profiles"][name]
        keys(profile, {"client", "provider", "model", "reasoning", "reasoning_levels", "options", "env_refs"}, f"profile {name}")
    p = dict(profile, profile=name, role=role, legacy=legacy, skills=info.get("skills", []), manifest=str(path))
    for field in ("client", "provider", "model"):
        string(p.get(field), field)
    require(not p["model"].startswith("-"), "model must not start with '-'")
    require(NAME.fullmatch(p["client"]) and NAME.fullmatch(p["provider"]), "invalid client/provider identifier")
    options = p.get("options", {})
    require(isinstance(options, dict), "options must be a table")
    p["options"] = options
    if p["client"] in BUILTINS:
        for key, value in options.items():
            string(value, f"{p['client']} option {key}")
    refs = p.get("env_refs", {})
    require(isinstance(refs, dict) and all(isinstance(k, str) and isinstance(v, str)
            and re.fullmatch(r"[A-Z][A-Z0-9_]*", k) and re.fullmatch(r"[A-Z][A-Z0-9_]*", v)
            for k, v in refs.items()), "env_refs must map environment variable names to source variable names")
    require(not any(k in {"PATH", "HOME", "PYTHONPATH", "PYTHONHOME", "LD_PRELOAD", "ROLES_TOML", "CLAUDE_CODE_SESSION_ID"}
                    or k.startswith(("AGENT_BUS_", "AGENT_LAUNCH_")) for k in refs), "env_refs cannot override launcher identity or process controls")
    effort = p.get("reasoning")
    levels = p.get("reasoning_levels", [])
    require(isinstance(levels, list) and all(isinstance(v, str) and NAME.fullmatch(v) for v in levels),
            "reasoning_levels must be an array of level names")
    if effort is not None:
        string(effort, "reasoning")
        require(effort in levels, "reasoning requires reasoning_levels declaring support for this model/provider")
    client = p["client"]
    if client == "claude-code":
        keys(options, {"permission", "fallback"}, "Claude options")
        require(options.get("permission", "default") in {"default", "acceptEdits", "auto", "bypassPermissions", "manual", "dontAsk", "plan"}, "unsupported Claude permission")
        if options.get("fallback") is not None:
            string(options["fallback"], "fallback")
        require(effort is None or effort in {"low", "medium", "high", "xhigh", "max"}, "unsupported Claude reasoning level")
    elif client == "codex":
        keys(options, {"sandbox", "approval"}, "Codex options")
        require(options.get("sandbox", "read-only") in {"read-only", "workspace-write", "danger-full-access"}, "unsupported Codex sandbox")
        require(options.get("approval", "on-request") in {"on-request", "never"}, "unsupported Codex approval")
        require(effort is None or effort in {"minimal", "low", "medium", "high", "xhigh", "max", "ultra", "none"}, "unsupported Codex reasoning level")
    elif client == "kimi-cli":
        keys(options, {"permission"}, "Kimi options")
        require(options.get("permission", "default") in {"default", "yolo", "auto", "plan"}, "unsupported Kimi permission")
        require(effort is None, "kimi-cli adapter has no verified reasoning override; omit reasoning and configure the native model alias")
    else:
        adapters = data.get("adapters", {})
        require(isinstance(adapters, dict) and client in adapters, f"unknown client {client}: register an adapter")
        adapter = adapters[client]
        keys(adapter, {"executable"}, f"adapter {client}")
        executable = Path(string(adapter.get("executable"), "adapter executable"))
        p["adapter"] = str((path.parent / executable).resolve())
    return p


def skill_sources(names):
    roots = [Path(os.environ.get("REPO_SKILLS", REPO / "skills")),
             Path(os.environ.get("POCOCK_SKILLS_ROOT", Path.home() / "Tools/herdr-plugins/skills/skills")) / "engineering"]
    sources = []
    for name in names:
        source = next((root / name for root in roots if (root / name / "SKILL.md").is_file()), None)
        require(source is not None, f"skill not found in repo or Pocock collection: {name}")
        sources.append(source.resolve())
    return sources


def prompt_for(p):
    file = Path(os.environ.get("ROLES_DIR", REPO / "roles")) / (p["role"] + ".md")
    require(file.is_file(), f"missing role prompt file: {file}")
    prompt = f"Role source: {file.resolve()} (resolve its relative links from this directory: {file.resolve().parent}).\n\n" + file.read_text()
    if not p["legacy"]:
        sources = skill_sources(p["skills"])
        prompt += "\n\nRead and follow these role skills before work (paths to SKILL.md; resolve their relative links from their own directory):\n"
        prompt += "\n".join(str(s / "SKILL.md") for s in sources)
        prompt += "\nUse native skill invocation when available, otherwise read the files directly.\n"
    return file.resolve(), prompt


def cache_file(text, suffix):
    root = Path(os.environ.get("AGENT_LAUNCH_CACHE", Path(os.environ.get("XDG_CACHE_HOME", Path.home() / ".cache")) / "agentbus/launch"))
    root.mkdir(parents=True, exist_ok=True, mode=0o700)
    target = root / (hashlib.sha256(text.encode()).hexdigest() + suffix)
    if not target.exists():
        fd, tmp = tempfile.mkstemp(dir=root)
        try:
            with os.fdopen(fd, "w") as f:
                f.write(text)
            os.replace(tmp, target)
        finally:
            if os.path.exists(tmp):
                os.unlink(tmp)
    return str(target)


def build(p, project, prompt, prompt_path, dry):
    client, model, options = p["client"], p["model"], p["options"]
    effort = p.get("reasoning")
    if client == "claude-code":
        cmd = ["claude", "--model", model]
        if options.get("fallback"):
            cmd += ["--fallback-model", options["fallback"]]
        if options.get("permission", "default") != "default":
            cmd += ["--permission-mode", options["permission"]]
        if effort:
            cmd += ["--effort", effort]
        cmd += ["--name", project + ":" + p["role"]]
        cmd += ["--append-system-prompt-file", str(prompt_path)] if dry else ["--append-system-prompt", prompt]
        return cmd
    if client == "codex":
        cmd = ["codex", "--model", model, "-c", "model_provider=" + json.dumps(p["provider"]),
               "--sandbox", options.get("sandbox", "read-only"), "--ask-for-approval", options.get("approval", "on-request")]
        if effort:
            cmd += ["-c", "model_reasoning_effort=" + json.dumps(effort)]
        # Initial prompt is additive: never replace the user's developer instructions.
        return cmd + ["<role prompt and skill paths>" if dry else prompt]
    if client == "kimi-cli":
        body = '---\nname: bus-' + p["role"].replace("_", "-") + '\ndescription: Agent Bus role\n---\n\n${base_prompt}\n\n' + prompt
        file = "<generated agent.md>" if dry else cache_file(body, ".md")
        cmd = ["kimi", "--model", model, "--agent-file", file]
        permission = options.get("permission", "default")
        if permission != "default":
            cmd += ["--" + permission]
        return cmd
    spec = dict(p, project=project, prompt=prompt, prompt_path=str(prompt_path))
    file = "<resolved launch.json>" if dry else cache_file(json.dumps(spec), ".json")
    return [p["adapter"], "--spec-file", file]


def launch(role, project):
    require(NAME.fullmatch(role) and NAME.fullmatch(project), "invalid role or project name")
    data, path = load()
    p = resolve(data, role, path)
    prompt_path, prompt = prompt_for(p)
    dry = os.environ.get("AGENT_LAUNCH_DRYRUN") == "1"
    # Check required flags before making any cache files. --help does not start a session.
    if not dry and not p["legacy"] and p["client"] in BUILTINS:
        binary = BUILTINS[p["client"]]
        require(shutil.which(binary), f"client executable missing: {binary}")
        help_result = subprocess.run([binary, "--help"], capture_output=True, text=True, timeout=15)
        require(help_result.returncode == 0, f"{binary} --help failed")
        needed = {"claude-code": ["--model", "--append-system-prompt", "--name"],
                  "codex": ["--model", "--config", "--sandbox", "--ask-for-approval"],
                  "kimi-cli": ["--model", "--agent-file"]}[p["client"]]
        if p["client"] == "claude-code":
            if p.get("reasoning"):
                needed.append("--effort")
            if p["options"].get("fallback"):
                needed.append("--fallback-model")
            if p["options"].get("permission", "default") != "default":
                needed.append("--permission-mode")
        if p["client"] == "kimi-cli" and p["options"].get("permission", "default") != "default":
            needed.append("--" + p["options"]["permission"])
        require(all(flag in help_result.stdout for flag in needed), f"{binary} lacks required launch flags; inspect its version/help")
    cmd = build(p, project, prompt, prompt_path, dry)
    if dry:
        print(shlex.join(cmd))
        if not p["legacy"]:
            print(f"profile={p['profile']} client={p['client']} provider={p['provider']} reasoning={p.get('reasoning', 'client-default')}; skills={','.join(p['skills'])}")
        return
    env = dict(os.environ, AGENT_BUS_PROJECT=project, AGENT_BUS_AGENT=role,
               AGENT_BUS_CLIENT=p["client"], AGENT_BUS_PROVIDER=p["provider"],
               AGENT_BUS_MODEL=p["model"], AGENT_BUS_PROFILE=p["profile"],
               AGENT_BUS_REASONING=p.get("reasoning", ""), ROLES_TOML=str(path))
    # A child session must never publish the parent Claude session's transcript as its own.
    env.pop("CLAUDE_CODE_SESSION_ID", None)
    for target, source in p.get("env_refs", {}).items():
        require(source in os.environ, f"missing environment source for {target}")
        env[target] = os.environ[source]
    if p["client"] == "claude-code" and env.get("AGENT_LAUNCH_KEEP_API_KEY") != "1":
        env.pop("ANTHROPIC_API_KEY", None)
    os.execvpe(cmd[0], cmd, env)


def install_skills():
    data, path = load()
    links = {}
    for role in data["roles"]:
        p = resolve(data, role, path)
        client = p["client"]
        if "SKILLS_DEST" in os.environ:
            dest = Path(os.environ["SKILLS_DEST"])
        else:
            # Codex and Kimi both discover the portable ~/.agents/skills root.
            dest = Path.home() / (".claude/skills" if client == "claude-code" else ".agents/skills")
        for source in skill_sources(p["skills"]):
            target = dest / source.name
            require(not target.exists() or target.is_symlink(), f"refusing to overwrite non-symlink {target}")
            links[target] = source
    # Preflight all sources and collisions before the first write.
    for target, source in links.items():
        target.parent.mkdir(parents=True, exist_ok=True)
        if target.is_symlink() and target.resolve() == source:
            continue
        # Atomic replacement never follows a destination symlink.
        with tempfile.TemporaryDirectory(dir=target.parent) as tmp:
            link = Path(tmp) / "link"
            link.symlink_to(source, target_is_directory=True)
            os.replace(link, target)
        print(f"linked {target.name} -> {source}")


def main():
    require(len(sys.argv) > 1, "expected action: launch, install, validate, exists, tier, field")
    action, *args = sys.argv[1:]
    if action == "launch":
        require(len(args) == 2, "usage: agent-launch <role> <project>")
        launch(*args)
    elif action == "install":
        install_skills()
    else:
        data, path = load()
        if action == "validate":
            for role in data["roles"]:
                resolve(data, role, path)
        elif action == "exists":
            sys.exit(0 if args[0] in data["roles"] else 3)
        elif action == "tier":
            for role, info in data["roles"].items():
                if info.get("tier") == args[0]:
                    print(role)
        elif action == "field":
            if args[0] not in data["roles"]:
                sys.exit(3)
            p = resolve(data, args[0], path)
            value = data["roles"][args[0]].get(args[1], p.get(args[1], p["options"].get(args[1])))
            if value is not None:
                print("\n".join(value) if isinstance(value, list) else value)
        else:
            raise ValueError("unknown action")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, subprocess.SubprocessError) as exc:
        print(f"agent roles: {exc}", file=sys.stderr)
        sys.exit(1)
