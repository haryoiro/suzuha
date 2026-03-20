#include <string.h>
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "esp_log.h"
#include "nvs_flash.h"
#include "cJSON.h"

#include "config.h"
#include "wifi.h"
#include "ws_client.h"
#include "audio_task.h"
#include "camera_task.h"
#include "speaker_task.h"
#include "display_task.h"
#include "servo_task.h"

static const char *TAG = "suzuha";

static void handle_command(const char *json, size_t len)
{
    cJSON *root = cJSON_ParseWithLength(json, len);
    if (!root) {
        ESP_LOGW(TAG, "Failed to parse command JSON");
        return;
    }

    const cJSON *cmd = cJSON_GetObjectItem(root, "cmd");
    if (!cJSON_IsString(cmd)) {
        goto cleanup;
    }

    if (strcmp(cmd->valuestring, "servo") == 0) {
        int pan = cJSON_GetObjectItem(root, "pan")->valueint;
        int tilt = cJSON_GetObjectItem(root, "tilt")->valueint;
        servo_set_position(pan, tilt);
    } else if (strcmp(cmd->valuestring, "capture") == 0) {
        camera_task_request_capture();
    } else if (strcmp(cmd->valuestring, "audio_start") == 0) {
        audio_task_set_streaming(true);
    } else if (strcmp(cmd->valuestring, "audio_stop") == 0) {
        audio_task_set_streaming(false);
    } else if (strcmp(cmd->valuestring, "face") == 0) {
        int expr = cJSON_GetObjectItem(root, "expression")->valueint;
        display_set_expression((face_expression_t)expr);
    } else if (strcmp(cmd->valuestring, "volume") == 0) {
        int level = cJSON_GetObjectItem(root, "level")->valueint;
        audio_set_volume(level);
    } else {
        ESP_LOGW(TAG, "Unknown command: %s", cmd->valuestring);
    }

cleanup:
    cJSON_Delete(root);
}

void app_main(void)
{
    ESP_LOGI(TAG, "suzuha physical agent starting...");

    // NVS init (required for WiFi)
    esp_err_t ret = nvs_flash_init();
    if (ret == ESP_ERR_NVS_NO_FREE_PAGES || ret == ESP_ERR_NVS_NEW_VERSION_FOUND) {
        ESP_ERROR_CHECK(nvs_flash_erase());
        ret = nvs_flash_init();
    }
    ESP_ERROR_CHECK(ret);

    // Display & servo first (no network dependency)
    ESP_ERROR_CHECK(display_task_start());
    ESP_ERROR_CHECK(servo_task_start());

    // WiFi (non-fatal — device works offline)
    ret = wifi_init_sta(WIFI_SSID, WIFI_PASS);
    if (ret != ESP_OK) {
        ESP_LOGW(TAG, "WiFi failed: %s — running offline", esp_err_to_name(ret));
    } else {
        ESP_LOGI(TAG, "WiFi connected");

        // WebSocket
        ret = ws_client_init(SERVER_URI, handle_command);
        if (ret != ESP_OK) {
            ESP_LOGW(TAG, "WebSocket failed: %s", esp_err_to_name(ret));
        } else {
            ESP_LOGI(TAG, "WebSocket connected");
        }
    }

    // Audio & camera
    ESP_ERROR_CHECK(audio_task_start());
    ESP_ERROR_CHECK(speaker_task_start());
    ESP_ERROR_CHECK(camera_task_start());

    // Auto-start mic streaming (server handles STT)
    audio_task_set_streaming(true);

    ESP_LOGI(TAG, "suzuha physical agent ready");
}
