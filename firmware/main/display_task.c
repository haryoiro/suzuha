#include "display_task.h"
#include "config.h"
#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include <string.h>

static const char *TAG = "display";

// ============================================================
// ESP32-P4: SSD1351 128x128 OLED via SPI
// ============================================================
#if CONFIG_IDF_TARGET_ESP32P4

#include "ssd1351.h"

static face_expression_t s_current_expr = FACE_NEUTRAL;
static volatile bool s_expr_dirty = true;

// Eye/mouth parameters scaled for 128x128
typedef struct {
    int eye_w, eye_h;       // ellipse radii (half-width, half-height)
    int eye_y_offset;       // vertical shift from center
    int mouth_w, mouth_h;   // mouth size
    bool mouth_open;
} face_params_t;

static const face_params_t s_faces[] = {
    [FACE_NEUTRAL]   = {  6,  6,   0,  10,  2, false },
    [FACE_HAPPY]     = {  6,  4,  -2,  14,  5, false },
    [FACE_SAD]       = {  5,  7,   2,   8,  4, true  },
    [FACE_SURPRISED] = {  8,  8,   0,  12,  8, true  },
    [FACE_ANGRY]     = {  7,  5,  -1,  10,  3, false },
    [FACE_SLEEPY]    = {  6,  3,   0,   8,  2, false },
    [FACE_THINKING]  = {  5,  5,  -3,   6,  2, false },
    [FACE_TALKING]   = {  6,  6,   0,  10,  7, true  },
};

static void draw_face(void)
{
    ssd1351_clear(SSD1351_BLACK);

    const int cx = SSD1351_WIDTH / 2;
    const int cy = SSD1351_HEIGHT / 2 - 8;
    const int eye_spacing = 20;

    const face_params_t *f = &s_faces[s_current_expr];

    // Left eye
    ssd1351_fill_ellipse(cx - eye_spacing, cy + f->eye_y_offset,
                         f->eye_w, f->eye_h, SSD1351_WHITE);

    // Right eye
    ssd1351_fill_ellipse(cx + eye_spacing, cy + f->eye_y_offset,
                         f->eye_w, f->eye_h, SSD1351_WHITE);

    // Mouth
    int mouth_y = cy + 25;
    ssd1351_fill_rounded_rect(cx - f->mouth_w / 2, mouth_y,
                               f->mouth_w, f->mouth_h,
                               f->mouth_open ? f->mouth_h / 2 : 1,
                               SSD1351_WHITE);

    ssd1351_flush();
}

static void display_task(void *arg)
{
    while (true) {
        if (s_expr_dirty) {
            draw_face();
            s_expr_dirty = false;
        }
        vTaskDelay(pdMS_TO_TICKS(33));  // ~30 fps
    }
    vTaskDelete(NULL);
}

esp_err_t display_task_start(void)
{
    esp_err_t ret = ssd1351_init();
    if (ret != ESP_OK) {
        ESP_LOGE(TAG, "SSD1351 init failed");
        return ret;
    }

    draw_face();

    xTaskCreatePinnedToCore(display_task, "display", 4096, NULL, 3, NULL, 1);

    ESP_LOGI(TAG, "Display started (SSD1351 %dx%d)", SSD1351_WIDTH, SSD1351_HEIGHT);
    return ESP_OK;
}

void display_task_stop(void)
{
    // TODO: cleanup
}

void display_set_expression(face_expression_t expr)
{
    s_current_expr = expr;
    s_expr_dirty = true;
}

void display_set_debug_text(const char *text)
{
    // No text rendering on SSD1351 for now
    (void)text;
}

// ============================================================
// Other targets: no display
// ============================================================
#else

esp_err_t display_task_start(void)
{
    ESP_LOGI(TAG, "Display task skipped (no display on this board)");
    return ESP_OK;
}

void display_task_stop(void) {}
void display_set_expression(face_expression_t expr) { (void)expr; }
void display_set_debug_text(const char *text) { (void)text; }

#endif
