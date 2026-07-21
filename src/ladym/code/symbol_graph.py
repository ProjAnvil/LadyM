"""Symbol + cross-reference extraction from a tree-sitter AST.

Given a parsed tree, yield :class:`RawSymbol` records (qualified_name, kind, signature,
docstring, line span, body text) and :class:`RawRef` records (caller → callee edges). The
indexer turns these into L2 memories and L4-style code_refs.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .languages import LanguageSpec


@dataclass
class RawSymbol:
    kind: str                       # function | class | method | module
    name: str                       # local identifier
    qualified_name: str             # module[.Class].name
    signature: str = ""
    docstring: str = ""
    line_start: int = 0             # 1-indexed
    line_end: int = 0
    body: str = ""
    calls: list[str] = field(default_factory=list)


@dataclass
class RawRef:
    src_symbol: str
    dst_symbol: str
    ref_kind: str = "calls"


def _node_text(node, src: bytes) -> str:  # type: ignore[no-untyped-def]
    return src[node.start_byte:node.end_byte].decode("utf-8", errors="replace")


def _walk(node, kinds: tuple[str, ...]):  # type: ignore[no-untyped-def]
    """Yield descendants whose type is in ``kinds``."""
    if node.type in kinds:
        yield node
    for child in node.children:
        yield from _walk(child, kinds)


def _first_child_by_field(node, field_name: str):  # type: ignore[no-untyped-def]
    try:
        return node.child_by_field_name(field_name)
    except Exception:
        return None


def _identifier_name(node, spec: LanguageSpec) -> str:  # type: ignore[no-untyped-def]
    """Best-effort: pull the identifier from the symbol's name field or first identifier child."""
    cand = _first_child_by_field(node, spec.name_field)
    if cand is not None:
        return _node_text(cand, b"") if False else cand.text.decode("utf-8", errors="replace")
    # fall back to first identifier-typed child
    for child in node.children:
        if child.type in ("identifier", "type_identifier", "property_identifier"):
            return child.text.decode("utf-8", errors="replace")
    return ""


def extract_symbols(
    tree,                         # tree_sitter.Tree
    src: bytes,
    spec: LanguageSpec,
    *,
    module_name: str,
    file_path: str,
    max_body_lines: int = 40,
) -> list[RawSymbol]:
    """Extract top-level + nested symbols from a parsed tree."""
    if not spec.definition_kinds:
        return []
    root = tree.root_node
    out: list[RawSymbol] = []

    # Track the enclosing class to build qualified names
    def _emit(node, parent_qname: str, kind: str) -> None:  # type: ignore[no-untyped-def]
        name = _identifier_name(node, spec)
        if not name:
            return
        qname = f"{parent_qname}.{name}" if parent_qname else f"{module_name}.{name}"
        body = _node_text(node, src)
        sig = _signature(node, src, spec, kind)
        doc = _docstring(node, src, spec)
        calls = _calls_in(node, src, spec)
        lines = body.count("\n") + 1
        out.append(
            RawSymbol(
                kind=kind,
                name=name,
                qualified_name=qname,
                signature=sig,
                docstring=doc,
                line_start=node.start_point[0] + 1,
                line_end=node.end_point[0] + 1,
                body=body if lines <= max_body_lines else _first_n_lines(body, max_body_lines),
                calls=calls,
            )
        )
        # recurse into class body for methods
        if kind == "class":
            class_body = _first_child_by_field(node, spec.body_field) or node
            for child in class_body.children:
                child_kind = _classify(child, spec)
                if child_kind:
                    _emit(child, qname, child_kind)

    for child in root.children:
        kind = _classify(child, spec)
        if kind:
            _emit(child, "", kind)
    return out


def _classify(node, spec: LanguageSpec) -> str | None:  # type: ignore[no-untyped-def]
    """Map a tree-sitter node type to a LadyM symbol kind, or None if it's not a definition."""
    t = node.type
    # unwrap decorated definitions
    if t == "decorated_definition":
        for ch in node.children:
            r = _classify(ch, spec)
            if r:
                return r
        return None
    if t in spec.definition_kinds:
        if "class" in t or "struct" in t or "interface" in t:
            return "class"
        if "method" in t or "constructor" in t:
            return "method"
        if "function" in t or "function_declaration" in t or t == "arrow_function":
            return "function"
        if "variable_declarator" in t:
            return "function"  # best effort for const f = () => ...
        if "type" in t or "interface" in t or "enum" in t:
            return "class"
        return "function"
    return None


