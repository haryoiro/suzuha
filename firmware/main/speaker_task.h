#pragma once

#include "esp_err.h"
#include <stdint.h>
#include <stddef.h>

esp_err_t speaker_task_start(void);
void speaker_task_stop(void);
void speaker_feed_data(const uint8_t *pcm, size_t len);
