# ハードウェア接続ガイド — Waveshare ESP32-P4-NANO

## ボード概要

- **MCU**: ESP32-P4 (RISC-V デュアルコア 400MHz + シングルコア LP)
- **無線**: ESP32-C6 コプロセッサ (WiFi 6 / BLE 5)
- **メモリ**: 32MB PSRAM / 16MB NOR Flash
- **カメラ**: 2-lane MIPI-CSI (RPi カメラ互換)
- **ディスプレイ**: SSD1351 128×128 RGB OLED (SPI)
- **オーディオ**: オンボードマイク + スピーカーヘッダー (MX1.25 2P, 8Ω 2W)
- **GPIO**: 2×13 ヘッダー × 2列 = 28 プログラマブル GPIO

## ピンアサイン一覧

### オンボードオーディオ (I2S) — はんだ付け不要

ボード上に実装済み。外部配線不要。

| 信号     | GPIO | 用途                        |
|----------|------|-----------------------------|
| MCLK     | 13   | マスタークロック             |
| SCLK     | 12   | ビットクロック (BCLK)        |
| LRCK     | 10   | ワードセレクト (WS)          |
| DSDIN    |  9   | マイク入力 (データ)          |
| ASDOUT   | 11   | スピーカー出力 (データ)      |
| PA_Ctrl  | 53   | パワーアンプ有効化           |

**スピーカー**: MX1.25 2Pコネクタに 8Ω 2W スピーカーを接続。

### カメラ (MIPI-CSI) — はんだ付け不要

ボード上の 2-lane MIPI-CSI コネクタに RPi 互換カメラを FFC ケーブルで接続。

> 対応例: Raspberry Pi Camera Module v2, v3

### ディスプレイ (SSD1351 OLED) — SPI 外部配線

128×128 RGB OLED (SSD1351) を SPI で接続。

| 信号       | GPIO | SSD1351 ピン |
|------------|------|-------------|
| SCK (CLK)  | 20   | SCL / SCK   |
| MOSI (SDA) | 21   | SDA / DIN   |
| CS         | 22   | CS          |
| DC         | 23   | DC / A0     |
| RST        | 24   | RST / RES   |
| VCC        | —    | 3.3V        |
| GND        | —    | GND         |

### I2C — はんだ付け不要 (ヘッダーに出ている)

| 信号 | GPIO | 備考           |
|------|------|----------------|
| SCL  |  8   | デフォルト I2C |
| SDA  |  7   | デフォルト I2C |

### サーボモーター (PWM) — 外部配線が必要

GPIO ヘッダーからサーボへ配線。以下はデフォルト設定 (`idf.py menuconfig` で変更可能)。

| サーボ | GPIO (デフォルト) | ヘッダー | 配線先          |
|--------|-------------------|----------|-----------------|
| パン   |  6                | ヘッダー | サーボ信号線 (橙) |
| チルト |  0                | ヘッダー | サーボ信号線 (橙) |

> **注意**: チルトのデフォルトは GPIO7 だが、カメラ SCCB (I2C SDA) と衝突するため
> `sdkconfig.defaults.esp32p4` で GPIO25 に変更済み。

**サーボ配線**:

```
ESP32-P4-NANO          SG90サーボ (×2)
─────────────          ──────────────
GPIO6  (ヘッダー) ──── パンサーボ 信号線 (橙/白)
GPIO0  (ヘッダー) ──── チルトサーボ 信号線 (橙/白)
5V     (ヘッダー) ──── サーボ Vcc (赤) ※両方共有
GND    (ヘッダー) ──── サーボ GND (茶) ※両方共有
```

### WiFi / BLE (ESP32-C6 コプロセッサ) — 内部接続、配線不要

| 信号   | GPIO | 備考                    |
|--------|------|-------------------------|
| RESET  | 54   | C6 リセット             |
| CMD    | 19   | SDIO CMD                |
| CLK    | 18   | SDIO CLK                |
| D0     | 14   | SDIO Data 0             |
| D1     | 15   | SDIO Data 1             |
| D2     | 16   | SDIO Data 2             |
| D3     | 17   | SDIO Data 3             |

### Ethernet (IP101 PHY) — オンボード RJ45

