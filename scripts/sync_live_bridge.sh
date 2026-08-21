#!/usr/bin/env bash
# IPTV Live Bridge - One-Click Sync & Service Reload Script
# Usage: sudo ./scripts/sync_live_bridge.sh (or sudo ./sync.sh)

set -e

if [ "$EUID" -ne 0 ]; then
  echo "⚠️  Please run as root / with sudo:"
  echo "    sudo $0"
  exit 1
fi

echo "=================================================="
echo "  IPTV LIVE BRIDGE - SYNC & SERVICE RELOAD"
echo "=================================================="

PROJECT_DIR="/home/joop/iptv"
PKG_DIR="${PROJECT_DIR}/pkg/iptv-live-bridge"

echo "1. Building & Installing Arch Linux package (pacman)..."
cd "${PKG_DIR}"
if [ -n "$SUDO_USER" ]; then
  sudo -u "$SUDO_USER" makepkg -f --nodeps
else
  makepkg -f --nodeps
fi

PKG_FILE=$(ls -1t iptv-live-bridge-*.pkg.tar.zst 2>/dev/null | head -n 1)
if [ -n "$PKG_FILE" ]; then
  pacman -U --noconfirm "$PKG_FILE"
else
  echo "⚠️ Warning: Package file not found, copying binary directly."
  cp -f "${PKG_DIR}/iptv-live-bridge.py" /usr/bin/iptv-live-bridge
  chmod 755 /usr/bin/iptv-live-bridge
fi
cd "${PROJECT_DIR}"

echo "2. Ensuring media & distribution directories exist..."
mkdir -p /var/lib/iptv-live-bridge/dist
mkdir -p /var/lib/iptv-live-bridge/esperantotv
mkdir -p /var/lib/iptv-live-bridge/bahaitv

echo "3. Syncing master playlists & EPG distribution files..."
if [ -d "${PROJECT_DIR}/dist" ]; then
  rsync -a "${PROJECT_DIR}/dist/" /var/lib/iptv-live-bridge/dist/
fi

echo "4. Syncing Esperanto TV media library..."
if [ -d "${PKG_DIR}/esperantotv" ]; then
  rsync -a --delete "${PKG_DIR}/esperantotv/" /var/lib/iptv-live-bridge/esperantotv/
fi

echo "4. Syncing Bahá'í Studio Sessions media library..."
if [ -d "${PKG_DIR}/bahaitv" ]; then
  rsync -a --delete "${PKG_DIR}/bahaitv/" /var/lib/iptv-live-bridge/bahaitv/
fi

echo "5. Applying strict permissions..."
chmod -R 755 /var/lib/iptv-live-bridge /usr/share/iptv-live-bridge

echo "6. Reloading systemd & restarting iptv-live-bridge.service..."
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
