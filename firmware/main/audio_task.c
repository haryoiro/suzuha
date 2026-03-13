#include "audio_task.h"
#include "ws_client.h"
#include "config.h"
#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include <string.h>

static const char *TAG = "audio";

// ============================================================
// ESP32-P4-NANO: onboard I2S microphone
// ============================================================
#if CONFIG_IDF_TARGET_ESP32P4

#include "driver/i2s_std.h"
#include "esp_heap_caps.h"

#define CHUNK_SAMPLES   (AUDIO_SAMPLE_RATE * AUDIO_CHUNK_MS / 1000)
#define CHUNK_BYTES     (CHUNK_SAMPLES * (AUDIO_BITS / 8) * AUDIO_CHANNELS)

static TaskHandle_t s_task_handle = NULL;
static volatile bool s_streaming = false;
static i2s_chan_handle_t s_rx_chan = NULL;

static esp_err_t init_microphone(void)
{
    i2s_chan_config_t chan_cfg = I2S_CHANNEL_DEFAULT_CONFIG(I2S_NUM_0, I2S_ROLE_MASTER);
    ESP_ERROR_CHECK(i2s_new_channel(&chan_cfg, NULL, &s_rx_chan));

    i2s_std_config_t std_cfg = {
        .clk_cfg = I2S_STD_CLK_DEFAULT_CONFIG(AUDIO_SAMPLE_RATE),
        .slot_cfg = I2S_STD_PHILIPS_SLOT_DEFAULT_CONFIG(I2S_DATA_BIT_WIDTH_16BIT, I2S_SLOT_MODE_MONO),
        .gpio_cfg = {
            .mclk = GPIO_NUM_13,
            .bclk = GPIO_NUM_12,
            .ws   = GPIO_NUM_10,
            .dout = I2S_GPIO_UNUSED,
            .din  = GPIO_NUM_9,
            .invert_flags = {
                .mclk_inv = false,
                .bclk_inv = false,
                .ws_inv   = false,
            },
        },
    };
    ESP_ERROR_CHECK(i2s_channel_init_std_mode(s_rx_chan, &std_cfg));
    ESP_ERROR_CHECK(i2s_channel_enable(s_rx_chan));
    return ESP_OK;
}

static void audio_task(void *arg)
{
    uint8_t *buf = heap_caps_malloc(CHUNK_BYTES, MALLOC_CAP_SPIRAM | MALLOC_CAP_8BIT);
    if (!buf) {
        ESP_LOGE(TAG, "Failed to allocate audio buffer in PSRAM");
        vTaskDelete(NULL);
        return;
    }

    size_t bytes_read = 0;

    while (true) {
        if (!s_streaming) {
            vTaskDelay(pdMS_TO_TICKS(100));
            continue;
        }

        esp_err_t ret = i2s_channel_read(s_rx_chan, buf, CHUNK_BYTES, &bytes_read,
                                          pdMS_TO_TICKS(1000));
        if (ret != ESP_OK || bytes_read == 0) {
            continue;
        }

        if (ws_client_is_connected()) {
            ws_client_send_binary(FRAME_TYPE_AUDIO, buf, bytes_read);
        }
    }

    free(buf);
    vTaskDelete(NULL);
}

esp_err_t audio_task_start(void)
{
    esp_err_t ret = init_microphone();
    if (ret != ESP_OK) {
        return ret;
    }

    BaseType_t created = xTaskCreatePinnedToCore(
        audio_task, "audio", 4096, NULL, 5, &s_task_handle, 0);

    return (created == pdPASS) ? ESP_OK : ESP_FAIL;
}

void audio_task_stop(void)
{
    s_streaming = false;
    if (s_task_handle) {
        vTaskDelete(s_task_handle);
        s_task_handle = NULL;
    }
    if (s_rx_chan) {
        i2s_channel_disable(s_rx_chan);
        i2s_del_channel(s_rx_chan);
        s_rx_chan = NULL;
    }
}

void audio_task_set_streaming(bool enabled)
{
    s_streaming = enabled;
    ESP_LOGI(TAG, "Audio streaming %s", enabled ? "started" : "stopped");
}

// ============================================================
// ESP32-CAM / others: no mic — stubs
// ============================================================
#else

esp_err_t audio_task_start(void)
{
    ESP_LOGI(TAG, "Audio task skipped (no mic on this board)");
    return ESP_OK;
}

void audio_task_stop(void) {}
void audio_task_set_streaming(bool enabled) { (void)enabled; }

#endif
