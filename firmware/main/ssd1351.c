#include "ssd1351.h"
#include "config.h"
#include "esp_log.h"
#include "esp_heap_caps.h"
#include "driver/spi_master.h"
#include "driver/gpio.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include <string.h>

static const char *TAG = "ssd1351";

static spi_device_handle_t s_spi;
static uint16_t *s_fb = NULL;

#define FB_SIZE (SSD1351_WIDTH * SSD1351_HEIGHT * sizeof(uint16_t))

// ---- low-level SPI helpers ----

static void cmd(uint8_t c)
{
    gpio_set_level(SSD1351_DC_GPIO, 0);
    spi_transaction_t t = {
        .length = 8,
        .tx_data = {c},
        .flags = SPI_TRANS_USE_TXDATA,
    };
    spi_device_polling_transmit(s_spi, &t);
}

static void data_bytes(const uint8_t *d, size_t len)
{
    if (!len) return;
    gpio_set_level(SSD1351_DC_GPIO, 1);
    spi_transaction_t t = {
        .length = len * 8,
        .tx_buffer = d,
    };
    spi_device_polling_transmit(s_spi, &t);
}

static void data1(uint8_t b)
{
    gpio_set_level(SSD1351_DC_GPIO, 1);
    spi_transaction_t t = {
        .length = 8,
        .tx_data = {b},
        .flags = SPI_TRANS_USE_TXDATA,
    };
    spi_device_polling_transmit(s_spi, &t);
}

// ---- public API ----

esp_err_t ssd1351_init(void)
{
    // DMA-capable framebuffer in internal SRAM
    s_fb = heap_caps_malloc(FB_SIZE, MALLOC_CAP_DMA | MALLOC_CAP_INTERNAL);
    if (!s_fb) {
        ESP_LOGE(TAG, "framebuffer alloc failed (%d bytes)", (int)FB_SIZE);
        return ESP_ERR_NO_MEM;
    }
    memset(s_fb, 0, FB_SIZE);

    // DC & RST pins
    gpio_config_t io = {
        .pin_bit_mask = (1ULL << SSD1351_DC_GPIO) | (1ULL << SSD1351_RST_GPIO),
        .mode = GPIO_MODE_OUTPUT,
    };
    gpio_config(&io);

    // Hardware reset
    gpio_set_level(SSD1351_RST_GPIO, 1);
    vTaskDelay(pdMS_TO_TICKS(5));
    gpio_set_level(SSD1351_RST_GPIO, 0);
    vTaskDelay(pdMS_TO_TICKS(10));
    gpio_set_level(SSD1351_RST_GPIO, 1);
    vTaskDelay(pdMS_TO_TICKS(20));

    // SPI bus
    spi_bus_config_t bus = {
        .mosi_io_num = SSD1351_MOSI_GPIO,
        .miso_io_num = -1,
        .sclk_io_num = SSD1351_SCK_GPIO,
        .quadwp_io_num = -1,
        .quadhd_io_num = -1,
        .max_transfer_sz = 4096,
    };
    ESP_ERROR_CHECK(spi_bus_initialize(SPI2_HOST, &bus, SPI_DMA_CH_AUTO));

    spi_device_interface_config_t dev = {
        .clock_speed_hz = 16 * 1000 * 1000,  // 16 MHz
        .mode = 0,
        .spics_io_num = SSD1351_CS_GPIO,
        .queue_size = 1,
    };
    ESP_ERROR_CHECK(spi_bus_add_device(SPI2_HOST, &dev, &s_spi));

    // ---- SSD1351 init sequence ----
    cmd(0xFD); data1(0x12);        // Unlock
    cmd(0xFD); data1(0xB1);        // Unlock extended commands
    cmd(0xAE);                      // Display OFF
    cmd(0xB3); data1(0xF1);        // Clock div / oscillator freq
    cmd(0xCA); data1(0x7F);        // Mux ratio = 128
    cmd(0xA0); data1(0x74);        // Remap: 65K color, COM split, RGB
    cmd(0xA1); data1(0x00);        // Start line = 0
    cmd(0xA2); data1(0x00);        // Display offset = 0
    cmd(0xB5); data1(0x00);        // GPIO off
    cmd(0xAB); data1(0x01);        // Function select: internal VDD
    cmd(0xB1); data1(0x32);        // Precharge
    cmd(0xBE); data1(0x05);        // VCOMH
    cmd(0xA6);                      // Normal display (not inverted)
    uint8_t ct[] = {0xC8, 0x80, 0xC8};
    cmd(0xC1); data_bytes(ct, 3);  // Contrast R, G, B
    cmd(0xC7); data1(0x0F);        // Master contrast
    uint8_t vsl[] = {0xA0, 0xB5, 0x55};
    cmd(0xB4); data_bytes(vsl, 3); // VSL
    cmd(0xB6); data1(0x01);        // Precharge2
    cmd(0xAF);                      // Display ON

    ssd1351_flush();

    ESP_LOGI(TAG, "SSD1351 initialized (%dx%d)", SSD1351_WIDTH, SSD1351_HEIGHT);
    return ESP_OK;
}

