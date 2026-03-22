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
// ESP32-P4 (MIPI-CSI + ISP + HW JPEG)
// ============================================================
#elif CONFIG_IDF_TARGET_ESP32P4

#include <string.h>
#include "esp_cam_ctlr.h"
#include "esp_cam_ctlr_csi.h"
#include "esp_cam_sensor.h"
#include "esp_cam_sensor_detect.h"
#include "esp_sccb_intf.h"
#include "esp_sccb_i2c.h"
#include "driver/i2c_master.h"
#include "driver/isp.h"
#include "driver/jpeg_encode.h"
#include "esp_ldo_regulator.h"
#include "esp_cache.h"
#include "esp_heap_caps.h"
#include "esp_cam_sensor_xclk.h"

// Camera sensor SCCB (I2C) — shared with board I2C header
#define CAM_SCCB_SCL_IO     8
#define CAM_SCCB_SDA_IO     7
#define CAM_SCCB_FREQ_HZ    (10 * 1000)

// MIPI PHY LDO
#define MIPI_LDO_CHAN_ID     3
#define MIPI_LDO_VOLTAGE_MV  2500

// CSI
#define CSI_LANE_BITRATE_MBPS  200
#define CSI_DATA_LANE_NUM      2

// Buffer sizes
#define FRAME_BUF_SIZE  (CAM_WIDTH * CAM_HEIGHT * 2)  // RGB565
#define JPEG_BUF_SIZE   (CAM_WIDTH * CAM_HEIGHT)      // max JPEG output

static esp_cam_ctlr_handle_t s_cam_handle = NULL;
static isp_proc_handle_t s_isp_handle = NULL;
static jpeg_encoder_handle_t s_jpeg_handle = NULL;
static uint8_t *s_frame_buf[2] = {NULL, NULL};  // double buffer
static int s_buf_idx = 0;                        // current capture buffer
static uint8_t *s_jpeg_buf = NULL;
static bool s_camera_ready = false;
static esp_cam_sensor_device_t *s_cam_sensor = NULL;

// Software AE state
static uint16_t s_exposure = 0x0300;  // initial exposure
static uint8_t  s_gain = 0x40;        // initial gain

// ISR callback: provide same buffer for next frame
static bool IRAM_ATTR on_get_new_trans(esp_cam_ctlr_handle_t handle,
                                       esp_cam_ctlr_trans_t *trans,
                                       void *user_data)
{
    esp_cam_ctlr_trans_t *def = (esp_cam_ctlr_trans_t *)user_data;
    trans->buffer = def->buffer;
    trans->buflen = def->buflen;
    return false;
}

static SemaphoreHandle_t s_frame_sem = NULL;

static bool IRAM_ATTR on_trans_finished(esp_cam_ctlr_handle_t handle,
                                        esp_cam_ctlr_trans_t *trans,
                                        void *user_data)
{
    BaseType_t woken = pdFALSE;
    if (s_frame_sem) {
        xSemaphoreGiveFromISR(s_frame_sem, &woken);
    }
    return woken == pdTRUE;
}

