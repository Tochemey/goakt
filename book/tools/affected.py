# MIT License
#
# Copyright (c) 2022-2026 GoAkt Team
#
# Permission is hereby granted, free of charge, to any person obtaining a copy
# of this software and associated documentation files (the "Software"), to deal
# in the Software without restriction, including without limitation the rights
# to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
# copies of the Software, and to permit persons to whom the Software is
# furnished to do so, subject to the following conditions:
#
# The above copyright notice and this permission notice shall be included in all
# copies or substantial portions of the Software.
#
# THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
# IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
# AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
# LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
# OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
# SOFTWARE.

"""affected.py: list the book chapters that cite code changed since a base revision.

Every chapter names the code it describes by path in the text, and the declarations in it by name ("`Name` in
`path/file.go`", "`Type.method`"). This script compares those citations with the changes between a base
revision and HEAD and prints, as Markdown, which chapters to reread. A changed Go file counts for a chapter only
through the declarations whose lines changed: the functions, methods, types, struct fields, constants and
variables, a doc comment counting for the declaration it documents. A chapter is listed when it cites the file
and one of those names. Any other changed file counts for every chapter that cites its path. A cited directory
(such as `actor/`) is ignored, because the package maps cite every directory.

Usage: affected.py [base]   (default base: origin/main), or affected.py - to read from stdin the output of
`git diff --unified=1000000 base...HEAD`. Always exits 0: the list is a review aid, not a gate."""
import pathlib
import re
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]
BOOK = ROOT / 'book'
PATH = re.compile(r'`((?:\.?[\w-]+/)+[\w.-]+\.(?:go|proto|yml|yaml|sh|mod)|Makefile|Dockerfile\.tools|go\.mod|\.golangci\.yml|\.mockery\.yml|codecov\.yml|buf\.gen\.yaml)`')
NAME = re.compile(r'`([A-Za-z_][\w.]*)`')
# enough context for git diff to print every line of a changed file, so both versions can be read from the diff
CONTEXT = 1000000
HUNK = re.compile(r'^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@')
FUNC = re.compile(r'^func\s+(?:\(\s*(?:\w+\s+)?\*?(\w+)(?:\[[^\]]*\])?\s*\)\s*)?(\w+)')
COMPOSITE = re.compile(r'^type\s+(\w+)(?:\[[^\]]*\])?\s+(?:struct|interface)\s*\{\s*$')
SINGLE = re.compile(r'^(?:type|var|const)\s+(\w+)')
GROUP = re.compile(r'^(?:type|var|const|import)\s*\(\s*$')
ENTRY = re.compile(r'^\t\*?([\w.]+)')


def read_diff(base):
    """The diff between the merge base of base and HEAD, every changed file in full."""
    out = subprocess.run(['git', 'diff', '--no-color', '--no-ext-diff', f'--unified={CONTEXT}', f'{base}...HEAD'], cwd=ROOT, capture_output=True, text=True, check=True)
    return out.stdout


def parse_diff(diff):
    """Map each changed file to its old and new lines and the numbers of the lines removed from and added to it."""
    files = {}
    current = None
    for line in diff.split('\n'):
        if line.startswith('diff --git '):
            current = {'old': [], 'new': [], 'removed': set(), 'added': set(), 'path': line.rsplit(' b/', 1)[-1], 'hunk': False}
            files[current['path']] = current
            continue
        if current is None:
            continue
        if not current['hunk']:
            if line.startswith('+++ b/'):
                del files[current['path']]
                current['path'] = line[len('+++ b/'):]
                files[current['path']] = current
            elif line.startswith('@@'):
                current['hunk'] = True
            continue
        if line.startswith('@@'):
            continue
        if line.startswith(' '):
            current['old'].append(line[1:])
            current['new'].append(line[1:])
        elif line.startswith('-'):
            current['old'].append(line[1:])
            current['removed'].add(len(current['old']))
        elif line.startswith('+'):
            current['new'].append(line[1:])
            current['added'].add(len(current['new']))
    return files


