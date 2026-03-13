#pragma once

#include "esp_err.h"

esp_err_t camera_task_start(void);
void camera_task_stop(void);
void camera_task_request_capture(void);
