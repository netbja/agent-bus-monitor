"""Regressions for missing memory paths and misleadingly green partial adoption."""
import importlib.util
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('memory_check', Path(__file__).resolve().parents[1] / 'scripts/check-memory.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class MemoryCheckTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        for path in ['AGENTS.md', 'MEMORY.md', 'docs/PROJECT-JOURNAL.md', 'CLAUDE.md',
                     'docs/memory/PROTOCOL.md', 'docs/memory/journal/INDEX.md',
                     'skills/peer/SKILL.md', 'roles/peer.md']:
            file = self.root / path
            file.parent.mkdir(parents=True, exist_ok=True)
            file.write_text('Read docs/memory/INDEX.md\n')
        for name in ['agent-bus', 'agent-bus-master', 'agent-bus-sentinel']:
            path = self.root / 'skills' / name / 'SKILL.md'
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text('Read docs/memory/INDEX.md\n')
        for name in ['architect', 'coder', 'deploy', 'foureyes', 'master', 'sentinel']:
            (self.root / 'roles' / (name + '.md')).write_text('Read docs/memory/INDEX.md\n')
        (self.root / 'docs/memory/INDEX.md').write_text('[Protocol](PROTOCOL.md)\n[Journal](journal/INDEX.md)\n')

    def test_complete_structure_and_boot(self):
        self.assertEqual(module.check(self.root), [])

    def test_missing_entry_point_fails(self):
        (self.root / 'MEMORY.md').unlink()
        self.assertTrue(any('missing required entry point: MEMORY.md' in e for e in module.check(self.root)))

    def test_missing_skill_reference_fails(self):
        with (self.root / 'skills/peer/SKILL.md').open('a') as file:
            file.write('Read `docs/MISSING.md` before acting.\n')
        self.assertTrue(any('missing skill file reference' in e for e in module.check(self.root)))

    def test_optional_reference_requires_explicit_fallback(self):
        with (self.root / 'skills/peer/SKILL.md').open('a') as file:
            file.write('Read `docs/OPTIONAL.md`. If missing, report it and continue with the index.\n<!-- optional-file: docs/OPTIONAL.md | If missing, report it and continue with the index. -->\n')
        warnings = []
        self.assertEqual(module.check(self.root, warnings), [])
        self.assertEqual(len(warnings), 1)
        self.assertIn('docs/OPTIONAL.md', warnings[0])

    def test_boot_without_memory_fails(self):
        (self.root / 'roles/peer.md').write_text('Start work.\n')
        self.assertTrue(any('boot path does not reference' in e for e in module.check(self.root)))

    def test_missing_known_role_is_not_silently_ignored(self):
        (self.root / 'roles/sentinel.md').unlink()
        self.assertTrue(any('boot path does not reference memory index: roles/sentinel.md' in e for e in module.check(self.root)))

    def test_broken_relative_link_fails(self):
        (self.root / 'docs/memory/INDEX.md').write_text('[Missing](missing.md)\n')
        self.assertTrue(any('broken local link' in e for e in module.check(self.root)))

    def test_unindexed_note_fails(self):
        (self.root / 'docs/memory/forgotten.md').write_text('A forgotten lesson.\n')
        self.assertTrue(any('unreachable' in e for e in module.check(self.root)))

    def test_verified_note_needs_verification_date(self):
        note = self.root / 'docs/memory/lessons/lesson.md'
        note.parent.mkdir()
        note.write_text('---\nid: lesson\nproject: test\nscope: project\nkind: lesson\nstatus: active\ncreated: 2026-09-19\nlast_verified: unknown\nauthor: test\nconfidence: verified\n---\n[Evidence](../PROTOCOL.md)\n')
        with (self.root / 'docs/memory/INDEX.md').open('a') as file:
            file.write('[Lesson](lessons/lesson.md)\n')
        self.assertTrue(any('invalid last_verified' in e for e in module.check(self.root)))

    def test_optional_comment_cannot_hide_broken_memory_link(self):
        with (self.root / 'docs/memory/INDEX.md').open('a') as file:
            file.write('[Ghost](lessons/ghost.md)\n<!-- optional-file: lessons/ghost.md | x -->\n')
        self.assertTrue(any('broken local link: lessons/ghost.md' in e for e in module.check(self.root)))

    def test_optional_skill_comment_needs_visible_fallback(self):
        with (self.root / 'skills/peer/SKILL.md').open('a') as file:
            file.write('Read `docs/OPTIONAL.md`.\n<!-- optional-file: docs/OPTIONAL.md | If missing, report it and continue with the index. -->\n')
        self.assertTrue(any('visible nearby fallback' in e for e in module.check(self.root)))

    def test_optional_skill_cannot_hide_missing_memory_note(self):
        with (self.root / 'skills/peer/SKILL.md').open('a') as file:
            file.write('Read `docs/memory/ghost.md`. If missing, report it and continue with the index.\n<!-- optional-file: docs/memory/ghost.md | If missing, report it and continue with the index. -->\n')
        self.assertTrue(any('missing skill file reference: docs/memory/ghost.md' in e for e in module.check(self.root)))

    def test_future_and_inverted_dates_fail(self):
        for created, verified, expected in [('2999-01-01', '2999-01-02', 'in the future'),
                                             ('2026-01-02', '2026-01-01', 'predates created')]:
            with self.subTest(created=created):
                note = self.root / 'docs/memory/lessons/dates.md'
                note.parent.mkdir(exist_ok=True)
                note.write_text(f'---\nid: dates\nproject: test\nscope: project\nkind: lesson\nstatus: active\ncreated: {created}\nlast_verified: {verified}\nauthor: test\nconfidence: verified\n---\n[Evidence](../PROTOCOL.md)\n')
                self.assertTrue(any(expected in e for e in module.check(self.root)))


if __name__ == '__main__':
    unittest.main()