static esp_err_t init_camera(void)
{
    // ---- MIPI PHY LDO ----
    esp_ldo_channel_handle_t ldo_mipi_phy = NULL;
    esp_ldo_channel_config_t ldo_cfg = {
        .chan_id = MIPI_LDO_CHAN_ID,
        .voltage_mv = MIPI_LDO_VOLTAGE_MV,
    };
    ESP_ERROR_CHECK(esp_ldo_acquire_channel(&ldo_cfg, &ldo_mipi_phy));

    // ---- I2C bus for camera SCCB ----
    i2c_master_bus_config_t i2c_bus_conf = {
        .clk_source = I2C_CLK_SRC_DEFAULT,
        .sda_io_num = CAM_SCCB_SDA_IO,
        .scl_io_num = CAM_SCCB_SCL_IO,
        .i2c_port = I2C_NUM_1,  // NUM_0 is used by ES8311 codec
        .flags.enable_internal_pullup = true,
    };
    i2c_master_bus_handle_t i2c_bus = NULL;
    ESP_ERROR_CHECK(i2c_new_master_bus(&i2c_bus_conf, &i2c_bus));

    // ---- Auto-detect camera sensor ----
    esp_cam_sensor_config_t cam_config = {
        .reset_pin = -1,
        .pwdn_pin = -1,
        .xclk_pin = -1,
    };

    esp_cam_sensor_device_t *cam = NULL;
    for (esp_cam_sensor_detect_fn_t *p = &__esp_cam_sensor_detect_fn_array_start;
         p < &__esp_cam_sensor_detect_fn_array_end; ++p) {
        sccb_i2c_config_t i2c_config = {
            .scl_speed_hz = CAM_SCCB_FREQ_HZ,
            .device_address = p->sccb_addr,
            .dev_addr_length = I2C_ADDR_BIT_LEN_7,
        };
        ESP_ERROR_CHECK(sccb_new_i2c_io(i2c_bus, &i2c_config, &cam_config.sccb_handle));

        cam_config.sensor_port = p->port;
        cam = (*(p->detect))(&cam_config);
        if (cam) {
            if (p->port != ESP_CAM_SENSOR_MIPI_CSI) {
                ESP_LOGE(TAG, "Detected sensor is not MIPI-CSI");
                return ESP_ERR_NOT_SUPPORTED;
            }
            break;
        }
        esp_sccb_del_i2c_io(cam_config.sccb_handle);
    }

    if (!cam) {
        ESP_LOGW(TAG, "No camera sensor detected — camera disabled");
        return ESP_OK;  // non-fatal
    }

    // ---- Select format ----
    esp_cam_sensor_format_array_t fmt_array = {0};
    esp_cam_sensor_query_format(cam, &fmt_array);

    const esp_cam_sensor_format_t *selected_fmt = NULL;
    for (int i = 0; i < fmt_array.count; i++) {
        ESP_LOGI(TAG, "  sensor format[%d]: %s", i, fmt_array.format_array[i].name);
        // Prefer RAW8 800x640 (closest to CAM_WIDTH x CAM_HEIGHT)
        if (!selected_fmt && strstr(fmt_array.format_array[i].name, "RAW8_800x640")) {
            selected_fmt = &fmt_array.format_array[i];
        }
    }
    // Fallback: any RAW8 format
    if (!selected_fmt) {
        for (int i = 0; i < fmt_array.count; i++) {
            if (strstr(fmt_array.format_array[i].name, "RAW8")) {
                selected_fmt = &fmt_array.format_array[i];
                break;
            }
        }
    }
    if (!selected_fmt && fmt_array.count > 0) {
        selected_fmt = &fmt_array.format_array[0];
    }
    if (!selected_fmt) {
        ESP_LOGE(TAG, "No compatible sensor format");
        return ESP_ERR_NOT_SUPPORTED;
    }

    ESP_LOGI(TAG, "Using format: %s", selected_fmt->name);
    ESP_ERROR_CHECK(esp_cam_sensor_set_format(cam, selected_fmt));

    s_cam_sensor = cam;  // store early for use in camera_task

    // NOTE: stream enable is deferred until after CSI controller starts

    // ---- CSI controller ----
    esp_cam_ctlr_csi_config_t csi_cfg = {
        .ctlr_id = 0,
        .h_res = CAM_WIDTH,
        .v_res = CAM_HEIGHT,
        .data_lane_num = CSI_DATA_LANE_NUM,
        .lane_bit_rate_mbps = CSI_LANE_BITRATE_MBPS,
        .input_data_color_type = CAM_CTLR_COLOR_RAW8,
        .output_data_color_type = CAM_CTLR_COLOR_RGB565,
        .byte_swap_en = false,
        .queue_items = 2,
    };
    ESP_ERROR_CHECK(esp_cam_new_csi_ctlr(&csi_cfg, &s_cam_handle));

    // Allocate frame buffer via controller (handles alignment)
    s_frame_buf[0] = esp_cam_ctlr_alloc_buffer(s_cam_handle, FRAME_BUF_SIZE,
                                                MALLOC_CAP_SPIRAM | MALLOC_CAP_DMA);
    if (!s_frame_buf[0]) {
        ESP_LOGE(TAG, "Frame buffer alloc failed");
        return ESP_ERR_NO_MEM;
    }

    static esp_cam_ctlr_trans_t s_default_trans;
    s_default_trans.buffer = s_frame_buf[0];
    s_default_trans.buflen = FRAME_BUF_SIZE;

    esp_cam_ctlr_evt_cbs_t cbs = {
        .on_get_new_trans = on_get_new_trans,
        .on_trans_finished = on_trans_finished,
    };
    ESP_ERROR_CHECK(esp_cam_ctlr_register_event_callbacks(s_cam_handle, &cbs, &s_default_trans));
    ESP_ERROR_CHECK(esp_cam_ctlr_enable(s_cam_handle));

    // ---- ISP (RAW8 → RGB565) ----
    esp_isp_processor_cfg_t isp_cfg = {
        .clk_hz = 80 * 1000 * 1000,
        .input_data_source = ISP_INPUT_DATA_SOURCE_CSI,
        .input_data_color_type = ISP_COLOR_RAW8,
        .output_data_color_type = ISP_COLOR_RGB565,
        .bayer_order = COLOR_RAW_ELEMENT_ORDER_GBRG,  // OV5647 bayer pattern
        .has_line_start_packet = false,
        .has_line_end_packet = false,
        .h_res = CAM_WIDTH,
        .v_res = CAM_HEIGHT,
    };
    ESP_ERROR_CHECK(esp_isp_new_processor(&isp_cfg, &s_isp_handle));
    ESP_ERROR_CHECK(esp_isp_enable(s_isp_handle));

    // Enable demosaic for proper color reconstruction
    esp_isp_demosaic_config_t demosaic_cfg = {
        .grad_ratio = {
            .integer = 2,
            .decimal = 5,
        },
    };
    ESP_ERROR_CHECK(esp_isp_demosaic_configure(s_isp_handle, &demosaic_cfg));
    ESP_ERROR_CHECK(esp_isp_demosaic_enable(s_isp_handle));

    // ---- JPEG encoder ----
    jpeg_encode_engine_cfg_t jpeg_eng_cfg = {
        .timeout_ms = 1000,
    };
    ESP_ERROR_CHECK(jpeg_new_encoder_engine(&jpeg_eng_cfg, &s_jpeg_handle));

    // Allocate JPEG output buffer
    jpeg_encode_memory_alloc_cfg_t jpeg_mem_cfg = {
        .buffer_direction = JPEG_ENC_ALLOC_OUTPUT_BUFFER,
    };
    size_t jpeg_alloc_size = 0;
    s_jpeg_buf = jpeg_alloc_encoder_mem(JPEG_BUF_SIZE, &jpeg_mem_cfg, &jpeg_alloc_size);
    if (!s_jpeg_buf) {
        ESP_LOGE(TAG, "JPEG buffer alloc failed");
        return ESP_ERR_NO_MEM;
    }

    // ---- Start CSI then enable sensor streaming ----
    ESP_ERROR_CHECK(esp_cam_ctlr_start(s_cam_handle));

    int stream_enable = 1;
    ESP_ERROR_CHECK(esp_cam_sensor_ioctl(cam, ESP_CAM_SENSOR_IOC_S_STREAM, &stream_enable));

    // ---- Set OV5647 manual exposure + gain via group hold ----
    // The 800x640 format does NOT init exposure/gain registers.
    // Use OV5647 group hold (0x3212) to latch changes atomically.
    esp_cam_sensor_reg_val_t reg;

    // Start group hold
    reg.regaddr = 0x3212; reg.value = 0x00;
    esp_cam_sensor_ioctl(cam, ESP_CAM_SENSOR_IOC_S_REG, &reg);

    // Manual AEC + AGC + delay bits
    reg.regaddr = 0x3503; reg.value = 0x63;
    esp_cam_sensor_ioctl(cam, ESP_CAM_SENSOR_IOC_S_REG, &reg);

    // Exposure: moderate initial value (s_exposure=0x0300)
    reg.regaddr = 0x3500; reg.value = (s_exposure >> 12) & 0x0F;
    esp_cam_sensor_ioctl(cam, ESP_CAM_SENSOR_IOC_S_REG, &reg);
    reg.regaddr = 0x3501; reg.value = (s_exposure >> 4) & 0xFF;
    esp_cam_sensor_ioctl(cam, ESP_CAM_SENSOR_IOC_S_REG, &reg);
    reg.regaddr = 0x3502; reg.value = (s_exposure & 0x0F) << 4;
    esp_cam_sensor_ioctl(cam, ESP_CAM_SENSOR_IOC_S_REG, &reg);

    // Gain: moderate initial value (s_gain=0x40)
    reg.regaddr = 0x350a; reg.value = 0x00;
    esp_cam_sensor_ioctl(cam, ESP_CAM_SENSOR_IOC_S_REG, &reg);
    reg.regaddr = 0x350b; reg.value = s_gain;
    esp_cam_sensor_ioctl(cam, ESP_CAM_SENSOR_IOC_S_REG, &reg);

    // End group hold + launch
    reg.regaddr = 0x3212; reg.value = 0x10;
    esp_cam_sensor_ioctl(cam, ESP_CAM_SENSOR_IOC_S_REG, &reg);
    reg.regaddr = 0x3212; reg.value = 0xA0;
    esp_cam_sensor_ioctl(cam, ESP_CAM_SENSOR_IOC_S_REG, &reg);

    ESP_LOGI(TAG, "OV5647 exposure+gain set via group hold");

    s_camera_ready = true;
    ESP_LOGI(TAG, "MIPI-CSI camera initialized (%dx%d)", CAM_WIDTH, CAM_HEIGHT);
    return ESP_OK;
}

