#!/bin/bash
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
  0x1000 firmware/build-esp32/bootloader.bin \
  0x8000 firmware/build-esp32/partition-table.bin \
  0x10000 firmware/build-esp32/suzuha-firmware.bin