def _signature(node, src: bytes, spec: LanguageSpec, kind: str) -> str:  # type: ignore[no-untyped-def]
    """Render a one-line signature, e.g. ``foo(x: int) -> str`` or ``def foo(x) -> int``.

    Strategy: take the first line of the node text (which for most languages is the
    declaration line), then strip trailing body markers. This avoids truncating Python
    type-annotation colons.
    """
    text = _node_text(node, src)
    first_line = text.split("\n", 1)[0].rstrip()
    if kind == "class":
        name = _identifier_name(node, spec)
        return f"class {name}".strip()
    # Strip trailing body-opening tokens from the first line. Order matters: we drop
    # everything from the first ``{`` (JS-family bodies) and from a trailing ``:`` only
    # when it's the final character (Python body opener), not inside a parameter list.
    sig = first_line
    if "{" in sig:
        sig = sig.split("{", 1)[0].rstrip()
    if "=>" in sig and sig.rstrip().endswith("=>"):
        sig = sig.rsplit("=>", 1)[0].rstrip()
    if spec.name == "python" and sig.rstrip().endswith(":"):
        sig = sig.rstrip()[:-1].rstrip()
    return sig.strip()


def _docstring(node, src: bytes, spec: LanguageSpec) -> str:  # type: ignore[no-untyped-def]
    """Grab the first comment/string immediately preceding or inside the def."""
    body = _first_child_by_field(node, spec.body_field)
    if body is not None and len(body.children) > 0:
        first = body.children[0]
        if spec.name == "python" and first.type == "expression_statement":
            for ch in first.children:
                if ch.type == "string":
                    txt = ch.text.decode("utf-8", errors="replace").strip()
                    return txt.strip('"""').strip("'''").strip('"').strip("'").strip()
        if first.type in spec.doc_node_kinds:
            return first.text.decode("utf-8", errors="replace").strip().lstrip("#").strip()
    # else look for a comment sibling just before the node
    prev = node.prev_sibling
    if prev is not None and prev.type in spec.doc_node_kinds:
        return prev.text.decode("utf-8", errors="replace").strip().lstrip("#/*").strip()
    return ""


def _calls_in(node, src: bytes, spec: LanguageSpec) -> list[str]:  # type: ignore[no-untyped-def]
    """Return a best-effort list of callable names referenced inside ``node``."""
    out: list[str] = []
    for call_node in _walk(node, spec.call_kinds):
        func = (
            _first_child_by_field(call_node, "function")
            or (call_node.child_by_field_name("function") if call_node.child_by_field_name("function") else None)
        )
        if func is None and call_node.children:
            func = call_node.children[0]
        if func is not None:
            txt = func.text.decode("utf-8", errors="replace").strip()
            # take the last segment for qualified calls like self.foo / obj.bar
            tail = txt.split(".")[-1]
            if tail and tail.isidentifier():
                out.append(tail)
    return out


def _first_n_lines(text: str, n: int) -> str:
    return "\n".join(text.split("\n")[:n])


def build_refs(symbols: list[RawSymbol], file_path: str) -> list[RawRef]:
    """Convert each symbol's ``calls`` list into intra-file refs (caller → callee).

    Both src and dst are resolved to qualified names when the callee is defined in this
    file. Cross-file resolution is done lazily at retrieval time against the symbol table.
    """
    name_to_qname = {s.name: s.qualified_name for s in symbols}
    refs: list[RawRef] = []
    seen: set[tuple[str, str]] = set()
    for sym in symbols:
        for callee in sym.calls:
            if callee in name_to_qname and callee != sym.name:
                dst_q = name_to_qname[callee]
                key = (sym.qualified_name, dst_q)
                if key in seen:
                    continue
                seen.add(key)
                refs.append(RawRef(src_symbol=sym.qualified_name, dst_symbol=dst_q, ref_kind="calls"))
    return refs