// ---- Simple software AE ----
#define AE_TARGET     110              // target brightness (0-255)
#define AE_TOLERANCE   20              // acceptable range around target

static int measure_brightness(const uint8_t *buf, size_t len)
{
    // Sample every 256th pixel for speed (RGB565: 2 bytes per pixel)
    uint32_t sum = 0;
    int count = 0;
    for (size_t i = 0; i + 1 < len; i += 512) {
        // RGB565: RRRRRGGG GGGBBBBB → approximate luminance
        uint16_t px = (uint16_t)buf[i] | ((uint16_t)buf[i + 1] << 8);
        uint8_t r = (px >> 11) & 0x1F;
        uint8_t g = (px >> 5) & 0x3F;
        uint8_t b = px & 0x1F;
        // Rough luminance (scale to 0-255)
        sum += (r * 8 * 77 + g * 4 * 150 + b * 8 * 29) >> 8;
        count++;
    }
    return count > 0 ? (int)(sum / count) : 0;
}

static void adjust_exposure(int brightness)
{
    if (!s_cam_sensor) return;

    int error = AE_TARGET - brightness;
    if (error > -AE_TOLERANCE && error < AE_TOLERANCE) return;

    // Adjust exposure first, then gain
    if (error > 0) {
        // Too dark: increase
        if (s_exposure < 0x0900) {
            s_exposure = s_exposure + (s_exposure >> 2) + 1;  // +25%
            if (s_exposure > 0x0900) s_exposure = 0x0900;
        } else if (s_gain < 0xFF) {
            s_gain = s_gain + (s_gain >> 3) + 1;  // +12.5%
            if (s_gain > 0xFF) s_gain = 0xFF;
        }
    } else {
        // Too bright: decrease
        if (s_gain > 0x10) {
            int g = (int)s_gain - (s_gain >> 3) - 1;
            s_gain = (g > 0x10) ? (uint8_t)g : 0x10;
        } else if (s_exposure > 0x0010) {
            s_exposure = s_exposure - (s_exposure >> 2) - 1;
            if (s_exposure < 0x0010) s_exposure = 0x0010;
        }
    }

    // Apply via group hold
    esp_cam_sensor_reg_val_t reg;

    reg.regaddr = 0x3212; reg.value = 0x00;
    esp_cam_sensor_ioctl(s_cam_sensor, ESP_CAM_SENSOR_IOC_S_REG, &reg);

    reg.regaddr = 0x3500; reg.value = (s_exposure >> 12) & 0x0F;
    esp_cam_sensor_ioctl(s_cam_sensor, ESP_CAM_SENSOR_IOC_S_REG, &reg);
    reg.regaddr = 0x3501; reg.value = (s_exposure >> 4) & 0xFF;
    esp_cam_sensor_ioctl(s_cam_sensor, ESP_CAM_SENSOR_IOC_S_REG, &reg);
    reg.regaddr = 0x3502; reg.value = (s_exposure & 0x0F) << 4;
    esp_cam_sensor_ioctl(s_cam_sensor, ESP_CAM_SENSOR_IOC_S_REG, &reg);

    reg.regaddr = 0x350a; reg.value = 0x00;
    esp_cam_sensor_ioctl(s_cam_sensor, ESP_CAM_SENSOR_IOC_S_REG, &reg);
    reg.regaddr = 0x350b; reg.value = s_gain;
    esp_cam_sensor_ioctl(s_cam_sensor, ESP_CAM_SENSOR_IOC_S_REG, &reg);

    reg.regaddr = 0x3212; reg.value = 0x10;
    esp_cam_sensor_ioctl(s_cam_sensor, ESP_CAM_SENSOR_IOC_S_REG, &reg);
    reg.regaddr = 0x3212; reg.value = 0xA0;
    esp_cam_sensor_ioctl(s_cam_sensor, ESP_CAM_SENSOR_IOC_S_REG, &reg);
}

