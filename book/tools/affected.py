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

"""affected.py: list the book chapters that cite files changed since a base revision.

Every chapter names the code it describes by path, in its "Source files" line and in the text. This script
compares those paths with the files changed between a base revision and HEAD and prints, as Markdown, which
chapters to reread. Only file paths are matched; a cited directory (such as `actor/`) is ignored, because
the package maps cite every directory.

Usage: affected.py [base]   (default base: origin/main), or affected.py - to read the changed files from stdin,
one per line. Always exits 0: the list is a review aid, not a gate."""
import pathlib
import re
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]
BOOK = ROOT / 'book'
PATH = re.compile(r'`((?:\.?[\w-]+/)+[\w.-]+\.(?:go|proto|yml|yaml|sh|mod)|Makefile|Dockerfile\.tools|go\.mod|\.golangci\.yml|\.mockery\.yml|codecov\.yml|buf\.gen\.yaml)`')


def changed_files(base):
    """Files changed between the merge base of base and HEAD."""
    out = subprocess.run(['git', 'diff', '--name-only', f'{base}...HEAD'], cwd=ROOT, capture_output=True, text=True, check=True)
    return [line for line in out.stdout.splitlines() if line]


def citations(page):
    """The repository paths a page cites in backticks."""
    return {p for p in PATH.findall(page.read_text()) if not p.startswith(('github.com/', 'vendor/'))}


def title(page):
    """The page's first heading, or its file name."""
    first = page.read_text().split('\n', 1)[0]
    return first[2:] if first.startswith('# ') else page.name


def main():
    """Print the affected chapters for the changed files."""
    base = sys.argv[1] if len(sys.argv) > 1 else 'origin/main'
    files = [line.strip() for line in sys.stdin if line.strip()] if base == '-' else changed_files(base)
    changed = [f for f in files if not f.startswith('book/')]
    pages = sorted((BOOK / 'chapters').glob('chap-*.md')) + [BOOK / 'architecture.md']
    hits = {}
    for page in pages:
        cited = citations(page)
        matched = sorted(f for f in changed if f in cited)
        if matched:
            hits[page] = matched
    if not hits:
        print('No chapter of the book cites a file changed in this pull request.')
        return
    print('These pages of the book cite files changed in this pull request. Reread them and update any statement the change makes untrue:\n')
    for page, files in hits.items():
        print(f'- [{title(page)}]({page.relative_to(ROOT)}): ' + ', '.join(f'`{f}`' for f in files))


if __name__ == '__main__':
    main()
