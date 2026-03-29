#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
OUT_DIR="$SCRIPT_DIR/build-esp32p4"
PORT="${1:-/dev/ttyACM0}"

if [ ! -f "$OUT_DIR/suzuha-firmware.bin" ]; then
  echo "Error: No firmware binary found. Run build-and-flash-esp32p4.sh first."
  exit 1
fi

echo "=== Fixing serial port permissions ==="
sudo chmod 666 "$PORT"

echo "=== Flashing to $PORT ==="
python3 -m esptool \
  -c esp32p4 \
  -p "$PORT" \
  -b 460800 \
  --before default-reset \
  -a hard-reset \
  write-flash \
  --flash-mode dio \
  --flash-size 16MB \
  --flash-freq 80m \
  0x2000 "$OUT_DIR/bootloader.bin" \
  0x8000 "$OUT_DIR/partition-table.bin" \
  0x10000 "$OUT_DIR/suzuha-firmware.bin"

echo "=== Done ==="
