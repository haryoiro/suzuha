#include "ws_client.h"
#include "speaker_task.h"
#include "config.h"
#include "esp_log.h"
#include "esp_websocket_client.h"
#include "freertos/FreeRTOS.h"
#include "freertos/semphr.h"
#include <string.h>
#include <stdlib.h>

static const char *TAG = "ws";
static esp_websocket_client_handle_t s_client = NULL;
static ws_command_handler_t s_command_handler = NULL;
static bool s_connected = false;

static void ws_event_handler(void *arg, esp_event_base_t event_base,
                             int32_t event_id, void *event_data)
{
    esp_websocket_event_data_t *data = (esp_websocket_event_data_t *)event_data;

    switch (event_id) {
    case WEBSOCKET_EVENT_CONNECTED:
        ESP_LOGI(TAG, "Connected to server");
        s_connected = true;
        break;

    case WEBSOCKET_EVENT_DISCONNECTED:
        ESP_LOGW(TAG, "Disconnected from server");
        s_connected = false;
        break;

    case WEBSOCKET_EVENT_DATA:
        if (data->op_code == 0x02) {
            // Binary frame: first byte is frame_type
            if (data->data_len > 1 && data->payload_len > 1) {
                uint8_t frame_type = (uint8_t)data->data_ptr[0];
                if (frame_type == FRAME_TYPE_COMMAND && s_command_handler) {
                    s_command_handler(data->data_ptr + 1, data->data_len - 1);
                } else if (frame_type == FRAME_TYPE_TTS) {
                    speaker_feed_data((const uint8_t *)data->data_ptr + 1, data->data_len - 1);
                }
            }
        } else if (data->op_code == 0x01) {
            // Text frame: treat as JSON command
            if (s_command_handler && data->data_len > 0) {
                s_command_handler(data->data_ptr, data->data_len);
            }
        }
        break;

    case WEBSOCKET_EVENT_ERROR:
        ESP_LOGE(TAG, "WebSocket error");
        break;
    }
}

esp_err_t ws_client_init(const char *uri, ws_command_handler_t handler)
{
    s_command_handler = handler;

    esp_websocket_client_config_t config = {
        .uri = uri,
        .buffer_size = 4096,
        .reconnect_timeout_ms = 5000,
        .network_timeout_ms = 10000,
    };

    s_client = esp_websocket_client_init(&config);
    if (!s_client) {
        return ESP_FAIL;
    }

    ESP_ERROR_CHECK(esp_websocket_register_events(
        s_client, WEBSOCKET_EVENT_ANY, ws_event_handler, NULL));

    return esp_websocket_client_start(s_client);
}

esp_err_t ws_client_send_binary(uint8_t frame_type, const uint8_t *data, size_t len)
{
    if (!s_connected || !s_client) {
        return ESP_ERR_INVALID_STATE;
    }

    // Allocate frame: [1 byte type] + [payload]
    size_t frame_len = 1 + len;
    uint8_t *frame = malloc(frame_len);
    if (!frame) {
        return ESP_ERR_NO_MEM;
    }

    frame[0] = frame_type;
    memcpy(frame + 1, data, len);

    int sent = esp_websocket_client_send_bin(s_client, (const char *)frame, frame_len, pdMS_TO_TICKS(3000));
    free(frame);

    return (sent >= 0) ? ESP_OK : ESP_FAIL;
}

esp_err_t ws_client_send_status(const char *json)
{
    if (!s_connected || !s_client) {
        return ESP_ERR_INVALID_STATE;
    }
    int sent = esp_websocket_client_send_text(s_client, json, strlen(json), portMAX_DELAY);
    return (sent >= 0) ? ESP_OK : ESP_FAIL;
}

bool ws_client_is_connected(void)
{
    return s_connected;
}

void ws_client_destroy(void)
{
    if (s_client) {
        esp_websocket_client_stop(s_client);
        esp_websocket_client_destroy(s_client);
        s_client = NULL;
    }
    s_connected = false;
}
