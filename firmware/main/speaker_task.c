#include "speaker_task.h"
#include "config.h"
#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include <string.h>
#include <math.h>

static const char *TAG = "speaker";

// ============================================================
// ESP32-P4-NANO: onboard I2S speaker + PA
// ============================================================
#if CONFIG_IDF_TARGET_ESP32P4

#include "freertos/ringbuf.h"
#include "driver/i2s_std.h"
#include "driver/gpio.h"

// Ring buffer: ~5 seconds of stereo audio
#define RINGBUF_SIZE (SPEAKER_SAMPLE_RATE * (SPEAKER_BITS / 8) * 2 * 5)

static i2s_chan_handle_t s_tx_chan = NULL;
static RingbufHandle_t s_ringbuf = NULL;
static TaskHandle_t s_task_handle = NULL;
static volatile bool s_playing = false;

void speaker_set_i2s_tx(i2s_chan_handle_t tx_chan)
{
    s_tx_chan = tx_chan;
}

static void play_test_tone(void)
{
    // 440Hz sine wave, 0.3s, at SPEAKER_SAMPLE_RATE
    const int duration_ms = 200;
    const int freq = 440;
    const int num_samples = SPEAKER_SAMPLE_RATE * duration_ms / 1000;
    const int amplitude = 32000;  // max volume

    // Stereo interleaved: L, R, L, R, ...
    int16_t *tone = malloc(num_samples * 2 * sizeof(int16_t));
    if (!tone) return;

    for (int i = 0; i < num_samples; i++) {
        int16_t val = (int16_t)(amplitude * sinf(2.0f * M_PI * freq * i / SPEAKER_SAMPLE_RATE));
        tone[i * 2]     = val;  // L
        tone[i * 2 + 1] = val;  // R
    }

    gpio_set_level(PA_CTRL_GPIO, 1);
    vTaskDelay(pdMS_TO_TICKS(50));

    size_t written = 0;
    i2s_channel_write(s_tx_chan, tone, num_samples * 2 * sizeof(int16_t), &written, pdMS_TO_TICKS(3000));

    vTaskDelay(pdMS_TO_TICKS(100));
    gpio_set_level(PA_CTRL_GPIO, 0);

    free(tone);
    ESP_LOGI(TAG, "Test tone played (%dHz, %dms)", freq, duration_ms);
}

static void speaker_task(void *arg)
{
    const size_t chunk = 1024;
    while (true) {
        size_t item_size = 0;
        void *data = xRingbufferReceiveUpTo(s_ringbuf, &item_size, pdMS_TO_TICKS(100), chunk);
        if (data && item_size > 0) {
            s_playing = true;
            gpio_set_level(PA_CTRL_GPIO, 1);
            size_t written = 0;
            i2s_channel_write(s_tx_chan, data, item_size, &written, pdMS_TO_TICKS(500));
            vRingbufferReturnItem(s_ringbuf, data);
        } else {
            if (s_playing) {
                // Keep PA on briefly for tail audio, then turn off
                vTaskDelay(pdMS_TO_TICKS(50));
                gpio_set_level(PA_CTRL_GPIO, 0);
                s_playing = false;
            }
        }
    }
    vTaskDelete(NULL);
}

esp_err_t speaker_task_start(void)
{
    if (!s_tx_chan) {
        ESP_LOGE(TAG, "I2S TX not set — call audio_task_start first");
        return ESP_ERR_INVALID_STATE;
    }

    // PA control GPIO
    gpio_config_t pa_cfg = {
        .pin_bit_mask = (1ULL << PA_CTRL_GPIO),
        .mode = GPIO_MODE_OUTPUT,
    };
    ESP_ERROR_CHECK(gpio_config(&pa_cfg));
    gpio_set_level(PA_CTRL_GPIO, 0);

    // Play test tone to verify speaker works
    play_test_tone();

    // Ring buffer for TTS data
    s_ringbuf = xRingbufferCreate(RINGBUF_SIZE, RINGBUF_TYPE_BYTEBUF);
    if (!s_ringbuf) {
        return ESP_ERR_NO_MEM;
    }

    xTaskCreatePinnedToCore(speaker_task, "speaker", 4096, NULL, 5, &s_task_handle, 1);
    ESP_LOGI(TAG, "Speaker started (sample rate: %d Hz)", SPEAKER_SAMPLE_RATE);
    return ESP_OK;
}

bool speaker_is_playing(void)
{
    return s_playing;
}

void speaker_task_stop(void)
{
    gpio_set_level(PA_CTRL_GPIO, 0);
    if (s_task_handle) { vTaskDelete(s_task_handle); s_task_handle = NULL; }
    // TX channel is owned by audio_task, don't delete here
    s_tx_chan = NULL;
    if (s_ringbuf) { vRingbufferDelete(s_ringbuf); s_ringbuf = NULL; }
}

void speaker_feed_data(const uint8_t *pcm, size_t len)
{
    if (!s_ringbuf) return;

    // Input is mono 16-bit PCM, I2S bus is stereo — duplicate to L+R
    size_t mono_samples = len / 2;
    size_t stereo_bytes = mono_samples * 4;
    int16_t *stereo = malloc(stereo_bytes);
    if (!stereo) return;

    const int16_t *mono = (const int16_t *)pcm;
    for (size_t i = 0; i < mono_samples; i++) {
        stereo[i * 2]     = mono[i];  // L
        stereo[i * 2 + 1] = mono[i];  // R
    }

    xRingbufferSend(s_ringbuf, stereo, stereo_bytes, pdMS_TO_TICKS(100));
    free(stereo);
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
