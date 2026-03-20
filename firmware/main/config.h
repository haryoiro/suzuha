#pragma once

// ---- WiFi ----
#define WIFI_SSID       CONFIG_WIFI_SSID
#define WIFI_PASS       CONFIG_WIFI_PASSWORD

// ---- suzuha2 Server ----
#define SERVER_URI      CONFIG_SERVER_URI

// ---- Audio Input (I2S mic) ----
#define AUDIO_SAMPLE_RATE   24000
#define AUDIO_BITS          16
#define AUDIO_CHANNELS      1
#define AUDIO_CHUNK_MS      100  // send every 100ms

// ---- Audio Output (I2S speaker, P4-NANO only) ----
// NOTE: speaker shares I2S bus with mic, so same sample rate (full-duplex)
#define SPEAKER_SAMPLE_RATE AUDIO_SAMPLE_RATE
#define SPEAKER_BITS        16
#define SPEAKER_CHANNELS    1
#if CONFIG_IDF_TARGET_ESP32P4
#define PA_CTRL_GPIO        GPIO_NUM_53  // power amplifier enable
#endif

// ---- Display (SSD1351 OLED, SPI) ----
#define SSD1351_SCK_GPIO    GPIO_NUM_20
#define SSD1351_MOSI_GPIO   GPIO_NUM_21
#define SSD1351_CS_GPIO     GPIO_NUM_22
#define SSD1351_DC_GPIO     GPIO_NUM_23
#define SSD1351_RST_GPIO    GPIO_NUM_24

// ---- Camera (MIPI-CSI, OV5647) ----
#define CAM_WIDTH       800
#define CAM_HEIGHT      640
#define CAM_FPS         5       // low fps to reduce bandwidth

// ---- Servo (PWM) ----
#define SERVO_PAN_GPIO      CONFIG_SERVO_PAN_GPIO
#define SERVO_TILT_GPIO     CONFIG_SERVO_TILT_GPIO
#define SERVO_FREQ_HZ       50
#define SERVO_MIN_PULSEWIDTH_US  500
#define SERVO_MAX_PULSEWIDTH_US  2500

// ---- WebSocket frame types ----
#define FRAME_TYPE_AUDIO    0x01  // mic PCM    (ESP32 -> Server)
#define FRAME_TYPE_IMAGE    0x02  // JPEG       (ESP32 -> Server)
#define FRAME_TYPE_COMMAND  0x03  // JSON       (Server -> ESP32)
#define FRAME_TYPE_STATUS   0x04  // JSON       (ESP32 -> Server)
#define FRAME_TYPE_TTS      0x05  // TTS PCM    (Server -> ESP32)
