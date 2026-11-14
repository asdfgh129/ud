#!/usr/bin/env python3
"""Docs-as-code builder for the Universal Downloader documentation set.

Usage:
    python3 tools/build_docs.py            # build docs/_site/index.html
    python3 tools/build_docs.py --check    # validate links/numbering only

Requires: python3 -m pip install markdown

The generated portal is a single self-contained HTML file (inline CSS/JS, no
build-time network access). Mermaid diagrams are enhanced at runtime when the
CDN is reachable; otherwise the diagram source remains readable as a code block.

Navigation contract
--------------------
* Every document gets a STABLE ASCII slug: home / readme / doc-01..doc-17 /
  adr-index / adr-0001..adr-0020 / changelog / root-readme. Non-ASCII fragments
  must be avoided: browsers percent-encode location.hash, which breaks naive
  lookups.
* `home` is a virtual section (no Markdown source): a generated status
  dashboard that is the default landing page. All existing sidebar links and
  deep links keep working unchanged.
* GROUPS entries are paths relative to docs/. An entry prefixed with "@root:"
  is resolved from the repository root instead (used for CHANGELOG.md, which
  must live at the root for Keep a Changelog tooling).
* Hash format: #/<slug> or #/<slug>~<scoped-heading-id>
* v0.1 hashes (#doc-<legacy-slug>) are still accepted through ALIASES, so old
  bookmarks keep working.
* Heading ids are scoped per document (h-<slug>--<tocid>) because the Markdown
  toc extension restarts its counter for every document.
"""

from __future__ import annotations

import html
import json
import posixpath
import re
import sys
from dataclasses import dataclass
from datetime import date
from pathlib import Path
from urllib.parse import unquote

try:
    import markdown as md
except ImportError:
    sys.exit("Missing dependency: run `python3 -m pip install markdown`")

ROOT = Path(__file__).resolve().parent.parent
DOCS = ROOT / "docs"
SITE = DOCS / "_site"
OUT = SITE / "index.html"

# Paths are relative to docs/ (converted to ROOT-relative internally).
GROUPS = [
    ("总览与治理", ["README.md", "01-项目章程.md", "02-术语表.md"]),
    ("需求与架构", [
        "03-需求规格说明书-SRS.md", "04-架构设计说明书-SAD.md",
        "05-详细设计说明书-LLD.md", "06-数据模型与存储设计.md",
        "16-互操作性与兼容性设计.md",
    ]),
    ("接口与扩展", [
        "07-接口规范-API.md", "08-扩展与插件开发规范.md",
        "09-Sidecar与外部依赖契约.md",
    ]),
    ("工程保障", [
        "10-技术选型与依赖清单.md", "11-安全与合规设计.md",
        "12-测试策略与测试计划.md", "13-构建发布与运维.md",
        "14-项目计划与风险管理.md", "15-开发规范与协作流程.md",
        "17-P0里程碑评审材料.md",
    ]),
    ("变更记录", ["@root:CHANGELOG.md"]),
]

ROOT_README_REL = "README.md"          # project README, relative to ROOT
DOCS_README_REL = "docs/README.md"     # default landing page
ADR_INDEX_REL = "docs/adr/README.md"
CHANGELOG_REL = "CHANGELOG.md"
HOME_SLUG = "home"             # virtual section: generated status dashboard


def group_rel(name: str) -> str:
    """Resolve a GROUPS entry to a ROOT-relative posix path."""
    return name[len("@root:"):] if name.startswith("@root:") else f"docs/{name}"


@dataclass
class Doc:
    rel: str          # path relative to ROOT, posix
    slug: str
    title: str
    meta: str
    body: str

    @property
    def src(self) -> Path:
        return ROOT / self.rel


def slugify(rel: str) -> str:
    """Return a stable, ASCII-only fragment slug for a document path."""
    if rel == ROOT_README_REL:
        return "root-readme"
    if rel == DOCS_README_REL:
        return "readme"
    if rel == ADR_INDEX_REL:
        return "adr-index"
    if rel == CHANGELOG_REL:
        return "changelog"
    stem = posixpath.basename(rel)
    stem = stem[:-3] if stem.endswith(".md") else stem
    m = re.match(r"^(\d+)-", stem)
    if m:
        return "doc-" + m.group(1).zfill(2)
    m = re.match(r"^ADR-(\d+)-", stem, re.I)
    if m:
        return "adr-" + m.group(1)
    s = re.sub(r"[^0-9A-Za-z]+", "-", stem).strip("-").lower()
    return s or "doc"


def legacy_slug(rel: str) -> str:
    """Slug produced by the v0.1 builder; used to keep old bookmarks alive."""
    old_rel = rel[len("docs/"):] if rel.startswith("docs/") else rel
    s = old_rel[:-3] if old_rel.endswith(".md") else old_rel
    s = re.sub(r"[^0-9A-Za-z\u4e00-\u9fff]+", "-", s).strip("-")
    return s.lower()


def discover() -> list[Doc]:
    ordered: list[str] = []
    seen: set[str] = set()

    def add(rel: str) -> None:
        if rel not in seen:
            ordered.append(rel)
            seen.add(rel)

    for _, names in GROUPS:
        for n in names:
            add(group_rel(n))
    add(ADR_INDEX_REL)
    for p in sorted((DOCS / "adr").glob("ADR-*.md")):
        add(f"docs/adr/{p.name}")
    for p in sorted(DOCS.glob("*.md")):
        add(f"docs/{p.name}")
    # Appended last so docs[0] stays the documentation index.
    add(ROOT_README_REL)

    docs: list[Doc] = []
    for rel in ordered:
        path = ROOT / rel
        if not path.exists():
            print(f"  [WARN] missing file: {rel}")
            continue
        text = path.read_text(encoding="utf-8")
        title_m = re.search(r"^#\s+(.+)$", text, re.M)
        title = title_m.group(1).strip() if title_m else rel
        meta_m = re.search(r"^>\s*(文档编号.*)$", text, re.M)
        meta = meta_m.group(1).strip() if meta_m else ""
        body_src = re.sub(r"^>\s*文档编号.*\n", "", text, count=1, flags=re.M)
        body_src = re.sub(r"^#\s+.+\n", "", body_src, count=1, flags=re.M)
        docs.append(Doc(rel=rel, slug=slugify(rel), title=title,
                        meta=meta, body=body_src))
    return docs


MD = md.Markdown(extensions=["tables", "fenced_code", "sane_lists", "attr_list", "toc"])


