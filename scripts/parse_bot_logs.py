#!/usr/bin/env python3
"""Parse antispam bot logs (Telegram JSON export) and extract rules + spam corpus.

Usage:
    # Extract rules as SA .cf file + URL map
    python3 parse_bot_logs.py /path/to/result.json --rules rspamd/seed-maps/spam

    # Extract spam samples for neural training
    python3 parse_bot_logs.py /path/to/result.json --spam spam.txt

    # Extract both + show stats
    python3 parse_bot_logs.py /path/to/result.json --rules rspamd/seed-maps/spam --spam spam.txt --stats

    # Sample spam corpus
    python3 parse_bot_logs.py /path/to/result.json --spam spam.txt --sample 2000
"""

import argparse
import json
import re
import sys
from collections import defaultdict


def get_text(msg):
    """Extract plain text from a Telegram message object."""
    t = msg.get("text", "")
    if isinstance(t, list):
        parts = []
        for p in t:
            if isinstance(p, str):
                parts.append(p)
            elif isinstance(p, dict):
                parts.append(p.get("text", ""))
        return "".join(parts)
    return t


def extract_rules(messages):
    """Extract regexp rules from bot rule list messages."""
    rules = []
    seen = set()

    rule_re = re.compile(
        r"\d+\[вкл\]:\s*'?(.+?)'?\s*\|\s*(delete|kick|ban|mute)",
        re.IGNORECASE,
    )

    for m in messages:
        text = get_text(m)
        for line in text.split("\n"):
            match = rule_re.match(line.strip())
            if not match:
                continue
            pattern = match.group(1).strip()
            action = match.group(2).strip().lower()

            if len(pattern) <= 3:
                continue
            if re.match(r"^[\d|]+$", pattern):
                continue
            if pattern.startswith("(?#"):
                comment_end = pattern.find(")")
                if comment_end > 0:
                    pattern = pattern[comment_end + 1:]
                    if not pattern:
                        continue

            if pattern not in seen:
                seen.add(pattern)
                rules.append({"pattern": pattern, "action": action})

    return rules


def extract_spam_samples(messages, bot_names=None):
    """Extract spam samples from forwarded messages and bot reports."""
    if bot_names is None:
        bot_names = set()

    spam_texts = []
    seen = set()

    for m in messages:
        if m.get("type") != "message":
            continue

        text = get_text(m)
        clean = " ".join(text.split())

        if len(clean) < 30:
            continue

        text_no_urls = re.sub(r"https?://\S+|t\.me/\S+", "", clean)
        if len(text_no_urls.split()) < 4:
            continue

        fwd = m.get("forwarded_from", "")
        if fwd and fwd not in bot_names:
            if clean not in seen:
                seen.add(clean)
                spam_texts.append(clean)

    return spam_texts


# Category detection for SA symbol naming
CATEGORY_MAP = [
    (["крипт", "crypto", "bitcoin", "binance", "биржа", "арбитр",
      "трейдинг", "forex", "mining", "airdrop", "токен"],
     "CRYPTO", "Crypto/trading spam"),
    (["наркот", "меф", "гаш", "закладк", "дропы", "курьер"],
     "DRUGS", "Drug-related spam"),
    (["заработ", "доход", "подработ", "удаленн", "profit", "invest", "инвест"],
     "INCOME", "Income/job spam"),
    (["whatsapp", "в лс", "в личку", "в личные", "пиши"],
     "DM", "Direct message lure"),
    (["casino", "казино", "ставк"],
     "GAMBLING", "Gambling spam"),
]

SPAM_INDICATORS = [
    "заработ", "крипт", "биржа", "binance", "арбитр", "трейдинг",
    "инвест", "profit", "casino", "crypto", "forex", "bitcoin",
    "дропы", "курьер", "закладк", "наркот", "mining", "airdrop",
    "whatsapp", "telegram", "удаленн", "подработ", "доход",
    "stoplamers", "стопламерс", "в лс", "в личку", "токен",
    "казино", "ставк",
]


