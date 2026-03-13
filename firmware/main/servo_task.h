#pragma once

#include "esp_err.h"

esp_err_t servo_task_start(void);
void servo_task_stop(void);
void servo_set_position(int pan_deg, int tilt_deg);