void ssd1351_clear(uint16_t color)
{
    uint16_t be = __builtin_bswap16(color);
    for (int i = 0; i < SSD1351_WIDTH * SSD1351_HEIGHT; i++) {
        s_fb[i] = be;
    }
}

static inline void px(int x, int y, uint16_t be_color)
{
    if ((unsigned)x < SSD1351_WIDTH && (unsigned)y < SSD1351_HEIGHT) {
        s_fb[y * SSD1351_WIDTH + x] = be_color;
    }
}

void ssd1351_fill_rect(int x, int y, int w, int h, uint16_t color)
{
    uint16_t be = __builtin_bswap16(color);
    for (int j = y; j < y + h; j++)
        for (int i = x; i < x + w; i++)
            px(i, j, be);
}

void ssd1351_fill_ellipse(int cx, int cy, int rx, int ry, uint16_t color)
{
    if (rx <= 0 || ry <= 0) return;
    uint16_t be = __builtin_bswap16(color);
    for (int j = -ry; j <= ry; j++) {
        for (int i = -rx; i <= rx; i++) {
            if ((int64_t)i * i * ry * ry + (int64_t)j * j * rx * rx
                <= (int64_t)rx * rx * ry * ry) {
                px(cx + i, cy + j, be);
            }
        }
    }
}

void ssd1351_fill_rounded_rect(int x, int y, int w, int h, int r, uint16_t color)
{
    if (r <= 0) {
        ssd1351_fill_rect(x, y, w, h, color);
        return;
    }
    if (r > w / 2) r = w / 2;
    if (r > h / 2) r = h / 2;

    uint16_t be = __builtin_bswap16(color);
    for (int j = 0; j < h; j++) {
        for (int i = 0; i < w; i++) {
            int dx = 0, dy = 0;
            if      (i < r    && j < r)    { dx = r-1-i; dy = r-1-j; }
            else if (i >= w-r && j < r)    { dx = i-w+r; dy = r-1-j; }
            else if (i < r    && j >= h-r) { dx = r-1-i; dy = j-h+r; }
            else if (i >= w-r && j >= h-r) { dx = i-w+r; dy = j-h+r; }
            if ((dx || dy) && dx * dx + dy * dy > r * r) continue;
            px(x + i, y + j, be);
        }
    }
}

void ssd1351_flush(void)
{
    // Set write window: full screen
    uint8_t col[] = {0x00, 0x7F};
    uint8_t row[] = {0x00, 0x7F};
    cmd(0x15); data_bytes(col, 2);
    cmd(0x75); data_bytes(row, 2);
    cmd(0x5C);  // Write RAM

    // Push framebuffer via DMA in 4KB chunks
    gpio_set_level(SSD1351_DC_GPIO, 1);
    const uint8_t *p = (const uint8_t *)s_fb;
    size_t remain = FB_SIZE;
    while (remain > 0) {
        size_t chunk = remain > 4096 ? 4096 : remain;
        spi_transaction_t t = {
            .length = chunk * 8,
            .tx_buffer = p,
        };
        spi_device_polling_transmit(s_spi, &t);
        p += chunk;
        remain -= chunk;
    }
}