def sanitize_for_hyperscan(pattern):
    """Rewrite PCRE patterns to be Hyperscan/PCRE2-compatible.

    Fixes:
    - \\uXXXX / \\UXXXXXXXX → actual UTF-8 characters (PCRE2 doesn't support \\u)
    - Back-references (\\1, \\2) → inline the captured group
    - Subpattern references (?N) → inline the group
    - Possessive quantifiers (.++) → greedy (.+)
    - \\b adjacent to Cyrillic → remove (broken in Hyperscan)
    - Surrounding quotes → strip
    - Unbalanced brackets → skip
    """
    p = pattern.strip()

    # Strip surrounding quotes
    if (p.startswith('"') and p.endswith('"')) or (p.startswith("'") and p.endswith("'")):
        p = p[1:-1]

    # Convert \uXXXX and \UXXXXXXXX to actual UTF-8 characters
    # Handle surrogate pairs: \ud83c\udXXX → single codepoint
    def replace_surrogates(m):
        hi = int(m.group(1), 16)
        lo = int(m.group(2), 16)
        try:
            cp = 0x10000 + (hi - 0xD800) * 0x400 + (lo - 0xDC00)
            return chr(cp)
        except (ValueError, OverflowError):
            return m.group(0)

    p = re.sub(r'\\[Uu]([dD][89aAbB][0-9a-fA-F]{2})\\[Uu]([dD][cCdDeEfF][0-9a-fA-F]{2})',
               replace_surrogates, p)

    def replace_u_escape(m):
        cp = int(m.group(1), 16)
        # Skip lone surrogates
        if 0xD800 <= cp <= 0xDFFF:
            return None  # signal to skip this pattern
        try:
            return chr(cp)
        except (ValueError, OverflowError):
            return m.group(0)

    # Track if we hit a lone surrogate
    has_bad_surrogate = [False]
    def safe_replace_u(m):
        result = replace_u_escape(m)
        if result is None:
            has_bad_surrogate[0] = True
            return m.group(0)
        return result

    p = re.sub(r'\\[Uu]([0-9a-fA-F]{4,8})', safe_replace_u, p)
    if has_bad_surrogate[0]:
        return None

    # Replace possessive quantifiers: .++ → .+
    p = re.sub(r'\+\+', '+', p)

    # Remove \b adjacent to non-ASCII (Hyperscan can't handle it)
    p = re.sub(r'\\b(?=[^\x00-\x7f(])', '', p)
    p = re.sub(r'(?<=[^\x00-\x7f)])\\b', '', p)
    # Also remove \b inside \b...\b around non-ASCII content
    # Pattern like: \bкириллица\b → кириллица
    p = re.sub(r'\\b([^\x00-\x7f][^\\]*)\\b', r'\1', p)

    # Check balanced parentheses/brackets
    paren_depth = 0
    bracket_depth = 0
    in_bracket = False
    for i, ch in enumerate(p):
        escaped = i > 0 and p[i-1] == '\\'
        if escaped:
            continue
        if ch == '[' and not in_bracket:
            bracket_depth += 1
            in_bracket = True
        elif ch == ']' and in_bracket:
            bracket_depth -= 1
            in_bracket = False
        elif ch == '(' and not in_bracket:
            paren_depth += 1
        elif ch == ')' and not in_bracket:
            paren_depth -= 1
        if paren_depth < 0:
            return None
    if paren_depth != 0 or bracket_depth != 0:
        return None

    # Handle back-references: find capture groups, then inline
    groups = {}
    group_idx = 0
    depth = 0
    group_starts = {}
    i = 0
    while i < len(p):
        if p[i] == '(' and (i == 0 or p[i-1] != '\\'):
            if i + 1 < len(p) and p[i+1] == '?':
                depth += 1
            else:
                group_idx += 1
                depth += 1
                group_starts[depth] = (group_idx, i + 1)
        elif p[i] == ')' and (i == 0 or p[i-1] != '\\'):
            if depth in group_starts:
                gidx, start = group_starts[depth]
                groups[gidx] = p[start:i]
                del group_starts[depth]
            depth -= 1
            if depth < 0:
                depth = 0
        i += 1

    # Replace \1, \2 etc with the captured group content
    def replace_backref(m):
        idx = int(m.group(1))
        if idx in groups:
            return groups[idx]
        return m.group(0)

    p = re.sub(r'\\(\d)', replace_backref, p)

    # Replace (?N) subpattern references
    def replace_subpattern(m):
        idx = int(m.group(1))
        if idx in groups:
            return f'(?:{groups[idx]})'
        return m.group(0)

    for _ in range(3):
        new_p = re.sub(r'\(\?(\d+)\)', replace_subpattern, p)
        if new_p == p:
            break
        p = new_p

    # If still has unresolved refs, skip
    if re.search(r'\\[1-9]|\(\?\d+\)', p):
        return None

    # Test validity
    try:
        re.compile(p)
    except re.error:
        return None

    # Remove all \b from patterns containing non-ASCII
    # Hyperscan can't handle \b with UTF-8 at all
    if re.search(r'[^\x00-\x7f]', p) and '\\b' in p:
        p = p.replace('\\b', '')

    # SA .cf format: # starts a comment, so patterns with literal # break parsing
    if '#' in p:
        return None

    return p