| 信号     | GPIO | 信号     | GPIO |
|----------|------|----------|------|
| TXD0     | 34   | RXD0     | 29   |
| TXD1     | 35   | RXD1     | 30   |
| TX_EN    | 49   | CRS_DV   | 28   |
| REF_CLK  | 50   | MDC      | 31   |
| MDIO     | 52   | RESET    | 51   |

### SD カード (SDMMC) — オンボード TF スロット

| 信号 | GPIO |
|------|------|
| CLK  | 43   |
| CMD  | 44   |
| D0   | 39   |
| D1   | 40   |
| D2   | 41   |
| D3   | 42   |

## サーボ用 GPIO の選び方

以下の GPIO は他の機能に**使用済み**なので避けること:

| GPIO     | 使用先                     |
|----------|---------------------------|
| 7, 8     | I2C (SDA, SCL)            |
| 9-13     | I2S オーディオ             |
| 20-24    | SSD1351 OLED (SPI)        |
| 14-19    | ESP32-C6 SDIO 通信        |
| 28-31, 34-35, 49-52 | Ethernet       |
| 39-44    | SD カード                  |
| 53       | PA_Ctrl (スピーカー)      |
| 54       | C6 リセット               |

**サーボに使用可能な候補 GPIO** (要回路図確認):

```
GPIO 0-6, 25-27, 32-33, 36-38, 45-48
```

> 最終的なピン選定はボード裏面の回路図シルク印刷 or
> [回路図PDF](https://files.waveshare.com/wiki/ESP32-P4-NANO/ESP32-P4-NANO-schematic.pdf)
> を参照してヘッダーに出ているピンを確認すること。

## 配線ダイアグラム

```
                    ┌──────────────────────────┐
                    │   ESP32-P4-NANO (top)    │
                    │                          │
  RPi Camera ◄─FFC─┤ MIPI-CSI                 │
                    │                          │
                    │ [MIC]  内蔵マイク         │
                    │                          │
                    │ [SPK]  MX1.25 ──► 8Ω 2W │
                    │         コネクタ  スピーカー│
                    │                          │
 SSD1351 OLED ◄─SPI┤ GPIO 20-24               │
                    │                          │
       ┌────────────┤ GPIO ヘッダー (左)        │
       │            │   :                      │
       │            │   :                      │
       │            │                          │
       │  ┌─────────┤ GPIO ヘッダー (右)        │
       │  │         │   :                      │
       │  │         │   :                      │
       │  │         │                          │
       │  │         │ RJ45     USB-C   USB-A   │
       │  │         └──────────────────────────┘
       │  │
       │  │    ┌──────────────┐
       │  ├───►│ サーボ (パン) │ ← GPIO6 (信号)
       │  │    │  SG90等      │ ← 5V  (電源)
       │  │    │              │ ← GND
       │  │    └──────────────┘
       │  │
       │  │    ┌──────────────┐
       │  └───►│ サーボ (チルト)│ ← GPIO?? (信号)
       │       │  SG90等      │ ← 5V  (電源)
       │       │              │ ← GND
       │       └──────────────┘
       │
       └── 5V / GND 共有
```

## セットアップ手順

1. **カメラ**: FFC ケーブルで MIPI-CSI コネクタに RPi カメラを接続
2. **ディスプレイ**: SSD1351 OLED を GPIO 20-24 に SPI 配線
3. **スピーカー**: MX1.25 コネクタに 8Ω 2W スピーカーを接続
3. **サーボ**: GPIO ヘッダーから配線 (上記参照)
4. **電源**: USB-C で給電 (サーボ使用時は外部 5V 推奨)
5. **ファームウェア書き込み**: USB-C UART ポートから `esptool.py` で書き込み

## menuconfig で変更可能な設定

```bash
docker compose run --rm firmware bash -c \
  "source \$IDF_PATH/export.sh && cd /firmware && idf.py menuconfig"
```

`Suzuha Physical Agent` メニュー内:
- WiFi SSID / Password
- サーバー WebSocket URI
- サーボ パン GPIO
- サーボ チルト GPIO

## 参考リンク

- [Waveshare ESP32-P4-NANO Wiki](https://www.waveshare.com/wiki/ESP32-P4-Nano-StartPage)
- [回路図 PDF](https://files.waveshare.com/wiki/ESP32-P4-NANO/ESP32-P4-NANO-schematic.pdf)
- [製品ページ](https://www.waveshare.com/esp32-p4-nano.htm)
