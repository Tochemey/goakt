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

"""checklinks.py: check that every relative Markdown link in the book resolves.

A link to another page must name an existing file, and a link with a fragment must name a heading of the
target page (anchors computed as GitHub computes them). Run from anywhere; prints each broken link and
exits non-zero if there is one."""
import pathlib
import re
import sys

BOOK = pathlib.Path(__file__).resolve().parents[1]
LINK = re.compile(r'\]\(([^)#\s]*\.md)?(#[^)\s]+)?\)')
HEADING = re.compile(r'^#{1,6} (.+?)\s*$')


def pages():
    """The book's own pages: README, the architecture overview, the chapters and the migration guides."""
    return [BOOK / 'README.md', BOOK / 'architecture.md'] + sorted((BOOK / 'chapters').glob('*.md')) + sorted((BOOK / 'migration').glob('*.md'))


def anchors(path):
    """The heading anchors of one page, deduplicated with -1, -2, ... as GitHub does."""
    seen, found, in_code = {}, set(), False
    for line in path.read_text().split('\n'):
        if line.startswith('```'):
            in_code = not in_code
            continue
        m = None if in_code else HEADING.match(line)
        if m:
            a = re.sub(r'[^\w\- ]', '', m.group(1).replace('`', '').lower()).replace(' ', '-')
            n = seen.get(a, 0)
            seen[a] = n + 1
            found.add(a if n == 0 else f'{a}-{n}')
    return found


cache, total, broken = {}, 0, 0
for page in pages():
    for number, line in enumerate(page.read_text().split('\n'), 1):
        for m in LINK.finditer(line):
            file, fragment = m.group(1), m.group(2)
            if not file and not fragment:
                continue
            total += 1
            target = (page.parent / file).resolve() if file else page
            where = f'{page.relative_to(BOOK)}:{number}'
            if not target.exists():
                broken += 1
                print(f'{where}: missing file {file}')
                continue
            if fragment:
                if target not in cache:
                    cache[target] = anchors(target)
                if fragment[1:] not in cache[target]:
                    broken += 1
                    print(f'{where}: missing anchor {file or ""}{fragment}')
print(f'{total} links checked, {broken} broken')
sys.exit(1 if broken else 0)
