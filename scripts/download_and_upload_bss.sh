#!/usr/bin/env bash
# ==============================================================================
# Baha'i Studio Sessions - Local Downloader & Remote Ingestion Uploader
# Run this script on your LOCAL machine where YouTube is not bot-blocked.
# ==============================================================================

set -e

DEST_HOST="vps2.kiefte.eu"
DEST_USER="joop"
DEST_PATH="/home/joop/iptv/downloads/bahai_sessions/"

WORK_DIR="$HOME/bss_downloads"
mkdir -p "${WORK_DIR}"
cd "${WORK_DIR}"

echo "=================================================="
echo "  1. DOWNLOADING STUDIO SESSIONS (LOCAL MACHINE)  "
echo "=================================================="

# Download all Studio Sessions in 720p HD with archive tracking
yt-dlp \
  -f "bestvideo[height<=720]+bestaudio/best[height<=720]" \
  --download-archive bss_archive.txt \
  -o "%(title)s [%(id)s].%(ext)s" \
  "https://www.youtube.com/playlist?list=PLcIp52lZ839q1-9c6Zq3R8UvI2rUj4kFh" || \
yt-dlp \
  -f "bestvideo[height<=720]+bestaudio/best[height<=720]" \
  --download-archive bss_archive.txt \
  -o "%(title)s [%(id)s].%(ext)s" \
  "https://www.youtube.com/@BahaiBlog/search?query=Baha%27i%20Blog%20Studio%20Sessions"

echo ""
echo "=================================================="
echo "  2. UPLOADING NEW SESSIONS TO VPS SERVER        "
echo "=================================================="

rsync -avz --progress \
  --include="*.mp4" --include="*.mkv" --include="*.webm" \
  --exclude="bss_archive.txt" \
  ./ "${DEST_USER}@${DEST_HOST}:${DEST_PATH}"

echo ""
echo "=================================================="
echo "  3. RUNNING INGESTION & AUTO-CLEANUP ON VPS      "
echo "=================================================="

ssh "${DEST_USER}@${DEST_HOST}" "python3 /home/joop/iptv/scripts/import_bahai_sessions.py --auto-clean"

echo ""
echo "✓ All Studio Sessions synced, encoded, and integrated into Bahá'í TV!"