def render_body(doc: Doc) -> tuple[str, dict[str, str]]:
    """Render Markdown to HTML and scope all heading ids to this document."""
    MD.reset()
    out = MD.convert(doc.body)
    out = out.replace("<li>[ ] ", '<li><input type="checkbox" disabled> ')
    out = re.sub(r"<li>\[[xX]\] ", '<li><input type="checkbox" checked disabled> ', out)

    raw_ids = re.findall(r'<h[1-6] id="([^"]+)"', out)
    idmap = {rid: f"h-{doc.slug}--{rid}" for rid in set(raw_ids)}
    if idmap:
        out = re.sub(
            r'(<h[1-6] id=")([^"]+)(")',
            lambda m: m.group(1) + idmap.get(m.group(2), m.group(2)) + m.group(3),
            out,
        )
    return out, idmap


def rewrite_links(doc: Doc, out: str, lookup: dict[str, str],
                  idmaps: dict[str, dict[str, str]]) -> str:
    """Turn .md / #anchor links into portal hashes, then convert mermaid blocks."""
    def repl(m: re.Match) -> str:
        href = m.group(1)
        if re.match(r"^(?:[a-zA-Z][a-zA-Z0-9+.-]*:|//|#/|#doc-)", href):
            return m.group(0)
        if href.startswith("#"):
            raw = unquote(href[1:])
            scoped = idmaps.get(doc.slug, {}).get(raw)
            if not scoped:
                print(f"  [WARN] unresolved heading link in {doc.rel}: {href}")
                return m.group(0)
            return f'href="#/{doc.slug}~{scoped}"'
        path, sep, anchor = href.partition("#")
        if not path.lower().endswith(".md"):
            return m.group(0)  # images and other assets: leave untouched
        target = posixpath.normpath(posixpath.join(posixpath.dirname(doc.rel), unquote(path)))
        if target not in lookup:
            print(f"  [WARN] unresolved link in {doc.rel}: {href}")
            return m.group(0)
        tslug = lookup[target]
        suffix = ""
        if anchor:
            scoped = idmaps.get(tslug, {}).get(unquote(anchor))
            if scoped:
                suffix = "~" + scoped
            else:
                print(f"  [WARN] unresolved heading in {doc.rel}: {href}")
        return f'href="#/{tslug}{suffix}"'

    out = re.sub(r'href="([^"]*)"', repl, out)
    # External links open in a new tab so the portal state is not lost.
    out = re.sub(
        r'<a href="(https?://[^"]+)"(?! target=)',
        lambda m: f'<a href="{m.group(1)}" target="_blank" rel="noopener noreferrer"',
        out,
    )
    # Mermaid conversion last: its source may contain href-like text.
    def mermaid_repl(m: re.Match) -> str:
        return f'<pre class="mermaid">{html.unescape(m.group(1))}</pre>'
    out = re.sub(r'<pre><code class="language-mermaid">(.*?)</code></pre>',
                 mermaid_repl, out, flags=re.S)
    return out


def prepare() -> tuple[list[Doc], dict[str, str], dict[str, dict[str, str]],
                       dict[str, str], dict[str, str]]:
    """Discover, render and rewrite all documents; return everything the builder needs."""
    docs = discover()
    lookup = {d.rel: d.slug for d in docs}
    rendered = {d.slug: render_body(d) for d in docs}
    idmaps = {slug: pair[1] for slug, pair in rendered.items()}
    bodies = {d.slug: rewrite_links(d, rendered[d.slug][0], lookup, idmaps) for d in docs}
    aliases = {}
    for d in docs:
        if d.rel == ROOT_README_REL:
            continue
        old = legacy_slug(d.rel)
        if old != d.slug:
            aliases[old] = d.slug
    return docs, lookup, idmaps, bodies, aliases


def check_links(docs: list[Doc], lookup: dict[str, str],
                idmaps: dict[str, dict[str, str]]) -> int:
    """Validate every internal Markdown link in the source set. Returns error count."""
    errors = 0
    pat = re.compile(r"\[[^\]]*\]\(([^)]+)\)")
    for d in docs:
        raw = d.src.read_text(encoding="utf-8")
        for href in pat.findall(raw):
            href = href.split()[0]
            if re.match(r"^(?:https?:|mailto:|#)", href):
                continue
            path, sep, anchor = href.partition("#")
            if not path.lower().endswith(".md"):
                continue
            target = posixpath.normpath(
                posixpath.join(posixpath.dirname(d.rel), unquote(path)))
            if target not in lookup:
                print(f"  [ERROR] broken link in {d.rel}: {href} -> {target}")
                errors += 1
            elif anchor and unquote(anchor) not in idmaps.get(lookup[target], {}):
                print(f"  [WARN] heading not found in {d.rel}: {href}")
    return errors


# ---------------------------------------------------------------------------
# Home dashboard: status overview generated from the Markdown sources.
#
# Status is NOT maintained in a second hand-written list. It is read from each
# document's version header (`> 文档编号 … ｜ 状态：…`) or an ADR's front matter
# (`- **状态**：…`), with the docs/README.md §三 control table as fallback.
# ---------------------------------------------------------------------------
GROUP_DESC = {
    "总览与治理": "为什么做，以及文档体系如何管理",
    "需求与架构": "需求、架构、详细设计、数据模型与互操作性",
    "接口与扩展": "对外契约、插件与 sidecar 边界",
    "工程保障": "依赖、安全、测试、构建、计划与协作流程",
    "变更记录": "每次代码变更的唯一记录入口",
    "架构决策记录 ADR": "一个决策一份记录，状态与影响面一目了然",
}

# key -> (short label, explanation). These five buckets are the canonical
# vocabulary shown on the homepage; raw status strings stay on the cards.
STATUS_BUCKETS = [
    ("review", "待审核", "草案 / 评审中 / Proposed / 待评审"),
    ("accepted", "已决定", "已批准 / 已接受 / Accepted / 现行"),
    ("maintained", "持续维护", "长期维护中的活文档"),
    ("deprecated", "已废止", "已废止 / 已取代 / Deprecated"),
    ("unknown", "未标注", "版本头未声明状态（如项目 README）"),
]
BUCKET_LABEL = {k: label for k, label, _ in STATUS_BUCKETS}


def clean_status(s: str) -> str:
    return (s or "").replace("*", "").replace("`", "").strip()


def classify_status(raw: str) -> str:
    """Map a free-form status string to one of the five canonical buckets."""
    s = clean_status(raw).lower()
    if not s or s in {"—", "-", "无"}:
        return "unknown"
    # Only the leading status word is authoritative. Parenthetical notes often
    # mention the *target* state, e.g. "Proposed（待立项评审批量转 Accepted）"
    # or "草案（…待评审）"; matching those would misclassify the document.
    s = re.split(r"[（(]", s, maxsplit=1)[0].strip()
    if any(k in s for k in ("已废止", "已取代", "deprecated", "superseded")):
        return "deprecated"
    if any(k in s for k in ("持续维护", "维护中")):
        return "maintained"
    if any(k in s for k in ("已批准", "已接受", "accepted", "现行", "有效", "已定稿", "已生效")):
        return "accepted"
    if any(k in s for k in ("草案", "评审中", "待评审", "待立项", "待审核", "proposed", "提议", "待定")):
        return "review"
    return "unknown"


