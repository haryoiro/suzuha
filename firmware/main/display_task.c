#include "display_task.h"
#include "config.h"
#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include <string.h>

static const char *TAG = "display";

// ============================================================
// ESP32-P4: MIPI-DSI + LVGL via Waveshare BSP
// ============================================================
#if CONFIG_IDF_TARGET_ESP32P4

#include "bsp/esp-bsp.h"
#include "lvgl.h"

static lv_obj_t *s_eye_left = NULL;
static lv_obj_t *s_eye_right = NULL;
static lv_obj_t *s_mouth = NULL;
static lv_obj_t *s_debug_label = NULL;
static lv_display_t *s_display = NULL;

static face_expression_t s_current_expr = FACE_NEUTRAL;
static char s_debug_text[128] = "";
static volatile bool s_expr_dirty = true;
static volatile bool s_debug_dirty = false;

// Eye parameters per expression
typedef struct {
    int eye_w, eye_h;      // eye ellipse size
    int eye_y_offset;      // vertical offset from center
    int pupil_offset_y;    // pupil vertical offset (for looking up/down)
    int mouth_w, mouth_h;  // mouth arc size
    bool mouth_open;
} face_params_t;

static const face_params_t s_faces[] = {
    [FACE_NEUTRAL]   = { 40, 40,  0,  0, 30, 5,  false },
    [FACE_HAPPY]     = { 40, 25, -5,  0, 40, 15, false },  // squinted
    [FACE_SAD]       = { 35, 45,  5,  5, 25, 10, true  },
    [FACE_SURPRISED] = { 50, 50,  0, -5, 35, 25, true  },  // wide
    [FACE_ANGRY]     = { 45, 30, -3,  0, 30, 8,  false },
    [FACE_SLEEPY]    = { 40, 15,  0,  0, 25, 5,  false },  // nearly closed
    [FACE_THINKING]  = { 35, 35,  0, -8, 20, 5,  false },  // looking up
    [FACE_TALKING]   = { 40, 40,  0,  0, 30, 20, true  },
};

static void draw_face(void)
{
    if (!s_display) return;

    lv_display_t *disp = s_display;
    int scr_w = lv_display_get_horizontal_resolution(disp);
    int scr_h = lv_display_get_vertical_resolution(disp);
    int cx = scr_w / 2;
    int cy = scr_h / 2 - 20;

    const face_params_t *f = &s_faces[s_current_expr];
    int eye_spacing = scr_w / 5;

    // Left eye
    lv_obj_set_size(s_eye_left, f->eye_w, f->eye_h);
    lv_obj_set_pos(s_eye_left, cx - eye_spacing - f->eye_w / 2, cy + f->eye_y_offset - f->eye_h / 2);
    lv_obj_set_style_radius(s_eye_left, LV_RADIUS_CIRCLE, 0);

    // Right eye
    lv_obj_set_size(s_eye_right, f->eye_w, f->eye_h);
    lv_obj_set_pos(s_eye_right, cx + eye_spacing - f->eye_w / 2, cy + f->eye_y_offset - f->eye_h / 2);
    lv_obj_set_style_radius(s_eye_right, LV_RADIUS_CIRCLE, 0);

    // Mouth
    int mouth_y = cy + scr_h / 6;
    lv_obj_set_size(s_mouth, f->mouth_w, f->mouth_h);
    lv_obj_set_pos(s_mouth, cx - f->mouth_w / 2, mouth_y);
    lv_obj_set_style_radius(s_mouth, f->mouth_open ? f->mouth_h : f->mouth_h / 2, 0);
}

static void update_debug_label(void)
{
    if (s_debug_label && s_debug_text[0]) {
        lv_label_set_text(s_debug_label, s_debug_text);
        lv_obj_clear_flag(s_debug_label, LV_OBJ_FLAG_HIDDEN);
    } else if (s_debug_label) {
        lv_obj_add_flag(s_debug_label, LV_OBJ_FLAG_HIDDEN);
    }
}

static void display_task(void *arg)
{
    while (true) {
        // Lock LVGL before touching widgets
        bsp_display_lock(0);

        if (s_expr_dirty) {
            draw_face();
            s_expr_dirty = false;
        }
        if (s_debug_dirty) {
            update_debug_label();
            s_debug_dirty = false;
        }

        bsp_display_unlock();

        lv_timer_handler();
        vTaskDelay(pdMS_TO_TICKS(30));  // ~33fps
    }
    vTaskDelete(NULL);
}

esp_err_t display_task_start(void)
{
    // Init BSP display (MIPI-DSI, selected via menuconfig)
    bsp_display_cfg_t cfg = {
        .lvgl_port_cfg = ESP_LVGL_PORT_INIT_CONFIG(),
    };
    s_display = bsp_display_start_with_config(&cfg);
    if (!s_display) {
        ESP_LOGE(TAG, "Display init failed");
        return ESP_FAIL;
    }

    bsp_display_brightness_init();
    bsp_display_brightness_set(80);

    // Create face widgets
    bsp_display_lock(0);

    lv_obj_t *scr = lv_display_get_screen_active(s_display);
    lv_obj_set_style_bg_color(scr, lv_color_black(), 0);

    // Eyes (white circles/ellipses)
    s_eye_left = lv_obj_create(scr);
    lv_obj_set_style_bg_color(s_eye_left, lv_color_white(), 0);
    lv_obj_set_style_border_width(s_eye_left, 0, 0);

    s_eye_right = lv_obj_create(scr);
    lv_obj_set_style_bg_color(s_eye_right, lv_color_white(), 0);
    lv_obj_set_style_border_width(s_eye_right, 0, 0);

    // Mouth (white rounded rect)
    s_mouth = lv_obj_create(scr);
    lv_obj_set_style_bg_color(s_mouth, lv_color_white(), 0);
    lv_obj_set_style_border_width(s_mouth, 0, 0);

    // Debug text (bottom, small green font)
    s_debug_label = lv_label_create(scr);
    lv_obj_set_style_text_color(s_debug_label, lv_color_make(0, 255, 0), 0);
    lv_obj_set_style_text_font(s_debug_label, &lv_font_montserrat_14, 0);
    lv_obj_align(s_debug_label, LV_ALIGN_BOTTOM_LEFT, 5, -5);
    lv_obj_add_flag(s_debug_label, LV_OBJ_FLAG_HIDDEN);

    draw_face();
    bsp_display_unlock();

    xTaskCreatePinnedToCore(display_task, "display", 4096, NULL, 3, NULL, 1);

    ESP_LOGI(TAG, "Display started");
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
    strncpy(s_debug_text, text, sizeof(s_debug_text) - 1);
    s_debug_text[sizeof(s_debug_text) - 1] = '\0';
    s_debug_dirty = true;
}

// ============================================================
// Other targets: no display
// ============================================================
#else

esp_err_t display_task_start(void)
{
    ESP_LOGI(TAG, "Display task skipped (no DSI on this board)");
    return ESP_OK;
}

void display_task_stop(void) {}
void display_set_expression(face_expression_t expr) { (void)expr; }
void display_set_debug_text(const char *text) { (void)text; }

#endif
