#!/usr/bin/env python3
"""
Interactsh Manager Helper
Helper utility to start an interactsh-client session, obtain a unique payload,
and wait/poll for incoming interactions.
"""

import argparse
import json
import os
import shutil
import subprocess
import sys
import time

INTERACTSH_BIN = os.environ.get(
    "INTERACTSH_BIN",
    shutil.which("interactsh-client") or "/Users/adrian/go/bin/interactsh-client"
)

def run_session(session_file, payload_file, output_file, poll_interval=3):
    if not os.path.exists(INTERACTSH_BIN):
        sys.stderr.write(f"Error: interactsh-client binary not found at {INTERACTSH_BIN}\n")
        sys.exit(1)

    cmd = [
        INTERACTSH_BIN,
        "-sf", session_file,
        "-ps", "-psf", payload_file,
        "-json", "-o", output_file,
        "-pi", str(poll_interval)
    ]
    print(f"[*] Starting interactsh session: {' '.join(cmd)}")
    proc = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)

    # Wait for payload file to be generated
    for _ in range(20):
        if os.path.exists(payload_file) and os.path.getsize(payload_file) > 0:
            with open(payload_file, "r") as f:
                payload = f.read().strip()
            print(f"[+] Interactsh payload ready: {payload}")
            break
        time.sleep(0.5)

    return proc

def poll_interactions(output_file, follow=False):
    if not os.path.exists(output_file):
        print("[-] No interactions file yet.")
        return

    seen = set()
    try:
        while True:
            with open(output_file, "r") as f:
                for line in f:
                    line = line.strip()
                    if not line or line in seen:
                        continue
                    seen.add(line)
                    try:
                        record = json.loads(line)
                        proto = record.get("protocol", "unknown")
                        addr = record.get("remote-address", "")
                        qtype = record.get("q-type", "")
                        print(f"[!] Hit: [{proto.upper()}] from {addr} (q-type: {qtype})")
                    except json.JSONDecodeError:
                        print(f"[!] Hit: {line}")
            if not follow:
                break
            time.sleep(2)
    except KeyboardInterrupt:
        print("\n[*] Stopped polling.")

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Interactsh session manager")
    parser.add_argument("--start", action="store_true", help="Start listener")
    parser.add_argument("--poll", action="store_true", help="Poll output file")
    parser.add_argument("--follow", "-f", action="store_true", help="Continuously follow output")
    parser.add_argument("--session", default="/tmp/interactsh_session.json", help="Session file")
    parser.add_argument("--payload", default="/tmp/interactsh_payload.txt", help="Payload file")
    parser.add_argument("--output", default="/tmp/interactsh_interactions.jsonl", help="Output file")
    args = parser.parse_args()

    if args.start:
        p = run_session(args.session, args.payload, args.output)
        try:
            p.wait()
        except KeyboardInterrupt:
            p.terminate()
    elif args.poll or args.follow:
        poll_interactions(args.output, follow=args.follow)
    else:
        parser.print_help()
