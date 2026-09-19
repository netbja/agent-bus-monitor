#!/usr/bin/env python3
"""Check shared-memory structure and boot references without network or Redis."""
import argparse
from datetime import date
from pathlib import Path
import re
from urllib.parse import unquote, urlsplit

LINK = re.compile(r'(?<!!)\[[^\]]*\]\(([^)]+)\)')
CODE_FILE = re.compile(r'`([A-Za-z0-9_.][A-Za-z0-9_./-]*\.md)`')
OPTIONAL = re.compile(r'<!-- optional-file: ([^|\n]+)\|\s*(.+?)\s*-->')
FIELDS = {'id', 'project', 'scope', 'kind', 'status', 'created', 'last_verified', 'author', 'confidence'}


def check(root, warnings=None):
    root = Path(root).resolve()
    errors = []
    if warnings is None:
        warnings = []
    memory = root / 'docs/memory'
    required = ['AGENTS.md', 'MEMORY.md', 'docs/PROJECT-JOURNAL.md',
                'docs/memory/INDEX.md', 'docs/memory/PROTOCOL.md',
                'docs/memory/journal/INDEX.md']
    boot = sorted({root / 'CLAUDE.md',
                   *(root / 'skills' / name / 'SKILL.md' for name in
                     ['agent-bus', 'agent-bus-master', 'agent-bus-sentinel']),
                   *(root / 'roles' / (name + '.md') for name in
                     ['architect', 'coder', 'deploy', 'foureyes', 'master', 'sentinel']),
                   *(root / 'skills').glob('*/SKILL.md'),
                   *(root / 'roles').glob('*.md')})
    docs = set(memory.rglob('*.md')) | set(boot) | {root / p for p in required}
    edges = {}
    for name in required:
        if not (root / name).is_file():
            errors.append(f'missing required entry point: {name}')
    for source in boot:
        if not source.is_file() or 'docs/memory/INDEX.md' not in source.read_text():
            errors.append(f'boot path does not reference memory index: {source.relative_to(root)}')
    for source in sorted(docs):
        if not source.is_file():
            continue
        text = source.read_text()
        label = str(source.relative_to(root))
        optional = {p.strip(): why.strip() for p, why in OPTIONAL.findall(text)}
        edges[source.resolve()] = set()
        def fallback_allowed(target, position):
            # Internal reference memory must never be made optional. Exceptions
            # are for concrete, project-specific resources named by a skill.
            if source.name != 'SKILL.md' or target not in optional:
                return False
            candidates = [(source.parent / target).resolve(), (root / target).resolve()]
            if any(p.is_relative_to(memory) or p == root / 'MEMORY.md' or
                   p == root / 'docs/PROJECT-JOURNAL.md' for p in candidates):
                return False
            why = optional[target]
            visible = re.sub(r'<!--.*?-->', '', text[max(0, position - 400):position + 600], flags=re.S)
            if not re.search(r'\bif (missing|absent|unavailable)\b', why, re.I) or len(why.split()) < 5 or why not in visible:
                errors.append(f'{label}: optional-file {target} needs a visible nearby fallback instruction')
                return False
            warning = f'{label}: optional missing file {target}; fallback: {why}'
            if warning not in warnings:
                warnings.append(warning)
            return True

        for match in LINK.finditer(text):
            target = match.group(1)
            # Standard Markdown paths only; external URLs/anchors are not fetched.
            target = target.strip().strip('<>')
            parsed = urlsplit(target)
            if parsed.scheme or parsed.netloc or not parsed.path:
                continue
            path = (source.parent / unquote(parsed.path)).resolve()
            if not path.is_relative_to(root):
                errors.append(f'{label}: local link escapes repository: {target}')
            elif not path.exists() and not fallback_allowed(target, match.start()):
                errors.append(f'{label}: broken local link: {target}')
            elif path.is_file():
                edges[source.resolve()].add(path)
        if source.name == 'SKILL.md':
            for match in CODE_FILE.finditer(text):
                target = match.group(1)
                # Bare protocol names next to links are relative to the skill;
                # examples with repo paths are resolved from the repository root.
                if not (root / target).is_file() and not (source.parent / target).is_file():
                    linked = any(Path(urlsplit(t).path).name == target and
                                 (source.parent / urlsplit(t).path).is_file()
                                 for t in LINK.findall(text))
                    if not linked and not fallback_allowed(target, match.start()):
                        errors.append(f'{label}: missing skill file reference: {target}; provide optional-file fallback')
        if source.parent.name in {'decisions', 'lessons', 'open-questions', 'journal'} and source.name != 'INDEX.md':
            parts = text.split('---', 2)
            if len(parts) != 3 or parts[0].strip():
                errors.append(f'{label}: missing YAML frontmatter')
                continue
            fields = dict(re.findall(r'^([a-z_]+):\s*(.*?)\s*$', parts[1], re.M))
            for key in sorted(FIELDS):
                if not fields.get(key):
                    errors.append(f'{label}: missing metadata {key}')
            choices = {'scope': {'project', 'transferable'},
                       'kind': {'decision', 'lesson', 'question', 'journal'},
                       'status': {'proposed', 'active', 'resolved', 'superseded'},
                       'confidence': {'verified', 'inferred', 'unknown'}}
            for key, allowed in choices.items():
                if fields.get(key) not in allowed:
                    errors.append(f'{label}: invalid {key}')
            for key in ['created', 'last_verified']:
                value = fields.get(key, '')
                if key == 'last_verified' and value == 'unknown' and fields.get('confidence') != 'verified':
                    continue
                try:
                    parsed_date = date.fromisoformat(value)
                    if parsed_date > date.today():
                        errors.append(f'{label}: {key} is in the future')
                    if key == 'last_verified' and parsed_date < date.fromisoformat(fields.get('created', '')):
                        errors.append(f'{label}: last_verified predates created')
                except ValueError:
                    errors.append(f'{label}: invalid {key} date')
            if not LINK.search(parts[2]):
                errors.append(f'{label}: no linked evidence/context')
    reached, todo = set(), [(memory / 'INDEX.md').resolve()]
    while todo:
        path = todo.pop()
        if path not in reached:
            reached.add(path)
            todo.extend(edges.get(path, ()))
    for path in memory.rglob('*.md'):
        if path.resolve() not in reached:
            errors.append(f'memory note unreachable from INDEX: {path.relative_to(root)}')
    return errors


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=Path(__file__).resolve().parents[1])
    args = parser.parse_args()
    warnings = []
    failures = check(args.root, warnings)
    for warning in warnings:
        print("OPTIONAL:", warning)
    for failure in failures:
        print('FAIL:', failure)
    if failures:
        raise SystemExit(1)
    print(f'PASS: memory entry points, boot references, local links, metadata and reachability; {len(warnings)} optional missing file(s) reported')
