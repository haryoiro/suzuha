#include "audio_task.h"
#include "speaker_task.h"
#include "ws_client.h"
#include "config.h"
#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include <string.h>

static const char *TAG = "audio";

// ============================================================
// ESP32-P4-NANO: onboard I2S mic + speaker (full-duplex)
// ============================================================
#if CONFIG_IDF_TARGET_ESP32P4

#include "driver/i2s_std.h"
#include "driver/i2c_master.h"
#include "esp_heap_caps.h"
#include "nvs_flash.h"
#include "nvs.h"

#define MCLK_MULTIPLE   512
#define ES8311_ADDR     0x18

#define CHUNK_SAMPLES   (AUDIO_SAMPLE_RATE * AUDIO_CHUNK_MS / 1000)
#define CHUNK_BYTES     (CHUNK_SAMPLES * (AUDIO_BITS / 8) * AUDIO_CHANNELS)

static TaskHandle_t s_task_handle = NULL;
static volatile bool s_streaming = false;
static i2s_chan_handle_t s_rx_chan = NULL;
static i2s_chan_handle_t s_tx_chan = NULL;

// Direct ES8311 register write via new I2C master API
static i2c_master_bus_handle_t s_i2c_bus = NULL;
static i2c_master_dev_handle_t s_es8311_dev = NULL;

static esp_err_t es8311_write_reg(uint8_t reg, uint8_t val)
{
    uint8_t buf[2] = {reg, val};
    return i2c_master_transmit(s_es8311_dev, buf, 2, 100);
}

