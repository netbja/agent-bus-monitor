"""Exercise the real launcher with fake clients: never call a model or live bus."""
import json
import os
from pathlib import Path
import shlex
import subprocess
import tempfile
import unittest

REPO = Path(__file__).resolve().parents[1]
LAUNCH = REPO / 'scripts/agent-launch'
INSTALL = REPO / 'scripts/link-role-skills.sh'


class LaunchTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='agent profiles ')
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.manifest = self.root / 'roles.toml'
        self.skills = self.root / 'skills'
        (self.skills / 'test-skill').mkdir(parents=True)
        (self.skills / 'test-skill/SKILL.md').write_text('# Test skill\n')
        (self.root / 'coder.md').write_text('ROLE INSTRUCTIONS: review $(touch MUST_NOT_EXIST) `literal`\n')
        (self.root / 'reviewer.md').write_text('REVIEWER INSTRUCTIONS\n')
        self.bin = self.root / 'bin'
        self.bin.mkdir()
        self.env = dict(os.environ, ROLES_TOML=str(self.manifest), ROLES_DIR=str(self.root),
                        REPO_SKILLS=str(self.skills), POCOCK_SKILLS_ROOT=str(self.root / 'pocock'),
                        AGENT_LAUNCH_CACHE=str(self.root / 'cache'), SKILLS_DEST=str(self.root / 'installed'),
                        PATH=str(self.bin) + os.pathsep + os.environ['PATH'], PYTHONDONTWRITEBYTECODE='1')
        self.env.pop('AGENT_LAUNCH_DRYRUN', None)
        for client in ('claude', 'codex', 'kimi', 'extra'):
            file = self.bin / client
            file.write_text('''#!/usr/bin/env python3
import json, os, pathlib, sys
if sys.argv[1:] == ['--help']:
    print('--model --config --sandbox --ask-for-approval --append-system-prompt --name --effort --permission-mode --fallback-model --agent-file --yolo --auto --plan')
    raise SystemExit(0)
args = sys.argv[1:]
result = {'args': args, 'env': {k:v for k,v in os.environ.items() if k.startswith('AGENT_BUS_') or k in ('ROLES_TOML','CLAUDE_CODE_SESSION_ID','ANTHROPIC_API_KEY')}}
for flag in ('--agent-file', '--spec-file'):
    if flag in args:
        result['file'] = pathlib.Path(args[args.index(flag)+1]).read_text()
print(json.dumps(result))
raise SystemExit(int(os.environ.get('FAKE_EXIT', '0')))
''')
            file.chmod(0o755)

    def config(self, client='codex', extra='', roles='', options=''):
        self.manifest.write_text(f'''schema_version = 2
[defaults]
profile = "main"
[profiles.main]
client = "{client}"
provider = "test-provider"
model = "test-model"
{extra}
{options}
[roles.coder]
tier = "boot"
skills = ["test-skill"]
{roles}
''')

    def run_launch(self, role='coder', ok=True, **env):
        result = subprocess.run([str(LAUNCH), role, 'demo'], env=dict(self.env, **env),
                                capture_output=True, text=True, cwd=self.root)
        if ok:
            self.assertEqual(result.returncode, 0, result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout)
        return result

    def test_defaults_leave_effort_untouched_all_clients(self):
        for client in ('claude-code', 'codex', 'kimi-cli'):
            with self.subTest(client=client):
                self.config(client)
                result = json.loads(self.run_launch(CLAUDE_CODE_SESSION_ID='parent-id').stdout)
                self.assertNotIn('--effort', result['args'])
                self.assertFalse(any('model_reasoning_effort' in a for a in result['args']))
                self.assertNotIn('CLAUDE_CODE_SESSION_ID', result['env'])
                self.assertEqual(result['env']['AGENT_BUS_REASONING'], '')
                self.assertEqual(result['env']['AGENT_BUS_CLIENT'], client)
                payload = result.get('file', ' '.join(result['args']))
                self.assertIn('ROLE INSTRUCTIONS', payload)
                self.assertIn(str(self.skills / 'test-skill/SKILL.md'), payload)
                if client == 'kimi-cli':
                    self.assertIn('${base_prompt}', payload)
                self.assertFalse((self.root / 'MUST_NOT_EXIST').exists())

    def test_mixed_profile_does_not_inherit_execution_options(self):
        self.config('claude-code', options='[profiles.main.options]\npermission = "bypassPermissions"\nfallback = "fallback-model"',
                    roles='''[profiles.review]
client = "codex"
provider = "openai"
model = "review-model"
[roles.reviewer]
profile = "review"
tier = "pop"
skills = []''')
        result = json.loads(self.run_launch('reviewer').stdout)
        self.assertEqual(result['args'][1], 'review-model')
        self.assertNotIn('bypassPermissions', result['args'])
        self.assertNotIn('--fallback-model', result['args'])
        self.assertIn('model_provider="openai"', result['args'])
        self.assertEqual(result['env']['AGENT_BUS_AGENT'], 'reviewer')

    def test_explicit_effort_and_rejections(self):
        for client, token in [('claude-code', '--effort'), ('codex', 'model_reasoning_effort="xhigh"')]:
            self.config(client, 'reasoning = "xhigh"\nreasoning_levels = ["low", "xhigh"]')
            self.assertIn(token, json.loads(self.run_launch().stdout)['args'])
        for extra, client in [('reasoning = "xhigh"', 'codex'),
                              ('reasoning = "standard"\nreasoning_levels = ["standard"]', 'claude-code'),
                              ('reasoning = "low"\nreasoning_levels = ["low"]', 'kimi-cli')]:
            self.config(client, extra)
            self.run_launch(ok=False)

    def test_dry_run_does_not_launch_write_cache_or_expose_prompt(self):
        self.config('kimi-cli')
        result = self.run_launch(AGENT_LAUNCH_DRYRUN='1', ANTHROPIC_API_KEY='secret-value')
        self.assertIn('kimi', result.stdout)
        self.assertNotIn('secret-value', result.stdout)
        self.assertNotIn('ROLE INSTRUCTIONS', result.stdout)
        self.assertFalse((self.root / 'cache').exists())

    def test_missing_flag_stops_before_cache(self):
        self.config('kimi-cli')
        (self.bin / 'kimi').write_text('#!/bin/sh\necho old-version\n')
        result = self.run_launch(ok=False)
        self.assertIn('lacks required', result.stderr)
        self.assertFalse((self.root / 'cache').exists())

    def test_unknown_fields_and_missing_skills_rejected(self):
        self.config(extra='reasnoning = "low"')
        self.assertIn('unknown fields', self.run_launch(ok=False).stderr)
        self.config(roles='model = "wrong-place"')
        self.run_launch(ok=False)
        self.config()
        (self.skills / 'test-skill/SKILL.md').unlink()
        self.assertIn('skill not found', self.run_launch(ok=False).stderr)

    def test_billing_guard_is_claude_only_and_exit_propagates(self):
        for client in ('codex', 'kimi-cli', 'claude-code'):
            self.config(client)
            result = json.loads(self.run_launch(ANTHROPIC_API_KEY='key').stdout)
            self.assertEqual(result['env'].get('ANTHROPIC_API_KEY'), None if client == 'claude-code' else 'key')
        self.assertEqual(self.run_launch(ok=False, FAKE_EXIT='7').returncode, 7)
        result = json.loads(self.run_launch(ANTHROPIC_API_KEY='key', AGENT_LAUNCH_KEEP_API_KEY='1').stdout)
        self.assertEqual(result['env']['ANTHROPIC_API_KEY'], 'key')

    def test_custom_adapter_receives_resolved_spec(self):
        self.config('local-client', roles=f'[adapters.local-client]\nexecutable = {json.dumps(str(self.bin / "extra"))}')
        result = json.loads(self.run_launch().stdout)
        spec = json.loads(result['file'])
        self.assertEqual(spec['provider'], 'test-provider')
        self.assertIn('ROLE INSTRUCTIONS', spec['prompt'])
        self.assertEqual(spec['project'], 'demo')

    def test_installer_preflight_preserves_existing_and_makes_no_partial_links(self):
        self.config(roles='''[roles.reviewer]
tier = "pop"
skills = ["missing-skill"]''')
        result = subprocess.run([str(INSTALL)], env=self.env, capture_output=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.root / 'installed').exists())
        self.config()
        dest = self.root / 'installed/test-skill'
        dest.mkdir(parents=True)
        (dest / 'user-file').write_text('preserve')
        result = subprocess.run([str(INSTALL)], env=self.env, capture_output=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((dest / 'user-file').read_text(), 'preserve')

    def test_spawn_quotes_manifest_and_rejects_project_injection(self):
        self.config()
        env = dict(self.env, HERDR_ENV='1', AGENT_SPAWN_DRYRUN='1')
        command = [str(REPO / 'scripts/agent-spawn'), 'coder', 'demo']
        result = subprocess.run(command, env=env, cwd=self.root, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        launch = result.stdout.split('<new-tab-root-pane> ', 1)[1].strip()
        words = shlex.split(launch)
        self.assertIn('ROLES_TOML=' + str(self.manifest), words)
        result = subprocess.run(command[:-1] + ['demo;touch injected'], env=env, cwd=self.root, capture_output=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.root / 'injected').exists())

    def test_environment_references_and_bad_permission_shape(self):
        self.config('codex', 'env_refs = {OPENAI_API_KEY = "TEAM_KEY"}')
        self.assertIn('missing environment source', self.run_launch(ok=False).stderr)
        self.run_launch(TEAM_KEY='not-printed-in-dry-run')
        result = self.run_launch(AGENT_LAUNCH_DRYRUN='1', TEAM_KEY='not-printed-in-dry-run')
        self.assertNotIn('not-printed-in-dry-run', result.stdout)
        self.config('codex', 'env_refs = {AGENT_BUS_AGENT = "OTHER"}')
        self.assertIn('cannot override', self.run_launch(ok=False).stderr)
        self.config('codex', options='[profiles.main.options]\nsandbox = {}')
        result = self.run_launch(ok=False)
        self.assertNotIn('Traceback', result.stderr)

    def test_bootstrap_persists_selected_manifest_with_quoted_paths(self):
        import tomllib
        self.config()
        for name in ('docker', 'herdr'):
            stub = self.bin / name
            stub.write_text('#!/bin/sh\nexit 0\n')
            stub.chmod(0o755)
        project = self.root / 'project "quoted" $literal 🧪'
        project.mkdir()
        templates = self.root / 'templates'
        env = dict(self.env, HERDR_PLUS_PROJECTS_DIR=str(templates),
                   HERDR_PLUS_PATH=str(self.root / 'absent-plugin'))
        result = subprocess.run([str(REPO / 'scripts/bootstrap'), 'new', 'demo', str(project)],
                                env=env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        with (templates / 'demo.toml').open('rb') as f:
            template = tomllib.load(f)
        self.assertEqual(template['working_dir'], str(project))
        command = template['tabs'][0]['command']
        self.assertIn('ROLES_TOML=' + str(self.manifest), shlex.split(command))
        # Execute the generated command as the workspace shell would, against our stub.
        result = subprocess.run(['bash', '-c', command], env=self.env, cwd=project,
                                capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)['env']['ROLES_TOML'], str(self.manifest))

    def test_installer_targets_each_client_and_is_idempotent(self):
        import importlib.util
        from unittest.mock import patch
        spec = importlib.util.spec_from_file_location('role_config', REPO / 'scripts/lib/role_config.py')
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        self.config('claude-code', roles='''[profiles.review]
client = "kimi-cli"
provider = "moonshot"
model = "alias"
[roles.reviewer]
profile = "review"
tier = "pop"
skills = ["test-skill"]''')
        env = dict(self.env)
        env.pop('SKILLS_DEST')
        with patch.dict(os.environ, env, clear=True), patch.object(module.Path, 'home', return_value=self.root):
            module.install_skills()
            links = [self.root / p / 'test-skill' for p in ('.claude/skills', '.agents/skills')]
            before = [link.lstat().st_ino for link in links]
            module.install_skills()
            self.assertEqual(before, [link.lstat().st_ino for link in links])
            for link in links:
                self.assertEqual(link.resolve(), self.skills / 'test-skill')


if __name__ == '__main__':
    unittest.main()
