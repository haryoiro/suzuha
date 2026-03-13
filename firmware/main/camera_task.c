#include "camera_task.h"
#include "ws_client.h"
#include "config.h"
#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

static const char *TAG = "camera";
static TaskHandle_t s_task_handle = NULL;
static volatile bool s_capture_requested = false;

// ============================================================
// ESP32-CAM (OV2640, parallel 8-bit) — esp_camera API
// ============================================================
#if CONFIG_IDF_TARGET_ESP32

#include "esp_camera.h"

// AI-Thinker ESP32-CAM pin definition
#define CAM_PIN_PWDN    32
#define CAM_PIN_RESET   -1
#define CAM_PIN_XCLK     0
#define CAM_PIN_SIOD    26
#define CAM_PIN_SIOC    27
#define CAM_PIN_D7      35
#define CAM_PIN_D6      34
#define CAM_PIN_D5      39
#define CAM_PIN_D4      36
#define CAM_PIN_D3      21
#define CAM_PIN_D2      19
#define CAM_PIN_D1      18
#define CAM_PIN_D0       5
#define CAM_PIN_VSYNC   25
#define CAM_PIN_HREF    23
#define CAM_PIN_PCLK    22

static esp_err_t init_camera(void)
{
    camera_config_t config = {
        .pin_pwdn  = CAM_PIN_PWDN,
        .pin_reset = CAM_PIN_RESET,
        .pin_xclk  = CAM_PIN_XCLK,
        .pin_sccb_sda = CAM_PIN_SIOD,
        .pin_sccb_scl = CAM_PIN_SIOC,
        .pin_d7 = CAM_PIN_D7,
        .pin_d6 = CAM_PIN_D6,
        .pin_d5 = CAM_PIN_D5,
        .pin_d4 = CAM_PIN_D4,
        .pin_d3 = CAM_PIN_D3,
        .pin_d2 = CAM_PIN_D2,
        .pin_d1 = CAM_PIN_D1,
        .pin_d0 = CAM_PIN_D0,
        .pin_vsync = CAM_PIN_VSYNC,
        .pin_href  = CAM_PIN_HREF,
        .pin_pclk  = CAM_PIN_PCLK,
        .xclk_freq_hz = 20000000,
        .ledc_timer   = LEDC_TIMER_0,
        .ledc_channel = LEDC_CHANNEL_0,
        .pixel_format = PIXFORMAT_JPEG,
        .frame_size   = FRAMESIZE_VGA,
        .jpeg_quality = 12,
        .fb_count     = 1,
        .grab_mode    = CAMERA_GRAB_WHEN_EMPTY,
    };
    return esp_camera_init(&config);
}

static void capture_and_send(void)
{
    camera_fb_t *fb = esp_camera_fb_get();
    if (!fb) {
        ESP_LOGW(TAG, "Camera capture failed");
        return;
    }
    if (ws_client_is_connected()) {
        ws_client_send_binary(FRAME_TYPE_IMAGE, fb->buf, fb->len);
    }
    esp_camera_fb_return(fb);
}

// ============================================================
// ESP32-P4 (MIPI-CSI + HW JPEG) — placeholder
// ============================================================
#elif CONFIG_IDF_TARGET_ESP32P4

static esp_err_t init_camera(void)
{
    // TODO: esp_cam_ctlr_csi + ISP + JPEG codec
    ESP_LOGI(TAG, "P4 camera init placeholder");
    return ESP_OK;
}

static void capture_and_send(void)
{
    ESP_LOGI(TAG, "P4 capture not yet implemented");
}

// ============================================================
// Unsupported target
// ============================================================
#else

static esp_err_t init_camera(void) { return ESP_OK; }
static void capture_and_send(void) {}

#endif

// ============================================================
// Common task code (shared by all targets)
// ============================================================
static void camera_task(void *arg)
{
    while (true) {
        if (!s_capture_requested) {
            vTaskDelay(pdMS_TO_TICKS(50));
            continue;
        }
        s_capture_requested = false;
        capture_and_send();
    }
    vTaskDelete(NULL);
}

esp_err_t camera_task_start(void)
{
    esp_err_t ret = init_camera();
    if (ret != ESP_OK) {
        return ret;
    }

    BaseType_t created = xTaskCreatePinnedToCore(
        camera_task, "camera", 4096, NULL, 4, &s_task_handle, 0);

    return (created == pdPASS) ? ESP_OK : ESP_FAIL;
}

void camera_task_stop(void)
{
    if (s_task_handle) {
        vTaskDelete(s_task_handle);
        s_task_handle = NULL;
    }
}

void camera_task_request_capture(void)
{
    s_capture_requested = true;
    ESP_LOGI(TAG, "Capture requested");
}