static esp_err_t init_es8311_codec(void)
{
    // I2C bus (shared with camera later)
    i2c_master_bus_config_t bus_cfg = {
        .clk_source = I2C_CLK_SRC_DEFAULT,
        .i2c_port = I2C_NUM_0,
        .sda_io_num = GPIO_NUM_7,
        .scl_io_num = GPIO_NUM_8,
        .flags.enable_internal_pullup = true,
    };
    ESP_ERROR_CHECK(i2c_new_master_bus(&bus_cfg, &s_i2c_bus));

    i2c_device_config_t dev_cfg = {
        .dev_addr_length = I2C_ADDR_BIT_LEN_7,
        .device_address = ES8311_ADDR,
        .scl_speed_hz = 100000,
    };
    ESP_ERROR_CHECK(i2c_master_bus_add_device(s_i2c_bus, &dev_cfg, &s_es8311_dev));

    // Verify ES8311 is present by reading chip ID (reg 0xFD)
    uint8_t reg = 0xFD;
    uint8_t chip_id = 0;
    esp_err_t probe = i2c_master_transmit_receive(s_es8311_dev, &reg, 1, &chip_id, 1, 100);
    ESP_LOGI(TAG, "ES8311 probe: ret=%s, chip_id=0x%02X (expect 0x83)", esp_err_to_name(probe), chip_id);
    if (probe != ESP_OK) {
        ESP_LOGW(TAG, "ES8311 not found — skipping codec init");
        i2c_master_bus_rm_device(s_es8311_dev);
        i2c_del_master_bus(s_i2c_bus);
        s_es8311_dev = NULL;
        s_i2c_bus = NULL;
        return ESP_OK;  // non-fatal
    }

    // ---- ES8311 init (from esp-bsp driver, exact register sequence) ----

    // 1. Reset
    es8311_write_reg(0x00, 0x1F);  // Reset
    vTaskDelay(pdMS_TO_TICKS(20));
    es8311_write_reg(0x00, 0x00);  // Clear reset
    es8311_write_reg(0x00, 0x80);  // Power-on

    // 2. Clock config: MCLK from pin, 12.288MHz, 24kHz sample rate
    //    coeff_div entry: {12288000, 24000, pre_div=2, pre_multi=0,
    //      adc_div=1, dac_div=1, fs_mode=0, lrck_h=0, lrck_l=0xFF,
    //      bclk_div=4, adc_osr=0x10, dac_osr=0x10}
    es8311_write_reg(0x01, 0x3F);  // Enable all clocks, MCLK from MCLK pin

    // reg02: (pre_div-1)<<5 | pre_multi<<3 | lower bits preserved
    es8311_write_reg(0x02, (1 << 5) | (0 << 3));  // pre_div=2→(2-1)=1, pre_multi=0

    // reg03: (fs_mode<<6) | adc_osr
    es8311_write_reg(0x03, (0 << 6) | 0x10);  // fs_mode=0, adc_osr=0x10

    // reg04: dac_osr
    es8311_write_reg(0x04, 0x10);  // dac_osr=0x10

    // reg05: (adc_div-1)<<4 | (dac_div-1)
    es8311_write_reg(0x05, (0 << 4) | 0);  // adc_div=1, dac_div=1

    // reg06: bclk_div (4-1=3), sclk not inverted
    es8311_write_reg(0x06, 0x03);  // bclk_div=4→3

    // reg07: lrck_h
    es8311_write_reg(0x07, 0x00);  // lrck_h=0

    // reg08: lrck_l
    es8311_write_reg(0x08, 0xFF);  // lrck_l=0xFF

    // 3. SDP format: 16-bit I2S (Philips)
    //    reg09 bits: [7:6]=00 I2S, [5:4]=00 32bit→[5:4]=11 16-bit, [3:2]=data length
    es8311_write_reg(0x09, 0x0C);  // SDP IN: I2S, 16-bit
    es8311_write_reg(0x0A, 0x0C);  // SDP OUT: I2S, 16-bit

    // 4. Power up analog + DAC
    es8311_write_reg(0x0D, 0x01);  // Power up analog circuitry
    es8311_write_reg(0x0E, 0x02);  // Enable analog PGA, ADC modulator
    es8311_write_reg(0x12, 0x00);  // Power up DAC
    es8311_write_reg(0x13, 0x10);  // Enable output to HP drive

    // 5. ADC/DAC equalizer
    es8311_write_reg(0x1C, 0x6A);  // ADC EQ bypass, DC offset cancel
    es8311_write_reg(0x37, 0x08);  // DAC EQ bypass

    // 6. Volume (restore from NVS, default 50%) and mic
    {
        nvs_handle_t nvs;
        int32_t saved_vol = 50;
        if (nvs_open("audio", NVS_READONLY, &nvs) == ESP_OK) {
            nvs_get_i32(nvs, "volume", &saved_vol);
            nvs_close(nvs);
        }
        uint8_t reg_val = (saved_vol == 0) ? 0 : (uint8_t)((saved_vol * 256 / 100) - 1);
        es8311_write_reg(0x32, reg_val);
        ESP_LOGI(TAG, "Volume restored: %ld%%", (long)saved_vol);
    }
    es8311_write_reg(0x14, 0x1A);  // Enable analog mic
    es8311_write_reg(0x17, 0xC8);  // ADC gain

    // Keep I2C alive for dynamic volume control
    // Camera will use a separate I2C bus instance

    ESP_LOGI(TAG, "ES8311 codec initialized (vol=50%%)");
    return ESP_OK;
}

void audio_set_volume(int percent)
{
    if (percent < 0) percent = 0;
    if (percent > 100) percent = 100;

    if (!s_es8311_dev) {
        ESP_LOGW(TAG, "ES8311 not initialized");
        return;
    }

    uint8_t reg_val = (percent == 0) ? 0 : (uint8_t)((percent * 256 / 100) - 1);
    es8311_write_reg(0x32, reg_val);

    // Persist to NVS
    nvs_handle_t nvs;
    if (nvs_open("audio", NVS_READWRITE, &nvs) == ESP_OK) {
        nvs_set_i32(nvs, "volume", percent);
        nvs_commit(nvs);
        nvs_close(nvs);
    }

    ESP_LOGI(TAG, "Volume set to %d%% (saved)", percent);
}

