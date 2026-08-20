#!/usr/bin/env python3
"""
Stream Validator & Health Checker
Probes streams concurrently and reports status.
By default, exempts Portuguese trusted streams (M3UPT) from failing CI,
or runs against all with `--all`.
"""

import sys
import os
import argparse
import urllib.request
import ssl
from concurrent.futures import ThreadPoolExecutor, as_completed
import yaml
import glob

# Insecure SSL context for servers with self-signed / incomplete CA chains
INSECURE_CTX = ssl.create_default_context()
INSECURE_CTX.check_hostname = False
INSECURE_CTX.verify_mode = ssl.CERT_NONE

DEFAULT_HEADERS = {
    "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.3"
}

def check_channel(ch, timeout=6):
    name = ch.get("name", "Unknown")
    url = ch.get("url", "").strip()
    group = ch.get("group", "")
    
    if not url or url.startswith("#"):
        return {"name": name, "group": group, "status": "SKIP", "code": 0, "error": "Commented or empty"}
        
    headers = dict(DEFAULT_HEADERS)
    if "http_user_agent" in ch and ch["http_user_agent"]:
        headers["User-Agent"] = ch["http_user_agent"]
    if "http_referrer" in ch and ch["http_referrer"]:
        headers["Referer"] = ch["http_referrer"]
    if "http_origin" in ch and ch["http_origin"]:
        headers["Origin"] = ch["http_origin"]
        
    try:
        req = urllib.request.Request(url, headers=headers)
        # Try HEAD first or GET range
        resp = urllib.request.urlopen(req, timeout=timeout, context=INSECURE_CTX)
        code = resp.getcode()
        return {"name": name, "group": group, "status": "OK" if code < 400 else "FAIL", "code": code, "error": None}
    except urllib.error.HTTPError as e:
        return {"name": name, "group": group, "status": "FAIL", "code": e.code, "error": str(e)}
    except Exception as e:
        return {"name": name, "group": group, "status": "FAIL", "code": 0, "error": str(e)}

def main():
    parser = argparse.ArgumentParser(description="Validate IPTV/Radio streams")
    parser.add_argument("--all", action="store_true", help="Check all streams including PT geoblocked")
    parser.add_argument("--group", type=str, help="Filter by group name")
    parser.add_argument("--workers", type=int, default=16, help="Concurrent workers")
    args = parser.parse_args()
    
    root_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    data_dir = os.path.join(root_dir, "data")
    
    channels = []
    for yf in sorted(glob.glob(os.path.join(data_dir, "*.yaml"))):
        with open(yf, "r", encoding="utf-8") as f:
            data = yaml.safe_load(f)
            if isinstance(data, list):
                channels.extend(data)
                
    if args.group:
        channels = [c for c in channels if args.group.lower() in c.get("group", "").lower()]
    elif not args.all:
        # Exclude PT TV streams that require PT IP/geochecks from hard failure
        channels = [c for c in channels if c.get("group") not in ["Generalistas PT", "Notícias", "Desporto"]]
        
    print(f"Starting validation for {len(channels)} channels with {args.workers} workers...")
    
    results = {"OK": 0, "FAIL": 0, "SKIP": 0}
    failed_channels = []
    
    with ThreadPoolExecutor(max_workers=args.workers) as executor:
        futures = {executor.submit(check_channel, ch): ch for ch in channels}
        for future in as_completed(futures):
            res = future.result()
            st = res["status"]
            results[st] += 1
            if st == "OK":
                print(f"  [✓ OK {res['code']}] {res['group']} | {res['name']}")
            elif st == "SKIP":
                print(f"  [- SKIP] {res['group']} | {res['name']}")
            else:
                print(f"  [✗ FAIL {res['code']}] {res['group']} | {res['name']} -> {res['error']}")
                failed_channels.append(res)
                
    print("\n" + "="*50)
    print(f"Validation summary: {results['OK']} OK, {results['FAIL']} Failed, {results['SKIP']} Skipped out of {len(channels)}")
    print("="*50)
    
    if failed_channels:
        print("\nFailed streams:")
        for f in failed_channels:
            print(f"  - [{f['group']}] {f['name']}: {f['error']}")

if __name__ == "__main__":
    main()
