#pragma once

#include "esp_err.h"

// Expression types for the face display
typedef enum {
    FACE_NEUTRAL,
    FACE_HAPPY,
    FACE_SAD,
    FACE_SURPRISED,
    FACE_ANGRY,
    FACE_SLEEPY,
    FACE_THINKING,
    FACE_TALKING,
} face_expression_t;

esp_err_t display_task_start(void);
void display_task_stop(void);
void display_set_expression(face_expression_t expr);
void display_set_debug_text(const char *text);
