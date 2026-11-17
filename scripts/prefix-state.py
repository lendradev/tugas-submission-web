#!/usr/bin/env python3
"""Mengubah ekspresi Alpine di template agar memakai prefix state "s".

Alpine.js build CSP tidak bisa memakai `x-data="filesApp"` karena evaluator
CSP mengabaikan scope dari provider Alpine.data. Pola pengganti adalah
`x-data="{ s: $app }"`; semua ekspresi di template memakai `s.namaProperti`.

Skrip ini idempoten: ekspresi yang sudah diawali "s." atau "$" dilewati.
"""
import re
import sys
from pathlib import Path

# Atribut Alpine tanpa modifier. x-html sengaja tidak ada: build CSP
# melarang x-html, sehingga ikon dikirim sebagai data-URI lewat src.
PLAIN_ATTRS = [
    "x-text", "x-show", "x-model", "x-init", "x-if", "x-cloak",
    ":disabled", ":value", ":href", ":src", ":placeholder", ":checked",
    ":selected", ":readonly", ":required", ":autofocus", ":multiple", ":class",
    ":aria-label", ":title", ":max", ":min", ":step",
]

# Atribut event: nama boleh diikuti modifier, mis. @input.debounce.250ms
EVENT_RE = re.compile(r'(@[a-zA-Z][\w-]*(?:\.[\w.-]+)*)="([^"]*)"')

RESERVED = {
    "true", "false", "null", "undefined", "new", "typeof", "void", "delete",
    "in", "instanceof", "this",
}


def prefix_expr(expr: str) -> str:
    """Menambahkan prefix s. pada identifier tingkat atas sebuah ekspresi."""
    expr = expr.strip()
    if not expr:
        return expr
    if expr.startswith("s.") or expr.startswith("$"):
        return expr
    if re.search(r"\bs\.[A-Za-z_$]", expr):
        return expr

    out = []
    i = 0
    n = len(expr)
    while i < n:
        ch = expr[i]
        if ch in "\"'`":
            quote = ch
            j = i + 1
            while j < n:
                if expr[j] == "\\":
                    j += 2
                    continue
                if expr[j] == quote:
                    j += 1
                    break
                j += 1
            out.append(expr[i:j])
            i = j
            continue
        if ch.isalpha() or ch in "_$":
            j = i
            while j < n and (expr[j].isalnum() or expr[j] in "_$"):
                j += 1
            word = expr[i:j]
            prev = expr[:i].rstrip()
            if prev.endswith(".") or word in RESERVED:
                out.append(word)
            else:
                out.append("s." + word)
            i = j
            continue
        out.append(ch)
        i += 1
    return "".join(out)


def transform(text: str) -> str:
    for attr in PLAIN_ATTRS:
        pattern = re.compile(rf'(?<![\w-]){re.escape(attr)}="([^"]*)"')

        def repl(m: re.Match) -> str:
            return f'{attr}="{prefix_expr(m.group(1))}"'

        text = pattern.sub(repl, text)

    # x-model.number / x-model.lazy: modifier setelah nama atribut.
    def model_modifier_repl(m: re.Match) -> str:
        return f'x-model.{m.group(1)}="{prefix_expr(m.group(2))}"'

    text = re.sub(
        r'x-model\.([a-zA-Z][\w-]*)="([^"]*)"',
        model_modifier_repl,
        text,
    )

    def event_repl(m: re.Match) -> str:
        return f'{m.group(1)}="{prefix_expr(m.group(2))}"'

    text = EVENT_RE.sub(event_repl, text)

    # x-for="item in items" -> "item in s.items"; identitas item tetap apa adanya.
    def for_repl(m: re.Match) -> str:
        body = m.group(1)
        item, sep, rest = body.partition(" in ")
        if not sep:
            return m.group(0)
        return f'x-for="{item} in {prefix_expr(rest)}"'

    text = re.sub(r'x-for="([^"]*)"', for_repl, text)

    # Kunci object literal pada :class tidak boleh diprefix.
    # Contoh: {active: cond} -> {active: cond}, bukan {s.active: cond}.
    text = re.sub(r"\{([A-Za-z_$][\w$]*):", lambda m: "{" + m.group(1) + ":", text)
    return text


def main() -> None:
    for path in sys.argv[1:]:
        p = Path(path)
        original = p.read_text()
        updated = transform(original)
        if updated != original:
            p.write_text(updated)
            print(f"diperbarui: {path}")
        else:
            print(f"tidak berubah: {path}")


if __name__ == "__main__":
    main()