def classify_and_write(rules, output_prefix=None):
    """Classify rules and optionally write as SA regexp_rules .cf file + URL map."""
    regex_chars = re.compile(r"[*+?{}()\[\]\\|^$]")
    seen_names = set()
    sa_rules = []
    urls = []

    def make_name(pattern, idx):
        lower = pattern.lower()
        for keywords, category, _ in CATEGORY_MAP:
            if any(kw in lower for kw in keywords):
                base = f"TG_SPAM_{category}"
                name = base
                n = 1
                while name in seen_names:
                    n += 1
                    name = f"{base}_{n}"
                seen_names.add(name)
                return name
        name = f"TG_SPAM_RULE_{idx}"
        seen_names.add(name)
        return name

    def get_description(pattern):
        lower = pattern.lower()
        for keywords, _, desc in CATEGORY_MAP:
            if any(kw in lower for kw in keywords):
                return desc
        return "Extracted spam pattern"

    def get_score(action):
        if action == "ban":
            return 8.0
        elif action == "kick":
            return 6.0
        return 5.0

    idx = 0
    for r in rules:
        pattern = r["pattern"]
        action = r.get("action", "delete")

        if re.match(r"^[\d|()]+$", pattern):
            continue

        cleaned = re.sub(r"\(\?#[^)]*\)", "", pattern).strip()
        if not cleaned or len(cleaned) <= 3:
            continue

        # URL patterns → separate url map
        if re.match(r"^[\w.-]+\.(ru|com|org|net|me|io|xyz|top|cc|link|pro)\b", cleaned):
            urls.append(cleaned)
            continue

        is_regex = bool(regex_chars.search(cleaned))

        # Simple text — only keep if spam-related
        if not is_regex:
            lower = cleaned.lower()
            if not any(kw in lower for kw in SPAM_INDICATORS):
                continue

        # Sanitize regexp for Hyperscan compatibility
        if is_regex:
            sanitized = sanitize_for_hyperscan(cleaned)
            if sanitized is None:
                continue  # unfixable pattern
            cleaned = sanitized

        idx += 1
        name = make_name(cleaned, idx)
        score = get_score(action)
        desc = get_description(cleaned)

        # Convert to /pattern/flags format
        # Add /u flag for patterns containing non-ASCII (Cyrillic, emoji, etc.)
        has_unicode = bool(re.search(r'[^\x00-\x7f]', cleaned))
        flags = "iu" if has_unicode else "i"

        if is_regex:
            if not cleaned.startswith("/"):
                escaped = cleaned.replace("/", r"\/")
                pat_formatted = f"/{escaped}/{flags}"
            else:
                pat_formatted = cleaned
        else:
            escaped = re.escape(cleaned).replace("/", r"\/")
            pat_formatted = f"/{escaped}/{flags}"

        sa_rules.append({
            "name": name,
            "pattern": pat_formatted,
            "score": score,
            "description": desc,
        })

    counts = {}

    if output_prefix is None:
        return counts, sa_rules, urls

    # Write SA .cf file
    cf_path = f"{output_prefix}_rules.cf"
    with open(cf_path, "w", encoding="utf-8") as out:
        out.write("# Auto-generated spam rules (SA regexp_rules format)\n")
        out.write("# Source: antispam bot rule export\n\n")
        for r in sa_rules:
            out.write(f"body {r['name']} {r['pattern']}\n")
            out.write(f"score {r['name']} {r['score']}\n")
            out.write(f"describe {r['name']} {r['description']}\n\n")
    counts["rules"] = len(sa_rules)

    # Write URL map
    if urls:
        url_path = f"{output_prefix}_urls.map"
        with open(url_path, "w", encoding="utf-8") as out:
            out.write("# Spam URLs/domains\n")
            for u in urls:
                out.write(u + "\n")
        counts["urls"] = len(urls)

    return counts, sa_rules, urls


