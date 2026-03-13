#pragma once

#include "esp_err.h"
#include <stdint.h>
#include <stdbool.h>
#include <stddef.h>

/**
 * Binary frame format (ESP32 <-> Server):
 *
 *   [1 byte: frame_type] [payload...]
 *
 * Frame types:
 *   0x01 AUDIO  - PCM16 audio chunk (ESP32 -> Server)
 *   0x02 IMAGE  - JPEG image data   (ESP32 -> Server)
 *   0x03 COMMAND - JSON command      (Server -> ESP32)
 *   0x04 STATUS  - JSON status       (ESP32 -> Server)
 *
 * COMMAND payload (JSON):
 *   {"cmd": "servo", "pan": 90, "tilt": 45}
 *   {"cmd": "capture"}           // request image capture
 *   {"cmd": "audio_start"}       // start audio streaming
 *   {"cmd": "audio_stop"}        // stop audio streaming
 */

typedef void (*ws_command_handler_t)(const char *json, size_t len);

esp_err_t ws_client_init(const char *uri, ws_command_handler_t handler);
esp_err_t ws_client_send_binary(uint8_t frame_type, const uint8_t *data, size_t len);
esp_err_t ws_client_send_status(const char *json);
bool ws_client_is_connected(void);
void ws_client_destroy(void);