def parse_doc_index() -> dict[str, dict[str, str]]:
    """Read the authoritative doc list (docs/README.md §三) for summaries."""
    text = (DOCS / "README.md").read_text(encoding="utf-8")
    row = re.compile(
        r"^\|\s*([^|]+?)\s*\|\s*\[([^\]]+)\]\(([^)]+)\)\s*\|\s*"
        r"([^|]+?)\s*\|\s*([^|]+?)\s*\|\s*([^|]+?)\s*\|\s*$", re.M)
    out: dict[str, dict[str, str]] = {}
    for m in row.finditer(text):
        num, label, target, status, readers, summary = (g.strip() for g in m.groups())
        rel = posixpath.normpath(posixpath.join("docs", unquote(target)))
        out[rel] = {"num": num, "status": status, "readers": readers,
                    "summary": summary}
    return out


def parse_adr_index() -> dict[str, dict[str, str]]:
    """Read the ADR index table for concise titles, statuses and impact."""
    text = (DOCS / "adr" / "README.md").read_text(encoding="utf-8")
    row = re.compile(
        r"^\|\s*\[(\d{4})\]\(([^)]+)\)\s*\|\s*([^|]+?)\s*\|\s*"
        r"([^|]+?)\s*\|\s*([^|]+?)\s*\|\s*$", re.M)
    out: dict[str, dict[str, str]] = {}
    for m in row.finditer(text):
        num, target, title, status, impact = (g.strip() for g in m.groups())
        out[f"docs/adr/{target}"] = {"num": num, "title": title,
                                     "status": status, "impact": impact}
    return out


def p0_basis() -> set[str]:
    """ADR numbers that ADR-0020 declares as the basis of the P0 build."""
    p = DOCS / "adr" / "ADR-0020-P0实现基线与范围声明.md"
    m = re.search(r"前置 ADR\*\*[：:]\s*(.+)", p.read_text(encoding="utf-8"))
    return set(re.findall(r"\b\d{4}\b", m.group(1))) if m else set()


def status_of(d: Doc, doc_idx: dict, adr_idx: dict) -> str:
    if d.rel.startswith("docs/adr/ADR-"):
        head = "\n".join(d.src.read_text(encoding="utf-8").splitlines()[:12])
        m = re.search(r"\*\*状态\*\*[：:]\s*(.+)", head)
        raw = clean_status(m.group(1)) if m else ""
        return raw or adr_idx.get(d.rel, {}).get("status", "")
    m = re.search(r"状态[：:]\s*([^｜|]+)", d.meta)
    raw = clean_status(m.group(1)) if m else ""
    return raw or doc_idx.get(d.rel, {}).get("status", "")


def version_of(d: Doc) -> str:
    m = re.search(r"版本[：:]\s*(v[\d.]+)", d.meta)
    return m.group(1) if m else ""


def adr_owner(d: Doc) -> str:
    head = "\n".join(d.src.read_text(encoding="utf-8").splitlines()[:12])
    m = re.search(r"\*\*决策者\*\*[：:]\s*(.+)", head)
    return clean_status(m.group(1)) if m else "架构组"


def home_card(d: Doc, doc_idx: dict, adr_idx: dict,
              basis: set[str]) -> tuple[str, str, str]:
    """Return (card HTML, status bucket, doc kind) for one document."""
    rel, di, ai = d.rel, doc_idx.get(d.rel, {}), adr_idx.get(d.rel, {})
    raw = status_of(d, doc_idx, adr_idx)
    bucket = classify_status(raw)
    is_adr = rel.startswith("docs/adr/ADR-")
    version = version_of(d)
    extra = ""

    if is_adr:
        num = re.search(r"ADR-(\d+)", rel).group(1)
        id_label = f"ADR-{num}"
        title = ai.get("title") or re.sub(r"^ADR-\d+\s*", "", d.title)
        desc = f"影响文档：{ai.get('impact', '—')}"
        meta = f"决策者 {adr_owner(d)} · {clean_status(raw) or '未标注'}"
        kind = "adr"
        if num in basis:
            extra = '<span class="chip s-p0">P0 依据</span>'
    elif rel == ROOT_README_REL:
        id_label, title = "README", "项目 README（总入口）"
        desc = "构建与运行方式、代码地图、已验证门禁与已知限制。"
        meta = "根目录 · 项目入口"
        kind = "root"
        extra = '<span class="chip s-plain">项目入口</span>'
    elif rel == CHANGELOG_REL:
        id_label, title = "CHANGELOG", "更新日志 CHANGELOG"
        desc = di.get("summary", "Keep a Changelog 1.1.0 + SemVer 2.0.0")
        meta = "根目录 · 唯一变更真值源"
        kind = "root"
        extra = '<span class="chip s-p0">唯一变更源</span>'
    elif rel == ADR_INDEX_REL:
        id_label = "UD-ADR-00"
        title = "ADR 索引与演进时间线"
        desc = di.get("summary", "全部架构决策记录（唯一决策真值源）")
        meta = f"{version or 'v0.2'} · {di.get('readers', '全体')}"
        kind = "doc"
    else:
        stem = posixpath.basename(rel)
        num = stem.split("-")[0]
        id_label = f"UD-DOC-{num.zfill(2)}" if num.isdigit() else "UD-DOC-00"
        title = re.sub(r"^\d+-\s*", "", d.title)
        desc = di.get("summary", "（docs/README.md §三 未登记摘要）")
        meta = f"{version or 'v0.1'} · {di.get('readers', '全体')}"
        kind = "doc"
        if version and version != "v0.1":
            extra = '<span class="chip s-p0">P0 已校准</span>'

    search = " ".join((id_label, title, desc, meta, clean_status(raw))).lower()
    card = (
        f'<div class="hcard" data-status="{bucket}" data-kind="{kind}" '
        f'data-search="{html.escape(search, quote=True)}">'
        f'<div class="hc-top"><span class="hc-id">{html.escape(id_label)}</span>'
        f'<span class="chip s-{bucket}">{BUCKET_LABEL[bucket]}</span></div>'
        f'<a class="hc-title" href="#/{d.slug}">{html.escape(title)}</a>'
        f'<div class="hc-desc">{html.escape(desc)}</div>'
        f'<div class="hc-foot"><span class="hc-meta">{html.escape(meta)}</span>{extra}</div>'
        f'</div>'
    )
    return card, bucket, kind