def write_spam_corpus(texts, output_path, sample_size=None, seed=None):
    """Write spam texts as training data (one per line)."""
    import random

    if sample_size and sample_size < len(texts):
        total = len(texts)
        if seed is not None:
            random.seed(seed)
        texts = random.sample(texts, sample_size)
        print(f"  Sampled {sample_size} from {total} spam samples", file=sys.stderr)

    with open(output_path, "w", encoding="utf-8") as out:
        for text in texts:
            out.write(text + "\n")

    return len(texts)


def print_stats(messages, rules, spam_texts, sa_rules, urls):
    """Print summary statistics."""
    print(f"\nTotal messages in log: {len(messages)}")
    print(f"Raw rules extracted: {len(rules)}")
    print(f"Spam samples extracted: {len(spam_texts)}")

    print(f"\nSA rules generated: {len(sa_rules)}")
    print(f"URL rules: {len(urls)}")
    print(f"Skipped: {len(rules) - len(sa_rules) - len(urls)}")

    # Category breakdown
    cats = defaultdict(int)
    for r in sa_rules:
        cat = r["name"].replace("TG_SPAM_", "").split("_")[0]
        cats[cat] += 1
    print(f"\nRules by category:")
    for cat, count in sorted(cats.items(), key=lambda x: -x[1]):
        print(f"  {cat}: {count}")

    print(f"\nSample rules:")
    for r in sa_rules[:10]:
        print(f"  {r['name']} ({r['score']}) {r['pattern'][:60]}")


def main():
    parser = argparse.ArgumentParser(description="Parse antispam bot logs")
    parser.add_argument("input", help="Path to result.json (Telegram JSON export)")
    parser.add_argument("--rules", "-r",
                        help="Output prefix for rspamd files (creates {prefix}_rules.cf, _urls.map)")
    parser.add_argument("--spam", "-s", help="Output spam samples (one per line)")
    parser.add_argument("--stats", action="store_true", help="Show statistics")
    parser.add_argument("--sample", "-n", type=int, help="Sample N spam messages")
    parser.add_argument("--seed", type=int, default=None, help="Random seed")
    parser.add_argument(
        "--bot-names",
        default="Linux Help Bot,FisHlaBsoMAN",
        help="Comma-separated bot/admin names to exclude from spam samples",
    )
    args = parser.parse_args()

    if not any([args.rules, args.spam, args.stats]):
        parser.error("Specify at least one of: --rules, --spam, --stats")

    print(f"Loading {args.input}...", file=sys.stderr)
    with open(args.input, encoding="utf-8") as f:
        data = json.load(f)

    messages = data.get("messages", [])
    print(f"Loaded {len(messages)} messages", file=sys.stderr)

    bot_names = set(n.strip() for n in args.bot_names.split(","))

    rules = extract_rules(messages)
    spam_texts = extract_spam_samples(messages, bot_names)

    sa_rules = []
    urls = []
    if args.rules:
        counts, sa_rules, urls = classify_and_write(rules, args.rules)
        if args.rules:
            for kind, count in counts.items():
                suffix = "_rules.cf" if kind == "rules" else f"_{kind}.map"
                print(f"Wrote {count} {kind} to {args.rules}{suffix}", file=sys.stderr)

    if args.stats:
        if not sa_rules and not urls:
            _, sa_rules, urls = classify_and_write(rules, None)
        print_stats(messages, rules, spam_texts, sa_rules, urls)

    if args.spam:
        count = write_spam_corpus(spam_texts, args.spam, args.sample, args.seed)
        print(f"Wrote {count} spam samples to {args.spam}", file=sys.stderr)


if __name__ == "__main__":
    main()
