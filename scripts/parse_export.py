#!/usr/bin/env python3
"""Parse Telegram Desktop HTML export and extract messages.

Usage:
    # Extract all messages as training data (one per line)
    python3 parse_export.py /path/to/ChatExport/ --output messages.txt

    # Extract only messages from specific users (potential spam)
    python3 parse_export.py /path/to/ChatExport/ --output spam.txt --users "CryptoBot,SpamUser"

    # Generate Redis commands for user profiles
    python3 parse_export.py /path/to/ChatExport/ --profiles profiles.redis

    # Show per-user stats
    python3 parse_export.py /path/to/ChatExport/ --stats
"""

import argparse
import glob
import json
import os
import re
import sys
from collections import defaultdict
from datetime import datetime

from bs4 import BeautifulSoup


def parse_html_file(path):
    """Parse a single messages*.html file and yield messages."""
    with open(path, encoding="utf-8") as f:
        soup = BeautifulSoup(f, "html.parser")

    current_from = None
    for msg_div in soup.find_all("div", class_=re.compile(r"message default")):
        # "joined" messages reuse the previous sender
        from_div = msg_div.find("div", class_="from_name")
        if from_div:
            current_from = from_div.get_text(strip=True)

        text_div = msg_div.find("div", class_="text")
        if not text_div:
            continue

        text = text_div.get_text(separator=" ", strip=True)
        if not text:
            continue

        # Parse date
        date_div = msg_div.find("div", class_="pull_right date details")
        date_str = ""
        timestamp = 0
        if date_div and date_div.get("title"):
            date_str = date_div["title"]
            # Format: "31.03.2017 15:53:19 UTC+00:00"
            try:
                dt = datetime.strptime(date_str[:19], "%d.%m.%Y %H:%M:%S")
                timestamp = int(dt.timestamp())
            except ValueError:
                pass

        # Get message ID
        msg_id = msg_div.get("id", "")
        msg_num = re.search(r"\d+", msg_id)
        msg_num = int(msg_num.group()) if msg_num else 0

        # Check for reply
        reply_div = msg_div.find("div", class_="reply_to details")
        reply_to = None
        if reply_div:
            reply_link = reply_div.find("a")
            if reply_link and reply_link.get("href"):
                reply_match = re.search(r"message(\d+)", reply_link["href"])
                if reply_match:
                    reply_to = int(reply_match.group(1))

        yield {
            "from": current_from or "Unknown",
            "text": text,
            "date": date_str,
            "timestamp": timestamp,
            "msg_id": msg_num,
            "reply_to": reply_to,
        }


def parse_export(export_dir):
    """Parse all message files from a Telegram export directory."""
    # Find all messages*.html files, sort by number
    pattern = os.path.join(export_dir, "messages*.html")
    files = sorted(glob.glob(pattern), key=lambda f: (
        int(re.search(r"(\d+)", os.path.basename(f)).group())
        if re.search(r"\d+", os.path.basename(f))
        else 0
    ))

    if not files:
        print(f"No messages*.html files found in {export_dir}", file=sys.stderr)
        sys.exit(1)

    messages = []
    for f in files:
        for msg in parse_html_file(f):
            messages.append(msg)
        print(f"  Parsed {f}: {len(messages)} total", file=sys.stderr)

    return messages


def write_training_data(messages, output_path, users_filter=None, min_length=10, min_words=4, sample_size=None, fmt="text", seed=None):
    """Write messages as training data.

    Formats:
      text  — one message per line, newlines within messages collapsed to spaces.
              Compatible with deploy.sh train.
      jsonl — JSON Lines with from, text (original newlines preserved), date fields.

    If sample_size is set, randomly sample that many messages from the
    filtered set (uniform random, reproducible with fixed seed).
    """
    import random
    import re

    url_re = re.compile(r"https?://\S+|t\.me/\S+", re.IGNORECASE)

    # First pass: collect eligible messages
    eligible = []
    for msg in messages:
        if users_filter and msg["from"] not in users_filter:
            continue
        clean = " ".join(msg["text"].split())
        if len(clean) < min_length:
            continue
        # Count real words (strip URLs, then count)
        text_no_urls = url_re.sub("", clean).strip()
        word_count = len(text_no_urls.split())
        if word_count < min_words:
            continue
        eligible.append(msg if fmt == "jsonl" else clean)

    # Sample if requested
    if sample_size and sample_size < len(eligible):
        total = len(eligible)
        if seed is not None:
            random.seed(seed)
        eligible = random.sample(eligible, sample_size)
        seed_info = f", seed={seed}" if seed is not None else ""
        print(f"  Sampled {sample_size} from {total} eligible messages{seed_info}", file=sys.stderr)

    with open(output_path, "w", encoding="utf-8") as out:
        for item in eligible:
            if fmt == "jsonl":
                out.write(json.dumps({
                    "from": item["from"],
                    "text": item["text"],
                    "date": item["date"],
                }, ensure_ascii=False) + "\n")
            else:
                out.write(item + "\n")

    return len(eligible)