def build_home(docs: list[Doc], by_rel: dict[str, Doc]) -> str:
    """Render the dashboard section.

    Counts and status badges come from the source docs. The short milestone
    banner is prose (it cannot be mechanically derived from a spec) and MUST
    be kept in sync with docs/README.md §三, the current-state truth source.
    The obsolete "ADR 0001–0020 still Proposed / P0 not reviewed" banner was
    removed on 2026-11-13: it contradicted the Accepted ADR index.
    """
    from collections import Counter
    doc_idx, adr_idx, basis = parse_doc_index(), parse_adr_index(), p0_basis()

    groups: list[tuple[str, list[str]]] = []
    for gi, (gname, names) in enumerate(GROUPS):
        rels = [group_rel(n) for n in names]
        if gi == 0 and ROOT_README_REL in by_rel:
            rels.insert(0, ROOT_README_REL)
        groups.append((gname, [r for r in rels if r in by_rel]))
    adr_rels = [ADR_INDEX_REL] + [d.rel for d in docs
                                  if d.rel.startswith("docs/adr/ADR-")]
    groups.append(("架构决策记录 ADR", [r for r in adr_rels if r in by_rel]))

    cards_html: list[str] = []
    buckets: Counter = Counter()
    kinds: Counter = Counter()
    for gname, rels in groups:
        cards: list[str] = []
        for rel in rels:
            card, bucket, kind = home_card(by_rel[rel], doc_idx, adr_idx, basis)
            cards.append(card)
            buckets[bucket] += 1
            kinds[kind] += 1
        cards_html.append(
            f'<section class="hgroup"><h3>{html.escape(gname)}'
            f'<span class="hc-num">{len(cards)} 份</span></h3>'
            f'<p class="hc-gdesc">{html.escape(GROUP_DESC.get(gname, ""))}</p>'
            f'<div class="hgrid">{"".join(cards)}</div></section>'
        )

    total = sum(buckets.values())
    counts = {k: buckets.get(k, 0) for k, _, _ in STATUS_BUCKETS}
    segs = "".join(
        f'<span class="hseg s-{k}" style="width:{counts[k] / total * 100:.2f}%" '
        f'title="{html.escape(label)} {counts[k]} 篇"></span>'
        for k, label, _ in STATUS_BUCKETS if counts[k]
    ) if total else ""
    legend = "".join(
        f'<span><i class="hseg s-{k}"></i>{html.escape(label)} {counts[k]}</span>'
        for k, label, _ in STATUS_BUCKETS if counts[k]
    )
    status_btns = "".join(
        [f'<button class="fbtn active" data-dim="status" data-f="all">全部 {total}</button>'] +
        [f'<button class="fbtn" data-dim="status" data-f="{k}">{html.escape(label)} {counts[k]}</button>'
         for k, label, _ in STATUS_BUCKETS]
    )
    kind_labels = [("doc", "规范文档"), ("adr", "ADR"), ("root", "项目文件")]
    kind_btns = "".join(
        [f'<button class="fbtn active" data-dim="kind" data-f="all">全部 {total}</button>'] +
        [f'<button class="fbtn" data-dim="kind" data-f="{k}">{html.escape(label)} {kinds.get(k, 0)}</button>'
         for k, label in kind_labels]
    )
    adr_count = kinds.get("adr", 0)
    # Derive the ADR status split from the ADR front matter (the ADR index is
    # the registry, but individual ADR headers are authoritative for
    # classify_status). The remaining prose banner must be kept in sync with
    # docs/README.md §三, as noted in the docstring above.
    adr_buckets: Counter = Counter(classify_status(status_of(d, doc_idx, adr_idx))
                                   for d in docs if d.rel.startswith("docs/adr/ADR-"))
    adr_accepted, adr_review = adr_buckets["accepted"], adr_buckets["review"]

    return f"""<article class="doc home" id="doc-{HOME_SLUG}" data-slug="{HOME_SLUG}">
<header class="hero">
  <div class="eyebrow">Universal Downloader / Documentation Home</div>
  <h1>开发文档 · 状态总览</h1>
  <p>把 BT / 磁力 / ed2k / HTTP(S) / FTP(S) / SFTP / Metalink / 迅雷链接 / 视频网站 / Telegram / 微信等来源，收敛到一条流水线、一个调度器、一份状态。本页按文档版本头与 ADR 状态行自动汇总：哪些仍在待审核、哪些已决定、哪些已按 P0 实现校准，一眼可见。左侧全部文档链接保持不变。</p>
  <div class="chips">
    <span class="chip s-plain">文档体系 v0.4</span>
    <span class="chip s-p0">P0 附加条件已关闭</span>
    <span class="chip s-review">P1 进行中 · 序 6 开放</span>
    <span class="chip s-plain">单文件 · 离线可读</span>
    <span class="chip s-plain">更新 {date.today().isoformat()}</span>
  </div>
  <div class="stats">
    <div class="stat"><b>{total}</b><small>文档总数（含 ADR / 根目录）</small></div>
    <div class="stat"><b>{counts['review']}</b><small>待审核（草案 / Proposed）</small></div>
    <div class="stat"><b>{counts['accepted']}</b><small>已决定（已批准 / 现行）</small></div>
    <div class="stat"><b>{adr_count}</b><small>架构决策记录 ADR</small></div>
  </div>
  <div class="hbar">{segs}</div>
  <div class="hlegend">{legend}</div>
</header>

<div class="banner warn"><strong>当前最该关注：</strong>P0「有条件通过」的附加条件已关闭，
  ADR 头部状态汇总为 <code>{adr_accepted}</code> 份 <code>Accepted</code>、
  <code>{adr_review}</code> 份待评审（新增提案不得作为实现依据）。P1 仍在进行：
  <a href="#/doc-14">docs/14 §11 序 6</a> 未关闭——L4 真实客户端矩阵未执行、
  判据与 P2P 礼仪范围冲突待评审、NFR-PERF-01 双机千兆基准未执行。
  当前态唯一速查为 <strong>docs/README §三</strong>，进度真值源为
  <a href="#/changelog">CHANGELOG</a>；范围外能力与残余风险见
  <a href="#/adr-0020">ADR-0020</a>。</div>

<section class="card">
  <h2>文档清单</h2>
  <p class="sub">状态取自各文档版本头的 <code>状态：</code> 字段与 ADR 头部的 <code>状态</code> 行，由构建器读取 Markdown 后规范化为 5 类。支持关键词搜索、按状态 / 类型筛选；点击卡片标题在本页打开正文。</p>
  <div class="htoolbar" id="homeFilters">
    <input id="homeSearch" class="hsearch" type="search" placeholder="搜索编号、标题、摘要、责任人或状态…" autocomplete="off">
    <div class="frow"><span>按状态</span><div class="filters">{status_btns}</div></div>
    <div class="frow"><span>按类型</span><div class="filters">{kind_btns}</div></div>
  </div>
  {''.join(cards_html)}
  <div class="hempty" id="homeEmpty">没有匹配的文档，试试其他关键词或筛选条件。</div>
</section>

<section class="card">
  <h2>从这里开始</h2>
  <p class="sub">四个最常用的入口；其余文档按左侧分类导航进入。</p>
  <div class="hquick">
    <a href="#/root-readme"><b>项目 README</b><span>构建、运行、代码地图与已知限制</span></a>
    <a href="#/readme"><b>文档总览与文档控制</b><span>状态机、编号规则、阅读路径</span></a>
    <a href="#/adr-0020"><b>ADR-0020 P0 实现基线</b><span>范围外能力与残余风险清单</span></a>
    <a href="#/changelog"><b>CHANGELOG</b><span>每次代码变更的唯一记录入口</span></a>
  </div>
  <div class="hnote"><b>状态口径：</b>规范化映射为
    {' · '.join(f'{html.escape(label)}（{html.escape(desc)}）' for _, label, desc in STATUS_BUCKETS)}。
    文档状态机为 <code>草案 → 评审中 → 已批准 → 已废止</code>（<a href="#/readme">docs/README §一</a>），
    ADR 状态定义见 <a href="#/adr-index">ADR 索引 §状态定义</a>。本页与全部卡片均由
    <code>tools/build_docs.py</code> 从 Markdown 生成，修改文档状态后重跑 <code>make docs</code> 即自动更新，
    不存在需要手工同步的第二份清单。</div>
</section>
</article>"""


