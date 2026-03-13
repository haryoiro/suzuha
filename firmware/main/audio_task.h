#pragma once

#include "esp_err.h"
#include <stdbool.h>

esp_err_t audio_task_start(void);
void audio_task_stop(void);
void audio_task_set_streaming(bool enabled);