def write_profiles(messages, output_path):
    """Generate Redis protocol commands to seed user profiles."""
    profiles = defaultdict(lambda: {
        "first_seen": float("inf"),
        "last_seen": 0,
        "msg_count": 0,
        "username": "",
        "first_name": "",
        "messages": [],
        "contacts": set(),
    })

    # Index msg_id -> from for reply tracking
    msg_authors = {}
    for msg in messages:
        msg_authors[msg["msg_id"]] = msg["from"]

    for msg in messages:
        name = msg["from"]
        p = profiles[name]
        ts = msg["timestamp"]
        if ts > 0:
            p["first_seen"] = min(p["first_seen"], ts)
            p["last_seen"] = max(p["last_seen"], ts)
        p["msg_count"] += 1
        p["first_name"] = name

        text = " ".join(msg["text"].split())
        if len(text) > 200:
            text = text[:200]
        p["messages"].append(text)
        if len(p["messages"]) > 10:
            p["messages"] = p["messages"][-10:]

        # Track contacts from replies
        if msg["reply_to"] and msg["reply_to"] in msg_authors:
            replied_to = msg_authors[msg["reply_to"]]
            if replied_to != name:
                p["contacts"].add(replied_to)
                profiles[replied_to]["contacts"].add(name)

    with open(output_path, "w", encoding="utf-8") as out:
        for name, p in profiles.items():
            # Use name as key (we don't have real user IDs from HTML export)
            safe_name = name.replace(" ", "_").lower()
            key = f"tg_profile:name:{safe_name}"

            first_seen = int(p["first_seen"]) if p["first_seen"] != float("inf") else 0
            last_seen = int(p["last_seen"])

            out.write(
                f'HSET {key} first_seen {first_seen} last_seen {last_seen} '
                f'msg_count {p["msg_count"]} first_name "{name}" username ""\n'
            )
            out.write(f'EXPIRE {key} {90 * 86400}\n')

            # Messages
            msg_key = f"{key}:messages"
            for text in reversed(p["messages"]):
                safe_text = text.replace('"', '\\"')
                out.write(f'LPUSH {msg_key} "{safe_text}"\n')
            out.write(f'LTRIM {msg_key} 0 9\n')
            out.write(f'EXPIRE {msg_key} {90 * 86400}\n')

            # Contacts
            if p["contacts"]:
                contacts_key = f"{key}:contacts"
                members = " ".join(
                    f'"{c.replace(" ", "_").lower()}"' for c in p["contacts"]
                )
                out.write(f"SADD {contacts_key} {members}\n")
                out.write(f'EXPIRE {contacts_key} {90 * 86400}\n')

    return len(profiles)


def print_stats(messages):
    """Print per-user statistics."""
    stats = defaultdict(lambda: {"count": 0, "first": None, "last": None})
    for msg in messages:
        s = stats[msg["from"]]
        s["count"] += 1
        if msg["date"]:
            if not s["first"] or msg["date"] < s["first"]:
                s["first"] = msg["date"]
            if not s["last"] or msg["date"] > s["last"]:
                s["last"] = msg["date"]

    # Sort by message count descending
    sorted_users = sorted(stats.items(), key=lambda x: -x[1]["count"])

    print(f"\n{'User':<30} {'Messages':>8} {'First seen':<22} {'Last seen':<22}")
    print("-" * 85)
    for name, s in sorted_users:
        print(f"{name[:29]:<30} {s['count']:>8} {(s['first'] or '')[:21]:<22} {(s['last'] or '')[:21]:<22}")
    print(f"\nTotal: {len(messages)} messages from {len(stats)} users")


def main():
    parser = argparse.ArgumentParser(description="Parse Telegram HTML export")
    parser.add_argument("export_dir", help="Path to ChatExport directory")
    parser.add_argument("--output", "-o", help="Output training data file (one message per line)")
    parser.add_argument("--profiles", help="Output Redis commands for user profiles")
    parser.add_argument("--stats", action="store_true", help="Show per-user statistics")
    parser.add_argument("--users", help="Comma-separated list of usernames to filter (for training data)")
    parser.add_argument("--min-length", type=int, default=10, help="Minimum message length in chars (default: 10)")
    parser.add_argument("--min-words", type=int, default=4, help="Minimum word count (default: 4)")
    parser.add_argument("--sample", "-n", type=int, help="Randomly sample N messages from eligible set")
    parser.add_argument("--seed", type=int, default=None, help="Random seed for reproducible sampling (default: random)")
    parser.add_argument("--format", choices=["text", "jsonl"], default="text",
                        help="Output format: text (one msg per line, newlines collapsed) or jsonl (JSON Lines, preserves structure)")
    args = parser.parse_args()

    if not any([args.output, args.profiles, args.stats]):
        parser.error("Specify at least one of: --output, --profiles, --stats")

    print(f"Parsing export from {args.export_dir}...", file=sys.stderr)
    messages = parse_export(args.export_dir)
    print(f"Total: {len(messages)} messages parsed", file=sys.stderr)

    if args.stats:
        print_stats(messages)

    if args.output:
        users_filter = None
        if args.users:
            users_filter = set(u.strip() for u in args.users.split(","))
        count = write_training_data(messages, args.output, users_filter, args.min_length, args.min_words, args.sample, args.format, args.seed)
        print(f"Wrote {count} messages to {args.output}", file=sys.stderr)

    if args.profiles:
        count = write_profiles(messages, args.profiles)
        print(f"Wrote {count} user profiles to {args.profiles}", file=sys.stderr)


if __name__ == "__main__":
    main()