def validate_html(page: str, docs: list[Doc]) -> int:
    """Validate the generated portal's own navigation graph. Returns error count."""
    errors = 0
    slugs = {d.slug for d in docs} | {HOME_SLUG}
    ids = set(re.findall(r'\sid="([^"]+)"', page))
    for href in re.findall(r'href="(#[^"]*)"', page):
        raw = unquote(href[1:])
        if not raw.startswith("/"):
            print(f"  [ERROR] unexpected hash link: {href}")
            errors += 1
            continue
        rest = raw[1:]
        slug, sep, anchor = rest.partition("~")
        if slug not in slugs:
            print(f"  [ERROR] hash points to unknown document: {href}")
            errors += 1
        if anchor and anchor not in ids:
            print(f"  [ERROR] hash points to missing heading: {href}")
            errors += 1
    nav_targets = set(re.findall(r'data-target="([^"]+)"', page))
    section_slugs = set(re.findall(r'data-slug="([^"]+)"', page))
    if HOME_SLUG not in section_slugs:
        print(f"  [ERROR] homepage section missing (rebuild with tools/build_docs.py)")
        errors += 1
    if len(re.findall(r'class="hcard"', page)) != len(docs):
        print(f"  [ERROR] homepage card count != document count ({len(docs)})")
        errors += 1
    for missing in sorted(nav_targets - section_slugs):
        print(f"  [ERROR] nav item has no section: {missing}")
        errors += 1
    for orphan in sorted(section_slugs - nav_targets):
        print(f"  [ERROR] section has no nav item: {orphan}")
        errors += 1
    leftover = re.findall(r'href="([^"]*\.md[^"]*)"', page)
    if leftover:
        print(f"  [ERROR] {len(leftover)} raw .md href(s) left in HTML: {leftover[:3]}")
        errors += 1
    return errors


