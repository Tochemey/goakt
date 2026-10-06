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

"""checknames.py: check that every code name the book cites still exists (chapters, architecture overview).

For every "`Name` in `path/file.go`" (and "`A` and `B` in `path`") in the chapters, the
name must be declared in that file: a function, a method (`Type.method`), a type, a
constant, a variable or a struct field. Every `path` cited must exist. Run from the
repository root; prints each missing name and exits non-zero if there is one."""
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]
CHAPTERS = ROOT / 'book' / 'chapters'
GROUP = re.compile(r"((?:`[A-Za-z_][\w.]*`(?:, | and ))*`[A-Za-z_][\w.]*`) in `([\w./-]+\.go)`")
NAME = re.compile(r"`([A-Za-z_][\w.]*)`")
PATH = re.compile(r"`((?:[\w-]+/)+[\w.-]+\.(?:go|proto|mdx|md|yml|yaml|sh))`")


def declared(text, name):
    """Report whether name is declared in the Go source text."""
    if '.' in name:
        recv, meth = name.split('.', 1)
        if re.search(r"^func \(\w+ \*?" + re.escape(recv) + r"(?:\[[^\]]*\])?\) " + re.escape(meth) + r"\b", text, re.M):
            return True
        name = meth
    pats = [
        r"^func " + re.escape(name) + r"\b",
        r"^func \(\w* ?\*?\w+(?:\[[^\]]*\])?\) " + re.escape(name) + r"\b",
        r"^type " + re.escape(name) + r"\b",
        r"^\t" + re.escape(name) + r"\b",
        r"^(?:const|var) " + re.escape(name) + r"\b",
        r"^\t+" + re.escape(name) + r"(?:,\s*\w+)*\s",
    ]
    return any(re.search(p, text, re.M) for p in pats)


missing = 0
BOOK = ROOT / 'book'
for chapter in sorted(CHAPTERS.glob('chap-*.md')) + [BOOK / 'architecture.md']:
    body = chapter.read_text()
    for path in sorted(set(PATH.findall(body))):
        if not (ROOT / path).exists():
            print(f"{chapter.name}: missing file {path}")
            missing += 1
    seen = set()
    for names, path in GROUP.findall(body):
        src = ROOT / path
        if not src.exists():
            continue
        text = src.read_text()
        for name in NAME.findall(names):
            if (name, path) in seen:
                continue
            seen.add((name, path))
            if not declared(text, name):
                print(f"{chapter.name}: `{name}` not found in {path}")
                missing += 1
print(f"{missing} missing")
sys.exit(1 if missing else 0)
