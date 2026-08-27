#!/usr/bin/env python3
"""Render Markdown to self-contained HTML meant for CUT-AND-PASTE into documents.

    ./scripts/md-to-paste-html.py FILE.md [FILE.md ...]

Writes FILE.html beside each input.

WHY NOT PANDOC. Pandoc emits class-based CSS in a <style> block. Word, Google Docs
and Outlook all discard <style> on paste, so the result arrives as unstyled text
with the code blocks and tables indistinguishable from prose. Every element here
carries its own inline `style` attribute, which is the only form those editors
preserve.

FONT NAMES ARE SINGLE-QUOTED inside the double-quoted style attributes. Writing
`font-family:"SF Mono",...` inside `style="..."` closes the attribute early; the
browser then reads the rest as bogus attributes and drops the styling entirely.
That defect shipped in this repo (roksbnkctl#239) and silently unstyled 98 code
spans, so it is worth stating rather than rediscovering.

Deliberately a small, explicit subset rather than a full CommonMark
implementation: headings, fenced code, tables, lists, blockquotes, rules,
paragraphs, and inline bold/code/links. Anything outside that is emitted as
escaped text, which is visible and wrong rather than invisible and wrong.
"""
import html
import re
import sys
from pathlib import Path

MONO = "'SF Mono',Menlo,Consolas,'Liberation Mono',monospace"
SANS = "-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif"

S = {
    "body": f"background:#ffffff;color:#1a2733;font-family:{SANS};font-size:15px;"
            "line-height:1.62;max-width:52em;margin:0 auto;padding:28px 32px;",
    "h1": "font-size:26px;line-height:1.25;margin:0 0 18px;color:#0b2e4f;"
          "border-bottom:3px solid #d8e1ea;padding-bottom:10px;",
    "h2": "font-size:20px;margin:34px 0 12px;color:#0b2e4f;"
          "border-bottom:1px solid #e2e8ee;padding-bottom:6px;",
    "h3": "font-size:16px;margin:24px 0 8px;color:#123a5e;",
    "p": "margin:0 0 13px;",
    "pre": "background:#f6f8fa;border:1px solid #d8e1ea;border-radius:5px;"
           f"padding:12px 14px;overflow-x:auto;font-family:{MONO};font-size:12.5px;"
           "line-height:1.5;margin:0 0 15px;color:#12222f;white-space:pre;",
    "code": f"font-family:{MONO};font-size:13px;background:#f2f5f8;padding:1px 5px;"
            "border-radius:3px;color:#99235c;",
    "table": "border-collapse:collapse;margin:0 0 16px;width:100%;font-size:14px;",
    "th": "border:1px solid #c6d0da;padding:7px 10px;text-align:left;"
          "background:#eef2f6;font-weight:600;color:#0b2e4f;",
    "td": "border:1px solid #c6d0da;padding:7px 10px;text-align:left;vertical-align:top;",
    "ul": "margin:0 0 14px;padding-left:24px;",
    "li": "margin:0 0 5px;",
    "quote": "margin:0 0 15px;padding:2px 0 2px 14px;border-left:4px solid #c6d0da;color:#42566b;",
    "hr": "border:0;border-top:1px solid #d8e1ea;margin:26px 0;",
    "a": "color:#0b5fa5;",
}

# Inline markers, applied to ALREADY-ESCAPED text so a literal < in the source
# cannot become a tag. Order matters: code spans first, so **bold** inside a code
# span is left alone.
CODE_RE = re.compile(r"`([^`]+)`")
BOLD_RE = re.compile(r"\*\*([^*]+)\*\*")
LINK_RE = re.compile(r"\[([^\]]+)\]\(([^)]+)\)")


def inline(text: str) -> str:
    out = html.escape(text, quote=False)
    out = CODE_RE.sub(lambda m: f'<code style="{S["code"]}">{m.group(1)}</code>', out)
    out = BOLD_RE.sub(lambda m: f"<strong>{m.group(1)}</strong>", out)
    out = LINK_RE.sub(
        lambda m: f'<a href="{html.escape(m.group(2), quote=True)}" style="{S["a"]}">{m.group(1)}</a>',
        out,
    )
    return out


