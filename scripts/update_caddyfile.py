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

old_block = """kiefte.eu, www.kiefte.eu, joop.kiefte.eu, joop.kiefte.nom.br {
        redir * https://bsky.app/profile/joop.kiefte.eu
}"""

new_block = """kiefte.eu, www.kiefte.eu, joop.kiefte.eu, joop.kiefte.nom.br {
        handle_path /iptv/* {
                reverse_proxy 127.0.0.1:7555
        }
        handle {
                redir https://bsky.app/profile/joop.kiefte.eu
        }
}"""

if "handle_path /iptv/*" in content:
    print("Caddyfile is already configured for /iptv/*.")
else:
    # Normalize whitespaces for replacement
    if old_block in content:
        content = content.replace(old_block, new_block)
    else:
        # Fallback replacement matching the domain line
        import re
        content = re.sub(
            r"(kiefte\.eu,\s*www\.kiefte\.eu[^{]*\{\s*)(redir[^\n\}]+)(\s*\})",
            r"\1handle_path /iptv/* {\n                reverse_proxy 127.0.0.1:7555\n        }\n        handle {\n                \2\n        }\3",
            content
        )

    with open(CADDYFILE, "w", encoding="utf-8") as f:
        f.write(content)
    print(f"Updated {CADDYFILE}")

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