static void encode_and_send_buf(uint8_t *buf)
{
    // Sync cache (PSRAM frame data → CPU)
    esp_cache_msync(buf, FRAME_BUF_SIZE, ESP_CACHE_MSYNC_FLAG_DIR_M2C);

    // Software AE: measure brightness and adjust for next frame
    int brightness = measure_brightness(buf, FRAME_BUF_SIZE);
    adjust_exposure(brightness);

    // JPEG encode
    jpeg_encode_cfg_t enc_cfg = {
        .width = CAM_WIDTH,
        .height = CAM_HEIGHT,
        .src_type = JPEG_ENCODE_IN_FORMAT_RGB565,
        .sub_sample = JPEG_DOWN_SAMPLING_YUV422,
        .image_quality = 80,
    };
    uint32_t jpeg_size = 0;
    esp_err_t ret = jpeg_encoder_process(s_jpeg_handle, &enc_cfg,
                                         buf, FRAME_BUF_SIZE,
                                         s_jpeg_buf, JPEG_BUF_SIZE,
                                         &jpeg_size);
    if (ret != ESP_OK) {
        ESP_LOGW(TAG, "JPEG encode failed: %s", esp_err_to_name(ret));
        return;
    }

    if (ws_client_is_connected()) {
        ws_client_send_binary(FRAME_TYPE_IMAGE, s_jpeg_buf, jpeg_size);
    }
}