CSS = """
:root{--bg:#ffffff;--bg-soft:#f7f8fa;--panel:#fbfcfd;--fg:#1f2328;--fg-soft:#57606a;
--line:#d8dee4;--accent:#2563eb;--accent-soft:#eaf1ff;--code-bg:#f6f8fa;--warn:#b45309;
--shadow:0 1px 3px rgba(16,24,40,.06)}
@media (prefers-color-scheme:dark){:root{--bg:#161b22;--bg-soft:#0d1117;--panel:#1c2128;
--fg:#e6edf3;--fg-soft:#9aa7b4;--line:#30363d;--accent:#6ea8fe;--accent-soft:#1b2a44;
--code-bg:#1c2128;--shadow:none}}
*{box-sizing:border-box}
html,body{margin:0;padding:0;height:100%}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Hiragino Sans GB","Microsoft YaHei",sans-serif;
background:var(--bg);color:var(--fg);font-size:15px;line-height:1.75;-webkit-font-smoothing:antialiased}
#app{display:flex;height:100vh;overflow:hidden}
#sidebar{width:310px;flex:0 0 310px;background:var(--bg-soft);border-right:1px solid var(--line);
display:flex;flex-direction:column;overflow:hidden}
.brand{padding:20px 20px 14px;border-bottom:1px solid var(--line)}
.brand h1{margin:0;font-size:16px;letter-spacing:.2px}
.brand p{margin:4px 0 0;font-size:12px;color:var(--fg-soft)}
#search{margin:14px 16px 8px;padding:8px 11px;border:1px solid var(--line);border-radius:8px;
background:var(--bg);color:var(--fg);font-size:13px;outline:none}
#search:focus{border-color:var(--accent)}
#nav{flex:1;overflow-y:auto;padding:6px 10px 24px}
.nav-group{margin:14px 0 4px;padding:0 10px;font-size:11px;font-weight:700;letter-spacing:1px;
color:var(--fg-soft);text-transform:uppercase}
.nav-item{display:block;padding:6px 10px;margin:1px 0;border-radius:7px;color:var(--fg);
text-decoration:none;font-size:13px;line-height:1.45;cursor:pointer;border:1px solid transparent}
.nav-item:hover{background:var(--panel);border-color:var(--line)}
.nav-item.active{background:var(--accent-soft);color:var(--accent);font-weight:600;border-color:transparent}
.nav-item .num{display:inline-block;min-width:34px;color:var(--fg-soft);font-variant-numeric:tabular-nums}
.nav-item.active .num{color:var(--accent)}
#main{flex:1;overflow-y:auto}
#content{max-width:980px;margin:0 auto;padding:38px 52px 120px}
.doc{display:none;animation:fade .18s ease}
.doc.active{display:block}
@keyframes fade{from{opacity:.4;transform:translateY(4px)}to{opacity:1;transform:none}}
.doc-head{border-bottom:1px solid var(--line);padding-bottom:18px;margin-bottom:28px}
.doc-head h1{font-size:29px;line-height:1.35;margin:0 0 10px;letter-spacing:-.4px}
.doc-meta{font-size:12.5px;color:var(--fg-soft)}
.badge{display:inline-block;padding:2px 9px;border-radius:20px;background:var(--accent-soft);
color:var(--accent);font-size:11.5px;font-weight:600;margin-right:8px}
h1,h2,h3,h4{scroll-margin-top:18px}
h2{font-size:21px;margin:38px 0 14px;padding-bottom:8px;border-bottom:1px solid var(--line)}
h3{font-size:17px;margin:28px 0 10px}
h4{font-size:15px;margin:22px 0 8px;color:var(--fg-soft)}
p{margin:10px 0}
ul,ol{padding-left:24px;margin:10px 0}
li{margin:4px 0}
a{color:var(--accent);text-decoration:none;cursor:pointer}
a:hover{text-decoration:underline}
code{background:var(--code-bg);border:1px solid var(--line);border-radius:5px;
padding:1px 5px;font-size:12.8px;font-family:"SFMono-Regular",Consolas,"Liberation Mono",monospace}
pre{background:var(--code-bg);border:1px solid var(--line);border-radius:9px;
padding:14px 16px;overflow-x:auto;line-height:1.55}
pre code{background:none;border:none;padding:0;font-size:12.6px}
pre.mermaid{background:var(--panel);text-align:center;white-space:pre-wrap;font-size:12.5px}
table{border-collapse:collapse;width:100%;margin:16px 0;font-size:13.5px;display:block;overflow-x:auto}
th,td{border:1px solid var(--line);padding:8px 11px;text-align:left;vertical-align:top}
th{background:var(--bg-soft);font-weight:650;white-space:nowrap}
tr:nth-child(even) td{background:var(--panel)}
blockquote{margin:14px 0;padding:10px 16px;border-left:3px solid var(--accent);
background:var(--accent-soft);border-radius:0 7px 7px 0;color:var(--fg)}
blockquote p{margin:4px 0}
hr{border:none;border-top:1px solid var(--line);margin:30px 0}
input[type=checkbox]{margin-right:6px}
.footnote{margin-top:60px;padding-top:16px;border-top:1px solid var(--line);
font-size:12px;color:var(--fg-soft)}
@media(max-width:900px){#sidebar{width:230px;flex-basis:230px}#content{padding:28px 24px 90px}}

/* ---- home dashboard ---- */
#content.home-active{max-width:1180px;padding:0 42px 100px}
.nav-home{font-weight:650;margin-bottom:6px;border-color:var(--line);background:var(--panel)}
.home .hero{padding-top:34px;border-bottom:1px solid var(--line);margin-bottom:22px}
.home .eyebrow{font-size:11.5px;letter-spacing:1.5px;text-transform:uppercase;
color:var(--accent);font-weight:700}
.home .hero h1{font-size:31px;line-height:1.3;margin:9px 0 11px;letter-spacing:-.5px}
.home .hero p{margin:0;color:var(--fg-soft);max-width:840px;font-size:14.5px}
.chips{display:flex;gap:8px;flex-wrap:wrap;margin-top:16px}
.chip{display:inline-flex;align-items:center;padding:2px 10px;border-radius:999px;
font-size:11.5px;font-weight:650;line-height:1.6;border:1px solid transparent;white-space:nowrap}
.chip.s-review{background:#fff4e0;color:#9a6300;border-color:#f5dfb5}
.chip.s-accepted{background:#e7f6ed;color:#177245;border-color:#c7e9d4}
.chip.s-maintained,.chip.s-p0{background:var(--accent-soft);color:var(--accent);border-color:#cfe1fb}
.chip.s-deprecated{background:#fdeeee;color:#a33a3a;border-color:#f3cccc}
.chip.s-unknown{background:var(--bg-soft);color:var(--fg-soft);border-color:var(--line)}
.chip.s-plain{background:var(--panel);color:var(--fg-soft);border-color:var(--line)}
@media (prefers-color-scheme:dark){
.chip.s-review{background:#3a2c12;color:#f0c674;border-color:#5c451c}
.chip.s-accepted{background:#143526;color:#8fdfae;border-color:#1f5238}
.chip.s-deprecated{background:#3a1e1e;color:#f0a3a3;border-color:#5c2b2b}
.chip.s-maintained,.chip.s-p0{border-color:#2c4670}}
.stats{display:grid;grid-template-columns:repeat(4,1fr);gap:12px;margin:22px 0 14px}
.stat{background:var(--panel);border:1px solid var(--line);border-radius:10px;padding:14px 16px}
.stat b{display:block;font-size:24px;line-height:1.2}
.stat small{color:var(--fg-soft);font-size:12px}
.hbar{display:flex;height:9px;border-radius:6px;overflow:hidden;border:1px solid var(--line);
background:var(--bg-soft);margin-bottom:26px}
.hseg{display:inline-block;height:100%}
.hseg.s-review{background:#e0a33a}.hseg.s-accepted{background:#3fa66b}
.hseg.s-maintained{background:#4b8cf0}.hseg.s-deprecated{background:#d46a6a}
.hseg.s-unknown{background:#b6bec8}
.hlegend{display:flex;gap:15px;flex-wrap:wrap;margin:-18px 0 0;font-size:12px;color:var(--fg-soft)}
.hlegend i{display:inline-block;width:9px;height:9px;border-radius:3px;margin-right:5px;
vertical-align:middle}
.banner{padding:12px 16px;border-radius:9px;font-size:13.5px;line-height:1.8}
.banner.warn{background:#fff4e0;border:1px solid #f5dfb5;color:#7a4e00}
.banner.info{background:var(--accent-soft);border:1px solid #cfe1fb;color:var(--accent)}
@media (prefers-color-scheme:dark){.banner.warn{background:#332711;border-color:#5c451c;color:#f0c674}}
.card{background:var(--panel);border:1px solid var(--line);border-radius:10px;
padding:22px 25px;margin:20px 0}
.card h2{font-size:19px;margin:0 0 5px;padding:0;border:0}
.card .sub{color:var(--fg-soft);font-size:12.5px;margin:0 0 16px;line-height:1.7}
.card .sub code{font-size:11.5px}
.htoolbar{display:flex;flex-direction:column;gap:10px;margin-bottom:6px}
.hsearch{padding:9px 13px;border:1px solid var(--line);border-radius:8px;background:var(--bg);
color:var(--fg);font-size:13.5px;outline:none}
.hsearch:focus{border-color:var(--accent);box-shadow:0 0 0 3px var(--accent-soft)}
.frow{display:flex;gap:9px;flex-wrap:wrap;align-items:flex-start}
.frow>span{font-size:12px;color:var(--fg-soft);min-width:48px;padding-top:7px}
.filters{display:flex;gap:7px;flex-wrap:wrap;flex:1}
.fbtn{padding:5px 11px;border:1px solid var(--line);background:var(--bg);border-radius:999px;
font-size:12px;cursor:pointer;color:var(--fg-soft);transition:.15s}
.fbtn:hover{border-color:var(--accent);color:var(--accent)}
.fbtn.active{background:var(--accent);border-color:var(--accent);color:#fff;font-weight:650}
.hgroup{margin-top:24px}
.hgroup h3{font-size:15px;margin:0 0 3px;display:flex;align-items:center;gap:9px;
padding:0;border:0}
.hgroup .hc-num{font-size:11px;color:var(--fg-soft);font-weight:500}
.hgroup .hc-gdesc{margin:0 0 12px;font-size:12.5px;color:var(--fg-soft)}
.hgrid{display:grid;grid-template-columns:repeat(auto-fill,minmax(266px,1fr));gap:11px}
.hcard{background:var(--bg);border:1px solid var(--line);border-radius:9px;padding:13px 15px;
display:flex;flex-direction:column;gap:5px;transition:.15s}
.hcard:hover{border-color:var(--accent);box-shadow:var(--shadow);transform:translateY(-1px)}
.hc-top{display:flex;align-items:center;gap:8px}
.hc-id{font-size:11px;font-weight:700;letter-spacing:.4px;color:var(--accent)}
.hc-title{display:block;font-size:14.5px;font-weight:650;line-height:1.45;margin:1px 0 4px;
color:var(--fg)}
.hc-title:hover{color:var(--accent)}
.hc-desc{font-size:12.5px;color:var(--fg-soft);line-height:1.6;flex:1;min-height:38px}
.hc-foot{display:flex;align-items:center;gap:7px;flex-wrap:wrap;border-top:1px solid var(--line);
padding-top:9px;margin-top:7px}
.hc-meta{font-size:11px;color:var(--fg-soft);margin-right:auto}
.hempty{text-align:center;color:var(--fg-soft);padding:32px 0;font-size:13.5px;display:none}
.hquick{display:grid;grid-template-columns:repeat(auto-fill,minmax(228px,1fr));gap:10px}
.hquick a{display:block;border:1px solid var(--line);border-radius:9px;padding:11px 14px;
background:var(--bg)}
.hquick a:hover{border-color:var(--accent);text-decoration:none}
.hquick b{display:block;font-size:13.5px;color:var(--accent)}
.hquick span{font-size:12px;color:var(--fg-soft)}
.hnote{font-size:12.5px;color:var(--fg-soft);line-height:1.9;border-top:1px solid var(--line);
padding-top:14px;margin-top:18px}
.hidden{display:none!important}
@media(max-width:760px){.stats{grid-template-columns:repeat(2,1fr)}
#content.home-active{padding:0 18px 80px}.home .hero h1{font-size:24px}}
"""

