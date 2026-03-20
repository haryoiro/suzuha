#pragma once

#include "esp_err.h"
#include <stdint.h>
#include <stddef.h>
#include <stdbool.h>

esp_err_t speaker_task_start(void);
void speaker_task_stop(void);
void speaker_feed_data(const uint8_t *pcm, size_t len);
bool speaker_is_playing(void);

// Called by audio_task to share the I2S TX handle (full-duplex)
#if CONFIG_IDF_TARGET_ESP32P4
#include "driver/i2s_std.h"
void speaker_set_i2s_tx(i2s_chan_handle_t tx_chan);
#endif
