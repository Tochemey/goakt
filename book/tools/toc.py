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

"""toc.py: insert or refresh a '## Contents' section in every chapter, listing its ##/### headings
with GitHub-style anchors. The section is placed right after the chapter title."""
import re, pathlib

ROOT = pathlib.Path(__file__).resolve().parents[1] / 'chapters'
HEADING = re.compile(r'^(##|###) (.+?)\s*$')


def anchor(text, seen):
    """GitHub anchor for a heading: lowercase, drop punctuation, spaces to hyphens, dedupe."""
    a = re.sub(r'[^\w\- ]', '', text.replace('`', '').lower()).replace(' ', '-')
    n = seen.get(a, 0)
    seen[a] = n + 1
    return a if n == 0 else f'{a}-{n}'


for path in sorted(ROOT.glob('chap-*.md')):
    lines = path.read_text().split('\n')
    # drop an existing Contents section (from '## Contents' up to the next heading)
    if '## Contents' in lines:
        s = lines.index('## Contents')
        e = s + 1
        while e < len(lines) and not lines[e].startswith('#'):
            e += 1
        del lines[s:e]
    seen, toc, in_code = {}, [], False
    for l in lines:
        if l.startswith('```'):
            in_code = not in_code
            continue
        m = HEADING.match(l) if not in_code else None
        if m:
            indent = '  ' if m.group(1) == '###' else ''
            toc.append(f'{indent}- [{m.group(2)}](#{anchor(m.group(2), seen)})')
    title = next(i for i, l in enumerate(lines) if l.startswith('# '))
    lines[title + 1:title + 1] = ['', '## Contents', ''] + toc
    path.write_text('\n'.join(lines))
    print(f'{path.name}: {len(toc)} entries')