static esp_err_t init_i2s_full_duplex(void)
{
    // Create BOTH TX and RX on the same I2S port (full-duplex, shared clocks)
    // NOTE: I2S must start BEFORE ES8311 init (codec needs MCLK to lock PLL)
    i2s_chan_config_t chan_cfg = I2S_CHANNEL_DEFAULT_CONFIG(I2S_NUM_0, I2S_ROLE_MASTER);
    chan_cfg.auto_clear = true;
    ESP_ERROR_CHECK(i2s_new_channel(&chan_cfg, &s_tx_chan, &s_rx_chan));

    i2s_std_config_t std_cfg = {
        .clk_cfg = I2S_STD_CLK_DEFAULT_CONFIG(AUDIO_SAMPLE_RATE),
        .slot_cfg = I2S_STD_PHILIPS_SLOT_DEFAULT_CONFIG(I2S_DATA_BIT_WIDTH_16BIT, I2S_SLOT_MODE_STEREO),
        .gpio_cfg = {
            .mclk = GPIO_NUM_13,
            .bclk = GPIO_NUM_12,
            .ws   = GPIO_NUM_10,
            .dout = GPIO_NUM_9,   // speaker (DSDIN on board = input to codec)
            .din  = GPIO_NUM_11,  // mic (ASDOUT on board = output from codec)
            .invert_flags = {
                .mclk_inv = false,
                .bclk_inv = false,
                .ws_inv   = false,
            },
        },
    };
    std_cfg.clk_cfg.mclk_multiple = MCLK_MULTIPLE;

    ESP_ERROR_CHECK(i2s_channel_init_std_mode(s_rx_chan, &std_cfg));
    ESP_ERROR_CHECK(i2s_channel_init_std_mode(s_tx_chan, &std_cfg));
    ESP_ERROR_CHECK(i2s_channel_enable(s_rx_chan));
    ESP_ERROR_CHECK(i2s_channel_enable(s_tx_chan));

    // Now MCLK is running — init ES8311 codec
    ESP_ERROR_CHECK(init_es8311_codec());

    // Give TX handle to speaker task
    speaker_set_i2s_tx(s_tx_chan);

    return ESP_OK;
}

static void audio_task(void *arg)
{
    // Read buffer: stereo (L+R interleaved, 2 samples per frame)
    const size_t stereo_bytes = CHUNK_BYTES * 2;  // stereo is 2x mono
    uint8_t *buf = heap_caps_malloc(stereo_bytes, MALLOC_CAP_SPIRAM | MALLOC_CAP_8BIT);
    // Mono output buffer (extract one channel)
    uint8_t *mono = heap_caps_malloc(CHUNK_BYTES, MALLOC_CAP_SPIRAM | MALLOC_CAP_8BIT);
    if (!buf || !mono) {
        ESP_LOGE(TAG, "Failed to allocate audio buffer");
        vTaskDelete(NULL);
        return;
    }

    size_t bytes_read = 0;

    while (true) {
        if (!s_streaming || speaker_is_playing()) {
            vTaskDelay(pdMS_TO_TICKS(100));
            continue;
        }

        esp_err_t ret = i2s_channel_read(s_rx_chan, buf, stereo_bytes, &bytes_read,
                                          pdMS_TO_TICKS(1000));
        if (ret != ESP_OK || bytes_read == 0) {
            continue;
        }

        // Convert stereo to mono: take left channel only
        int frames = bytes_read / 4;  // 4 bytes per stereo frame (2x int16)
        int16_t *stereo_samples = (int16_t *)buf;
        int16_t *mono_samples = (int16_t *)mono;
        for (int i = 0; i < frames; i++) {
            mono_samples[i] = stereo_samples[i * 2];  // left channel
        }
        size_t mono_bytes = frames * 2;

        if (ws_client_is_connected()) {
            ws_client_send_binary(FRAME_TYPE_AUDIO, mono, mono_bytes);
        }
    }

    free(buf);
    vTaskDelete(NULL);
}

esp_err_t audio_task_start(void)
{
    esp_err_t ret = init_i2s_full_duplex();
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
    if (s_tx_chan) {
        i2s_channel_disable(s_tx_chan);
        i2s_del_channel(s_tx_chan);
        s_tx_chan = NULL;
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
