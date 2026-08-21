#!/usr/bin/env bash
# IPTV Live Bridge - One-Click Sync & Service Reload Tool
# Installed to /usr/bin/iptv-live-bridge-sync

set -e

if [ "$EUID" -ne 0 ]; then
  echo "⚠️  Please run as root / with sudo:"
  echo "    sudo iptv-live-bridge-sync"
  exit 1
fi

echo "=================================================="
echo "  IPTV LIVE BRIDGE - SYNC & SERVICE RELOAD"
echo "=================================================="

PROJECT_DIR="/home/joop/iptv"
PKG_DIR="${PROJECT_DIR}/pkg/iptv-live-bridge"

echo "1. Ensuring live target directories exist..."
mkdir -p /var/lib/iptv-live-bridge/esperantotv
mkdir -p /var/lib/iptv-live-bridge/bahaitv
mkdir -p /usr/share/iptv-live-bridge/testcard
mkdir -p /usr/share/iptv-live-bridge/offline

echo "2. Updating bridge binary in /usr/bin/..."
if [ -f "${PKG_DIR}/iptv-live-bridge.py" ]; then
  cp -f "${PKG_DIR}/iptv-live-bridge.py" /usr/bin/iptv-live-bridge
  chmod 755 /usr/bin/iptv-live-bridge
fi

echo "3. Syncing testcards & station idents..."
if [ -d "${PKG_DIR}/testcard" ]; then
  rsync -a "${PKG_DIR}/testcard/" /usr/share/iptv-live-bridge/testcard/
fi

if [ -d "${PKG_DIR}/offline" ]; then
  rsync -a "${PKG_DIR}/offline/" /usr/share/iptv-live-bridge/offline/
fi

echo "4. Syncing Esperanto TV media library..."
if [ -d "${PKG_DIR}/esperantotv" ]; then
  rsync -a --delete "${PKG_DIR}/esperantotv/" /var/lib/iptv-live-bridge/esperantotv/
fi

echo "5. Syncing Bahá'í Studio Sessions media library..."
if [ -d "${PKG_DIR}/bahaitv" ]; then
  rsync -a --delete "${PKG_DIR}/bahaitv/" /var/lib/iptv-live-bridge/bahaitv/
fi

echo "6. Applying strict permissions..."
chmod -R 755 /var/lib/iptv-live-bridge /usr/share/iptv-live-bridge

echo "7. Reloading systemd & restarting iptv-live-bridge.service..."
systemctl daemon-reload
systemctl restart iptv-live-bridge

echo ""
echo "=================================================="
echo "  ✓ SYNC COMPLETE & SERVICE RUNNING!"
echo "=================================================="

ESP_COUNT=$(ls -1 /var/lib/iptv-live-bridge/esperantotv/*.ts 2>/dev/null | wc -l || echo 0)
BAH_COUNT=$(ls -1 /var/lib/iptv-live-bridge/bahaitv/*.ts 2>/dev/null | wc -l || echo 0)

ESP_MIN=$(echo "scale=1; $ESP_COUNT * 6.0 / 60" | bc 2>/dev/null || echo "0")
BAH_MIN=$(echo "scale=1; $BAH_COUNT * 6.0 / 60" | bc 2>/dev/null || echo "0")

echo "  💚 Esperanto TV : ${ESP_COUNT} segments (~${ESP_MIN} minutes)"
echo "  🕊️ Bahá'í TV   : ${BAH_COUNT} segments (~${BAH_MIN} minutes)"
echo "  📡 Live Stream  : https://kiefte.eu/iptv/esperanto/tv"
echo "  📡 Live Stream  : https://kiefte.eu/iptv/bahai/tv"
echo "=================================================="
