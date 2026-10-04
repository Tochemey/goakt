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

"""Turn chapter and section references in the book into Markdown links.

Handles "Chapter N, §N.k", "Chapter N, \"Heading\"", "Chapters N and M", "Chapter N" and bare "§N.k".
Skips fenced code, headings, inline code and existing links. Reports references whose target does not exist.
Usage: linkrefs.py [--check]. With --check nothing is written, and the script exits non-zero when a reference is
not linked yet or does not resolve."""
import pathlib
import re
import sys

BOOK = pathlib.Path(__file__).resolve().parents[1]
CHAPTERS = BOOK / 'chapters'
HEADING = re.compile(r'^(#{2,4}) (.+?)\s*$')
DRY = '--check' in sys.argv


def anchor(text, seen):
    """GitHub anchor for a heading, as toc.py computes it."""
    a = re.sub(r'[^\w\- ]', '', text.replace('`', '').lower()).replace(' ', '-')
    n = seen.get(a, 0)
    seen[a] = n + 1
    return a if n == 0 else f'{a}-{n}'


def index_chapter(path):
    """Map section numbers and heading titles of one chapter to their anchors."""
    sections, titles, seen, in_code = {}, {}, {}, False
    for line in path.read_text().split('\n'):
        if line.startswith('```'):
            in_code = not in_code
            continue
        m = None if in_code else HEADING.match(line)
        if not m:
            continue
        title = m.group(2)
        a = anchor(title, seen)
        num = re.match(r'(\d+\.\d+(?:\.\d+)?) ', title)
        if num:
            sections[num.group(1)] = a
        titles[title.replace('`', '').lower()] = a
    return sections, titles


INDEX = {}
for p in sorted(CHAPTERS.glob('chap-*.md')):
    INDEX[int(p.stem.split('-')[1])] = index_chapter(p)

PROTECT = re.compile(r'`[^`]*`|\[[^\]]*\]\([^)]*\)')
REF = re.compile(
    r'Chapter (?P<c1>\d+), §(?P<s1>\d+\.\d+)'
    r'|Chapter (?P<c2>\d+), "(?P<h2>[^"]+)"'
    r'|Chapters (?P<c3>\d+)(?P<rest>(?:(?:, | and | to )\d+)+)'
    r'|Chapter (?P<c4>\d+)'
    r'|§(?P<s5>\d+\.\d+)')
problems = []


def target(chapter, here, frag=''):
    """Relative link to a chapter (and fragment) from the file being edited."""
    if here.parent == CHAPTERS:
        base = '' if here.stem == f'chap-{chapter:02d}' and frag else f'chap-{chapter:02d}.md'
    elif here.parent == BOOK:
        base = f'chapters/chap-{chapter:02d}.md'
    else:
        base = f'../chapters/chap-{chapter:02d}.md'
    return base + (f'#{frag}' if frag else '')


def section_link(sec, here, text, where):
    """Link text to section sec (e.g. '3.5'), or leave it and record a problem."""
    ch = int(sec.split('.')[0])
    a = INDEX.get(ch, ({}, {}))[0].get(sec)
    if a is None:
        problems.append(f'{where}: §{sec} not found')
        return text
    return f'[{text}]({target(ch, here, a)})'


def chapter_link(ch, here, text, where):
    """Link text to chapter ch, unless it is the file itself."""
    if ch not in INDEX:
        problems.append(f'{where}: Chapter {ch} does not exist')
        return text
    if here.stem == f'chap-{ch:02d}':
        return text
    return f'[{text}]({target(ch, here)})'


def rewrite(segment, here, where):
    """Replace every reference in a plain-text segment."""
    def repl(m):
        if m.group('c1'):
            ch, sec = int(m.group('c1')), m.group('s1')
            if sec.split('.')[0] != str(ch):
                problems.append(f'{where}: "{m.group(0)}" section belongs to another chapter')
                return m.group(0)
            return section_link(sec, here, m.group(0), where)
        if m.group('c2'):
            ch, title = int(m.group('c2')), m.group('h2')
            a = INDEX.get(ch, ({}, {}))[1].get(title.replace('`', '').lower())
            if a is None:
                return chapter_link(ch, here, f'Chapter {ch}', where) + f', "{title}"'
            return f'[{m.group(0)}]({target(ch, here, a)})'
        if m.group('c3'):
            nums = [m.group('c3')] + re.findall(r'\d+', m.group('rest'))
            seps = re.findall(r', | and | to ', m.group('rest'))
            out = 'Chapters ' + chapter_link(int(nums[0]), here, nums[0], where)
            for sep, n in zip(seps, nums[1:]):
                out += sep + chapter_link(int(n), here, n, where)
            return out
        if m.group('c4'):
            return chapter_link(int(m.group('c4')), here, m.group(0), where)
        return section_link(m.group('s5'), here, m.group(0), where)
    return REF.sub(repl, segment)


def process(path):
    """Link references in one file; return the number of changed lines."""
    lines, in_code, changed = path.read_text().split('\n'), False, 0
    for i, line in enumerate(lines):
        if line.startswith('```'):
            in_code = not in_code
            continue
        if in_code or line.startswith('#'):
            continue
        where = f'{path.relative_to(BOOK)}:{i + 1}'
        out, pos = [], 0
        for m in PROTECT.finditer(line):
            out.append(rewrite(line[pos:m.start()], path, where))
            out.append(m.group(0))
            pos = m.end()
        out.append(rewrite(line[pos:], path, where))
        new = ''.join(out)
        if new != line:
            lines[i] = new
            changed += 1
    if changed and not DRY:
        path.write_text('\n'.join(lines))
    return changed


files = sorted(CHAPTERS.glob('chap-*.md')) + [BOOK / 'architecture.md', BOOK / 'README.md'] + sorted((BOOK / 'migration').glob('*.md'))
total = 0
for f in files:
    n = process(f)
    total += n
    if n:
        print(f'{f.relative_to(BOOK)}: {n} lines')
print(f'{total} lines {"to link" if DRY else "changed"}')
print(f'{len(problems)} unresolved references')
for p in problems:
    print('  ' + p)
sys.exit(1 if problems or (DRY and total) else 0)
