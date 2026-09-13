#!/usr/bin/env python3
"""
Safely configures https://kiefte.eu/iptv/* subpath in /etc/caddy/Caddyfile
Proxying to iptv-live-bridge on 127.0.0.1:7555 while preserving all existing routes.
"""

import os
import sys
import shutil
import subprocess

CADDYFILE = "/etc/caddy/Caddyfile"
BACKUP = "/etc/caddy/Caddyfile.bak"

if os.geteuid() != 0:
    print("Error: This script must be run as root (e.g. sudo python3 scripts/update_caddyfile.py)")
    sys.exit(1)

if not os.path.exists(CADDYFILE):
    print(f"Error: {CADDYFILE} does not exist.")
    sys.exit(1)

with open(CADDYFILE, "r", encoding="utf-8") as f:
    content = f.read()

# Backup
shutil.copy2(CADDYFILE, BACKUP)
print(f"Created backup at {BACKUP}")

def get_configured_bridge_addr():
    if len(sys.argv) > 1 and not sys.argv[1].startswith("-"):
        return sys.argv[1]
    if os.environ.get("BRIDGE_ADDR"):
        return os.environ["BRIDGE_ADDR"]
    host = os.environ.get("BRIDGE_HOST")
    port = os.environ.get("BRIDGE_PORT")
    if host and port:
        if ":" in host and not host.startswith("["):
            host = f"[{host}]"
        return f"{host}:{port}"
    for conf_path in [
        "/etc/iptv-live-bridge.conf",
        os.path.join(os.path.dirname(__file__), "../pkg/iptv-live-bridge/iptv-live-bridge.conf")
    ]:
        if os.path.exists(conf_path):
            try:
                conf = {}
                with open(conf_path, "r", encoding="utf-8") as f:
                    for line in f:
                        line = line.strip()
                        if line and not line.startswith("#") and "=" in line:
                            k, v = line.split("=", 1)
                            conf[k.strip()] = v.strip().strip("\"'")
                if "BRIDGE_ADDR" in conf:
                    return conf["BRIDGE_ADDR"]
                if "BRIDGE_HOST" in conf and "BRIDGE_PORT" in conf:
                    h = conf["BRIDGE_HOST"]
                    p = conf["BRIDGE_PORT"]
                    if ":" in h and not h.startswith("["):
                        h = f"[{h}]"
                    return f"{h}:{p}"
            except Exception:
                pass
    return "[fd00:2830::7555]:8080"

bridge_addr = get_configured_bridge_addr()
print(f"Targeting IPTV bridge address: {bridge_addr}")

import re
if "handle_path /iptv/*" in content:
    # Update existing reverse_proxy line inside handle_path /iptv/*
    content = re.sub(
        r"(handle_path /iptv/\*\s*\{\s*reverse_proxy\s+)[^\n\s\}]+",
        r"\g<1>" + bridge_addr,
        content
    )
    print(f"Updated existing /iptv/* reverse_proxy target to {bridge_addr}")
else:
    # Insert new block
    content = re.sub(
        r"(kiefte\.eu,\s*www\.kiefte\.eu[^{]*\{\s*)(redir[^\n\}]+)(\s*\})",
        f"\\1handle_path /iptv/* {{\\n                reverse_proxy {bridge_addr}\\n        }}\\n        handle {{\\n                \\2\\n        }}\\3",
        content
    )
    print(f"Inserted /iptv/* block into {CADDYFILE}")

with open(CADDYFILE, "w", encoding="utf-8") as f:
    f.write(content)

# Validate Caddy configuration
print("\nValidating Caddy configuration...")
val = subprocess.run(["caddy", "validate", "--config", CADDYFILE], capture_output=True, text=True)
if val.returncode != 0:
    print(f"✗ Validation failed:\n{val.stderr}")
    print(f"Restoring backup from {BACKUP}...")
    shutil.copy2(BACKUP, CADDYFILE)
    sys.exit(1)

print("✓ Caddy configuration is valid!")

# Reload Caddy
print("\nReloading Caddy service...")
rel = subprocess.run(["systemctl", "reload", "caddy"], capture_output=True, text=True)
if rel.returncode == 0:
    print("✓ Caddy reloaded successfully!")
    print("\nTest endpoint: https://kiefte.eu/iptv/health")
else:
    print(f"Note: Could not reload systemd caddy: {rel.stderr}")
