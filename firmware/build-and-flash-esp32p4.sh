#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
OUT_DIR="$SCRIPT_DIR/build-esp32p4"
PORT="${1:-/dev/ttyACM0}"

mkdir -p "$OUT_DIR"

echo "=== Building firmware (esp32p4) ==="
cd "$PROJECT_DIR"
docker compose build firmware-p4

echo "=== Extracting binaries ==="
docker rm -f fw-tmp 2>/dev/null || true
docker create --name fw-tmp "$(docker compose images firmware-p4 -q)" >/dev/null
docker cp fw-tmp:/firmware/build/suzuha-firmware.bin "$OUT_DIR/suzuha-firmware.bin"
docker cp fw-tmp:/firmware/build/bootloader/bootloader.bin "$OUT_DIR/bootloader.bin"
docker cp fw-tmp:/firmware/build/partition_table/partition-table.bin "$OUT_DIR/partition-table.bin"
docker rm fw-tmp >/dev/null

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