def render(md: str) -> str:
    lines = md.split("\n")
    out, i = [], 0

    while i < len(lines):
        line = lines[i]

        if line.startswith("```"):
            i += 1
            block = []
            while i < len(lines) and not lines[i].startswith("```"):
                block.append(lines[i])
                i += 1
            i += 1
            body = html.escape("\n".join(block), quote=False)
            out.append(f'<pre style="{S["pre"]}">{body}</pre>')
            continue

        if line.startswith("|") and i + 1 < len(lines) and re.match(r"^\|[\s:|-]+\|$", lines[i + 1]):
            header = [c.strip() for c in line.strip("|").split("|")]
            i += 2
            rows = []
            while i < len(lines) and lines[i].startswith("|"):
                rows.append([c.strip() for c in lines[i].strip("|").split("|")])
                i += 1
            t = [f'<table style="{S["table"]}"><thead><tr>']
            t += [f'<th style="{S["th"]}">{inline(c)}</th>' for c in header]
            t.append("</tr></thead><tbody>")
            width = len(header)
            for r in rows:
                # Rows are squared to the header width. A short row renders
                # misaligned; a LONG one has its extra cells dropped by the
                # browser, so content disappears with no error at all -- the
                # invisible-and-wrong failure this whole converter exists to
                # avoid. Truncation is reported rather than done quietly.
                if len(r) > width:
                    print(f"  ! row truncated to {width} cells (had {len(r)}): {r[:2]}...",
                          file=sys.stderr)
                    r = r[:width]
                elif len(r) < width:
                    r = r + [""] * (width - len(r))
                t.append("<tr>" + "".join(f'<td style="{S["td"]}">{inline(c)}</td>' for c in r) + "</tr>")
            t.append("</tbody></table>")
            out.append("".join(t))
            continue

        m = re.match(r"^(#{1,3})\s+(.*)$", line)
        if m:
            tag = f"h{len(m.group(1))}"
            out.append(f'<{tag} style="{S[tag]}">{inline(m.group(2))}</{tag}>')
            i += 1
            continue

        if re.match(r"^(---+|\*\*\*+)\s*$", line):
            out.append(f'<hr style="{S["hr"]}">')
            i += 1
            continue

        if re.match(r"^\s*([-*]|\d+\.)\s+", line):
            items, ordered = [], bool(re.match(r"^\s*\d+\.\s+", line))
            while i < len(lines) and re.match(r"^\s*([-*]|\d+\.)\s+", lines[i]):
                item = re.sub(r"^\s*([-*]|\d+\.)\s+", "", lines[i])
                i += 1
                # Continuation lines are indented under their bullet.
                while i < len(lines) and lines[i].startswith("  ") and lines[i].strip() \
                        and not re.match(r"^\s*([-*]|\d+\.)\s+", lines[i]):
                    item += " " + lines[i].strip()
                    i += 1
                items.append(item)
            tag = "ol" if ordered else "ul"
            body = "".join(f'<li style="{S["li"]}">{inline(x)}</li>' for x in items)
            out.append(f'<{tag} style="{S["ul"]}">{body}</{tag}>')
            continue

        if line.startswith(">"):
            block = []
            while i < len(lines) and lines[i].startswith(">"):
                block.append(lines[i].lstrip("> ").rstrip())
                i += 1
            out.append(f'<blockquote style="{S["quote"]}">{inline(" ".join(block))}</blockquote>')
            continue

        if not line.strip():
            i += 1
            continue

        para = []
        while i < len(lines) and lines[i].strip() and not re.match(
            r"^(```|\||#{1,3}\s|>|---+\s*$|\s*([-*]|\d+\.)\s+)", lines[i]
        ):
            para.append(lines[i].strip())
            i += 1
        if para:
            out.append(f'<p style="{S["p"]}">{inline(" ".join(para))}</p>')

    return "\n".join(out)


def main(argv):
    if not argv:
        print(__doc__.strip(), file=sys.stderr)
        return 2
    for arg in argv:
        src = Path(arg)
        md = src.read_text(encoding="utf-8")
        title = next((l[2:].strip() for l in md.split("\n") if l.startswith("# ")), src.stem)
        page = (
            "<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\">\n"
            f"<title>{html.escape(title, quote=False)}</title>\n"
            f"</head>\n<body style=\"{S['body']}\">\n{render(md)}\n</body></html>\n"
        )
        dst = src.with_suffix(".html")
        dst.write_text(page, encoding="utf-8")
        print(f"  {src.name} -> {dst.name}  ({len(page):,} bytes)")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