JS = r"""
const ALIASES = __ALIASES_JSON__;
const navItems = [...document.querySelectorAll('.nav-item')];
const docs = [...document.querySelectorAll('.doc')];
const main = document.getElementById('main');
const content = document.getElementById('content');

function decodeHashPart(s){
  try { return decodeURIComponent(s); } catch(e) { return s; }
}
function parseHash(){
  const h = decodeHashPart(location.hash.replace(/^#/, ''));
  let rest;
  if(h.startsWith('/')) rest = h.slice(1);          // current format: #/<slug>
  else if(h.startsWith('doc-')) rest = h.slice(4); // v0.1 format: #doc-<legacy-slug>
  else return null;
  const i = rest.indexOf('~');
  const raw = i < 0 ? rest : rest.slice(0, i);
  return {slug: ALIASES[raw] || raw, anchor: i < 0 ? '' : rest.slice(i + 1)};
}
function buildHash(slug, anchor){ return '#/' + slug + (anchor ? '~' + anchor : ''); }
function setHash(h, push){
  if(location.hash === h) return;
  try {
    // pushState does not emit hashchange, so no feedback loop. file:// origins
    // may reject it; fall back to a plain hash assignment in that case.
    if(push) history.pushState(null, '', h); else history.replaceState(null, '', h);
  } catch(e) { location.hash = h; }
}
function scrollTo(section, anchor){
  requestAnimationFrame(() => {
    if(anchor){
      const el = document.getElementById(anchor);
      if(el && section.contains(el)){ el.scrollIntoView({block:'start', behavior:'smooth'}); return; }
    }
    main.scrollTop = 0;
  });
}
function show(slug, anchor, writeHistory){
  const found = docs.find(d => d.dataset.slug === (ALIASES[slug] || slug)) || docs[0];
  docs.forEach(d => d.classList.toggle('active', d === found));
  content.classList.toggle('home-active', found.dataset.slug === 'home');
  navItems.forEach(n => n.classList.toggle('active', n.dataset.target === found.dataset.slug));
  renderMermaid(found);
  const n = navItems.find(x => x.dataset.target === found.dataset.slug);
  if(n && n.scrollIntoView) n.scrollIntoView({block:'nearest'});
  if(writeHistory) setHash(buildHash(found.dataset.slug, anchor || ''), true);
  scrollTo(found, anchor);
}
function route(){
  const p = parseHash();
  if(p) show(p.slug, p.anchor, false);
  else { show('home', '', false); setHash(buildHash('home',''), false); }
}
navItems.forEach(n => n.addEventListener('click', e => {
  e.preventDefault();
  show(n.dataset.target, '', true);
}));
// Delegated handler for every cross-document / heading link inside the content.
content.addEventListener('click', e => {
  const a = e.target.closest ? e.target.closest('a[href^="#/"]') : null;
  if(!a) return;
  e.preventDefault();
  const rest = decodeHashPart(a.getAttribute('href').slice(2));
  const i = rest.indexOf('~');
  show(i < 0 ? rest : rest.slice(0, i), i < 0 ? '' : rest.slice(i + 1), true);
});
document.getElementById('search').addEventListener('input', e => {
  const q = e.target.value.trim().toLowerCase();
  navItems.forEach(n => n.style.display = n.textContent.toLowerCase().includes(q) ? '' : 'none');
  document.querySelectorAll('.nav-group').forEach(g => {
    let sib = g.nextElementSibling, visible = false;
    while(sib && !sib.classList.contains('nav-group')){
      if(sib.classList.contains('nav-item') && sib.style.display !== 'none') visible = true;
      sib = sib.nextElementSibling;
    }
    g.style.display = visible ? '' : 'none';
  });
});
// ---- homepage dashboard: status / kind filters + free-text search ----
const homeCards = [...document.querySelectorAll('.hcard')];
const homeGroups = [...document.querySelectorAll('.hgroup')];
const homeSearch = document.getElementById('homeSearch');
const homeEmpty = document.getElementById('homeEmpty');
const homeFilter = {status:'all', kind:'all'};
function applyHomeFilter(){
  const q = ((homeSearch && homeSearch.value) || '').trim().toLowerCase();
  homeCards.forEach(c => {
    const okS = homeFilter.status === 'all' || c.dataset.status === homeFilter.status;
    const okK = homeFilter.kind === 'all' || c.dataset.kind === homeFilter.kind;
    const okQ = !q || (c.dataset.search || '').includes(q);
    c.classList.toggle('hidden', !(okS && okK && okQ));
  });
  homeGroups.forEach(g => {
    const any = [...g.querySelectorAll('.hcard')].some(c => !c.classList.contains('hidden'));
    g.classList.toggle('hidden', !any);
  });
  if(homeEmpty) homeEmpty.style.display = homeCards.some(c => !c.classList.contains('hidden')) ? 'none' : '';
}
document.querySelectorAll('#homeFilters .fbtn').forEach(b => b.addEventListener('click', () => {
  const dim = b.dataset.dim;
  document.querySelectorAll('#homeFilters .fbtn[data-dim="' + dim + '"]').forEach(x =>
    x.classList.toggle('active', x === b));
  homeFilter[dim] = b.dataset.f;
  applyHomeFilter();
}));
if(homeSearch) homeSearch.addEventListener('input', applyHomeFilter);

window.addEventListener('popstate', route);
window.addEventListener('hashchange', route);
async function renderMermaid(scope){
  if(!window.__mermaid) return;
  const nodes = [...scope.querySelectorAll('pre.mermaid:not([data-mm])')];
  if(!nodes.length) return;
  nodes.forEach(n => n.setAttribute('data-mm', '1'));
  try { await window.__mermaid.run({nodes}); } catch(e) { console.warn('mermaid render failed', e); }
}
route();
"""

