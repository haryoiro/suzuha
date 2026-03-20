#pragma once

#include "esp_err.h"
#include <stdint.h>

#define SSD1351_WIDTH  128
#define SSD1351_HEIGHT 128

// RGB565 colors
#define SSD1351_BLACK   0x0000
#define SSD1351_WHITE   0xFFFF
#define SSD1351_RED     0xF800
#define SSD1351_GREEN   0x07E0
#define SSD1351_BLUE    0x001F

#define SSD1351_RGB565(r, g, b) \
    ((((r) & 0xF8) << 8) | (((g) & 0xFC) << 3) | (((b) & 0xF8) >> 3))

esp_err_t ssd1351_init(void);
void ssd1351_clear(uint16_t color);
void ssd1351_fill_rect(int x, int y, int w, int h, uint16_t color);
void ssd1351_fill_ellipse(int cx, int cy, int rx, int ry, uint16_t color);
void ssd1351_fill_rounded_rect(int x, int y, int w, int h, int r, uint16_t color);
void ssd1351_flush(void);
