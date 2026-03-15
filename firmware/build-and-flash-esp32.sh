#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
OUT_DIR="$SCRIPT_DIR/build-esp32"

echo "=== Building firmware ==="
docker build -f "$SCRIPT_DIR/Dockerfile" --build-arg TARGET=esp32 -t suzuha-firmware-esp32 "$SCRIPT_DIR/"

echo "=== Extracting binaries ==="
docker rm -f fw-tmp 2>/dev/null || true
docker create --name fw-tmp suzuha-firmware-esp32
docker cp fw-tmp:/firmware/build/suzuha-firmware.bin "$OUT_DIR/suzuha-firmware.bin"
docker cp fw-tmp:/firmware/build/bootloader/bootloader.bin "$OUT_DIR/bootloader.bin"
docker cp fw-tmp:/firmware/build/partition_table/partition-table.bin "$OUT_DIR/partition-table.bin"
docker rm fw-tmp

echo "=== Flashing ==="
sudo $(which python) -m esptool \
  --chip esp32 \
  -p /dev/ttyUSB0 \
  -b 460800 \
  --before default-reset \
  --after hard-reset \
  write-flash \
  --flash-mode dio \
  --flash-size 4MB \
  --flash-freq 40m \
  0x1000 "$OUT_DIR/bootloader.bin" \
  0x8000 "$OUT_DIR/partition-table.bin" \
  0x10000 "$OUT_DIR/suzuha-firmware.bin"

echo "=== Done ==="
