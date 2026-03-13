#include "speaker_task.h"
#include "config.h"
#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include <string.h>

static const char *TAG = "speaker";

// ============================================================
// ESP32-P4-NANO: onboard I2S speaker + PA
// ============================================================
#if CONFIG_IDF_TARGET_ESP32P4

#include "freertos/ringbuf.h"
#include "driver/i2s_std.h"
#include "driver/gpio.h"

// Ring buffer: ~2 seconds of audio
#define RINGBUF_SIZE (SPEAKER_SAMPLE_RATE * (SPEAKER_BITS / 8) * 2)

static i2s_chan_handle_t s_tx_chan = NULL;
static RingbufHandle_t s_ringbuf = NULL;
static TaskHandle_t s_task_handle = NULL;

static esp_err_t init_speaker(void)
{
    // Enable power amplifier GPIO
    gpio_config_t pa_cfg = {
        .pin_bit_mask = (1ULL << PA_CTRL_GPIO),
        .mode = GPIO_MODE_OUTPUT,
    };
    ESP_ERROR_CHECK(gpio_config(&pa_cfg));
    gpio_set_level(PA_CTRL_GPIO, 0);

    // I2S TX channel for speaker
    //   ASDOUT = GPIO11, SCLK = GPIO12, LRCK = GPIO10, MCLK = GPIO13
    i2s_chan_config_t chan_cfg = I2S_CHANNEL_DEFAULT_CONFIG(I2S_NUM_1, I2S_ROLE_MASTER);
    ESP_ERROR_CHECK(i2s_new_channel(&chan_cfg, &s_tx_chan, NULL));

    i2s_std_config_t std_cfg = {
        .clk_cfg = I2S_STD_CLK_DEFAULT_CONFIG(SPEAKER_SAMPLE_RATE),
        .slot_cfg = I2S_STD_PHILIPS_SLOT_DEFAULT_CONFIG(I2S_DATA_BIT_WIDTH_16BIT, I2S_SLOT_MODE_MONO),
        .gpio_cfg = {
            .mclk = GPIO_NUM_13,
            .bclk = GPIO_NUM_12,
            .ws   = GPIO_NUM_10,
            .dout = GPIO_NUM_11,
            .din  = I2S_GPIO_UNUSED,
            .invert_flags = {
                .mclk_inv = false,
                .bclk_inv = false,
                .ws_inv   = false,
            },
        },
    };
    ESP_ERROR_CHECK(i2s_channel_init_std_mode(s_tx_chan, &std_cfg));
    return ESP_OK;
}

static void speaker_task(void *arg)
{
    const size_t chunk = 1024;
    while (true) {
        size_t item_size = 0;
        void *data = xRingbufferReceiveUpTo(s_ringbuf, &item_size, pdMS_TO_TICKS(100), chunk);
        if (data && item_size > 0) {
            gpio_set_level(PA_CTRL_GPIO, 1);
            size_t written = 0;
            i2s_channel_write(s_tx_chan, data, item_size, &written, pdMS_TO_TICKS(500));
            vRingbufferReturnItem(s_ringbuf, data);
        } else {
            gpio_set_level(PA_CTRL_GPIO, 0);
        }
    }
    vTaskDelete(NULL);
}

esp_err_t speaker_task_start(void)
{
    s_ringbuf = xRingbufferCreate(RINGBUF_SIZE, RINGBUF_TYPE_BYTEBUF);
    if (!s_ringbuf) {
        return ESP_ERR_NO_MEM;
    }
    esp_err_t ret = init_speaker();
    if (ret != ESP_OK) return ret;

    ESP_ERROR_CHECK(i2s_channel_enable(s_tx_chan));
    xTaskCreatePinnedToCore(speaker_task, "speaker", 4096, NULL, 5, &s_task_handle, 1);
    ESP_LOGI(TAG, "Speaker started");
    return ESP_OK;
}

void speaker_task_stop(void)
{
    gpio_set_level(PA_CTRL_GPIO, 0);
    if (s_task_handle) { vTaskDelete(s_task_handle); s_task_handle = NULL; }
    if (s_tx_chan) { i2s_channel_disable(s_tx_chan); i2s_del_channel(s_tx_chan); s_tx_chan = NULL; }
    if (s_ringbuf) { vRingbufferDelete(s_ringbuf); s_ringbuf = NULL; }
}

void speaker_feed_data(const uint8_t *pcm, size_t len)
{
    if (!s_ringbuf) return;
    xRingbufferSend(s_ringbuf, pcm, len, 0);
}

// ============================================================
// Other targets: stubs
// ============================================================
#else

esp_err_t speaker_task_start(void)
{
    ESP_LOGI(TAG, "Speaker task skipped (no onboard amp)");
    return ESP_OK;
}

void speaker_task_stop(void) {}
void speaker_feed_data(const uint8_t *pcm, size_t len) { (void)pcm; (void)len; }

#endif