// ============================================================
// Unsupported target
// ============================================================
#else

static esp_err_t init_camera(void) { return ESP_OK; }
static void capture_and_send(void) {}

#endif

// ============================================================
// Common task code
// ============================================================

#if CONFIG_IDF_TARGET_ESP32P4

// P4: use on_trans_finished semaphore instead of esp_cam_ctlr_receive
static void camera_task(void *arg)
{
    if (!s_camera_ready) {
        ESP_LOGW(TAG, "Camera not ready, task idle");
        while (true) { vTaskDelay(pdMS_TO_TICKS(1000)); }
    }

    s_frame_sem = xSemaphoreCreateBinary();

    while (true) {
        // Wait for frame from CSI (signaled by on_trans_finished ISR)
        if (xSemaphoreTake(s_frame_sem, pdMS_TO_TICKS(1000)) != pdTRUE) {
            continue;
        }

        if (s_capture_requested) {
            s_capture_requested = false;
            encode_and_send_buf(s_frame_buf[0]);
        }
    }
    vTaskDelete(NULL);
}

#else

// ESP32-CAM: on-demand capture
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

#endif

esp_err_t camera_task_start(void)
{
    esp_err_t ret = init_camera();
    if (ret != ESP_OK) {
        return ret;
    }

    BaseType_t created = xTaskCreatePinnedToCore(
        camera_task, "camera", 8192, NULL, 4, &s_task_handle, 0);

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