def top_level(line):
    """The names a top-level Go line declares, the text that closes its body when it opens one, and how the body is read."""
    match = FUNC.match(line)
    if match:
        receiver, name = match.groups()
        names = frozenset({name, f'{receiver}.{name}'} if receiver else {name})
        return names, ('}' if line.rstrip().endswith('{') else None), 'body', None
    match = COMPOSITE.match(line)
    if match:
        return frozenset({match.group(1)}), '}', 'entries', match.group(1)
    if GROUP.match(line):
        return frozenset(), ')', 'entries', None
    match = SINGLE.match(line)
    names = frozenset({match.group(1)}) if match else frozenset()
    stripped = line.rstrip()
    if stripped.endswith('{'):
        return names, '}', 'body', None
    if stripped.endswith('('):
        return names, ')', 'body', None
    return names, None, None, None


def declarations(lines):
    """Map each line number of a Go source to the names of the declaration it belongs to.

    A doc comment belongs to the declaration below it. In a grouped declaration or a struct or interface body,
    each entry is its own declaration: a struct field or interface method is named both alone and as `Type.name`."""
    owners = {}
    pending = []
    block = None
    entry = None
    inner = []
    for number, line in enumerate(lines, 1):
        if block is not None:
            closer, mode, names, prefix = block
            if line.startswith(closer):
                owners[number] = names
                block = None
                continue
            if mode == 'body':
                owners[number] = names
                continue
            if not line.strip():
                inner = []
                continue
            if line.startswith('\t//'):
                inner.append(number)
                continue
            match = ENTRY.match(line)
            if match:
                name = match.group(1).rsplit('.', 1)[-1]
                entry = frozenset({name, f'{prefix}.{name}'} if prefix else {name})
                for comment in inner:
                    owners[comment] = entry
                inner = []
            owners[number] = entry if entry is not None else names
            continue
        if line.startswith('//'):
            pending.append(number)
            continue
        if not line.strip():
            pending = []
            continue
        names, closer, mode, prefix = top_level(line)
        for comment in pending:
            owners[comment] = names
        pending = []
        owners[number] = names
        if closer:
            block = (closer, mode, names, prefix)
            entry = None
            inner = []
    return owners


def changed_names(change):
    """The names of the declarations whose lines a change to a Go file removed or added."""
    names = set()
    for lines, numbers in ((change['old'], change['removed']), (change['new'], change['added'])):
        owners = declarations(lines)
        for number in numbers:
            names |= owners.get(number, frozenset())
    return names


def citations(page):
    """The repository paths a page cites in backticks."""
    return {p for p in PATH.findall(page.read_text()) if not p.startswith(('github.com/', 'vendor/'))}


def cited_names(page):
    """The code names a page cites in backticks."""
    return set(NAME.findall(page.read_text()))


def cites_any(cited, names):
    """Report, without their type, which of names the cited names refer to, alone or as the last part of a dotted name."""
    return sorted({n.rsplit('.', 1)[-1] for n in names if n in cited or any(c.endswith('.' + n) for c in cited)})


def title(page):
    """The page's first heading, or its file name."""
    first = page.read_text().split('\n', 1)[0]
    return first[2:] if first.startswith('# ') else page.name


def main():
    """Print the affected chapters for the changed code."""
    base = sys.argv[1] if len(sys.argv) > 1 else 'origin/main'
    diff = sys.stdin.read() if base == '-' else read_diff(base)
    changes = {path: change for path, change in parse_diff(diff).items() if not path.startswith('book/')}
    names = {path: changed_names(change) for path, change in changes.items() if path.endswith('.go')}
    pages = sorted((BOOK / 'chapters').glob('chap-*.md')) + [BOOK / 'architecture.md']
    hits = {}
    for page in pages:
        paths = citations(page)
        cited = cited_names(page)
        matched = []
        for path in sorted(changes):
            if path not in paths:
                continue
            if path not in names:
                matched.append(f'`{path}`')
                continue
            found = cites_any(cited, names[path])
            if found:
                matched.append(f'`{path}` (' + ', '.join(f'`{n}`' for n in found) + ')')
        if matched:
            hits[page] = matched
    if not hits:
        print('No chapter of the book cites code changed in this pull request.')
        return
    print('These pages of the book cite code changed in this pull request. Reread them and update any statement the change makes untrue:\n')
    for page, matched in hits.items():
        print(f'- [{title(page)}]({page.relative_to(ROOT)}): ' + ', '.join(matched))


if __name__ == '__main__':
    main()