MERMAID_LOADER = """
<script type="module">
try{
  const m=(await import('https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.esm.min.mjs')).default;
  m.initialize({startOnLoad:false,securityLevel:'loose',theme:matchMedia('(prefers-color-scheme: dark)').matches?'dark':'default'});
  window.__mermaid=m;
  const active=document.querySelector('.doc.active');
  if(active){
    const nodes=[...active.querySelectorAll('pre.mermaid:not([data-mm])')];
    nodes.forEach(n=>n.setAttribute('data-mm','1'));
    await m.run({nodes});
  }
}catch(e){/* offline: diagram source stays readable */}
</script>
"""


def build() -> int:
    docs, lookup, idmaps, bodies, aliases = prepare()
    print(f"Found {len(docs)} documents")
    errors = check_links(docs, lookup, idmaps)

    by_rel = {d.rel: d for d in docs}
    adr_docs = [d for d in docs if d.rel.startswith("docs/adr/") and d.rel != ADR_INDEX_REL]
    adr_index = by_rel[ADR_INDEX_REL]

    def navlink(d: Doc, num: str, label: str) -> str:
        return (f'<a class="nav-item" data-target="{d.slug}" href="#/{d.slug}">'
                f'<span class="num">{num}</span>{html.escape(label)}</a>')

    nav: list[str] = [
        f'<a class="nav-item nav-home" data-target="{HOME_SLUG}" href="#/{HOME_SLUG}">'
        f'<span class="num">⌂</span>首页 · 状态总览</a>'
    ]
    root = by_rel.get(ROOT_README_REL)
    for idx, (group_name, names) in enumerate(GROUPS):
        nav.append(f'<div class="nav-group">{html.escape(group_name)}</div>')
        if idx == 0 and root:
            nav.append(navlink(root, "00", "项目 README（总入口）"))
        for name in names:
            d = by_rel.get(group_rel(name))
            if not d:
                continue
            stem = posixpath.basename(group_rel(name))
            num = stem.split("-")[0] if stem[0].isdigit() else "IDX"
            label = re.sub(r"^\d+-\s*", "", d.title)
            nav.append(navlink(d, num, label))
    nav.append('<div class="nav-group">架构决策记录 ADR</div>')
    nav.append(navlink(adr_index, "IDX", "ADR 索引与演进时间线"))
    for d in adr_docs:
        num = d.rel.split("/")[-1].split("-")[1]
        label = re.sub(r"^ADR-\d+\s*", "", d.title)
        nav.append(navlink(d, num, label))

    # The homepage is a virtual section (no Markdown source) and is first, so
    # it is also the fallback when a hash points to an unknown slug.
    sections: list[str] = [build_home(docs, by_rel)]
    for d in docs:
        status = ""
        m = re.search(r"状态[：:]\s*([^｜|]+)", d.meta)
        if m:
            status = m.group(1).strip().replace("*", "")
        badge = f'<span class="badge">{html.escape(status)}</span>' if status else ""
        sections.append(
            f'<article class="doc" id="doc-{d.slug}" data-slug="{d.slug}">'
            f'<div class="doc-head"><h1>{html.escape(d.title)}</h1>'
            f'<div class="doc-meta">{badge}{html.escape(d.meta)}</div></div>'
            f'{bodies[d.slug]}'
            f'<div class="footnote">Universal Downloader 开发文档 · 由 tools/build_docs.py 生成于 {date.today().isoformat()} · '
            f'源文件 <code>{html.escape(d.rel)}</code></div></article>'
        )

    page = f"""<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Universal Downloader · 开发文档</title>
<style>{CSS}</style></head><body>
<div id="app">
<aside id="sidebar">
  <div class="brand"><h1>Universal Downloader</h1><p>开发文档 · 状态首页 + {len(docs)} 篇</p></div>
  <input id="search" placeholder="搜索文档标题…" autocomplete="off">
  <nav id="nav">{''.join(nav)}</nav>
</aside>
<main id="main"><div id="content">{''.join(sections)}</div></main>
</div>
<script>{JS}</script>
{MERMAID_LOADER}
</body></html>"""
    page = page.replace("__ALIASES_JSON__",
                        json.dumps(aliases, ensure_ascii=False).replace("<", "\\u003c"))

    errors += validate_html(page, docs)
    SITE.mkdir(parents=True, exist_ok=True)
    OUT.write_text(page, encoding="utf-8")
    print(f"Wrote {OUT.relative_to(ROOT)} ({len(page):,} chars)")
    print(f"Legacy slug aliases: {len(aliases)}")
    print(f"Validation: {errors} error(s)")
    return errors


if __name__ == "__main__":
    docs, lookup, idmaps, bodies, aliases = prepare()
    if "--check" in sys.argv:
        errs = check_links(docs, lookup, idmaps)
        if OUT.exists():
            errs += validate_html(OUT.read_text(encoding="utf-8"), docs)
        print(f"Found {len(docs)} documents; validation: {errs} error(s)")
        sys.exit(1 if errs else 0)
    sys.exit(1 if build() else 0)
