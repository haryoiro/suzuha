#include "servo_task.h"
#include "config.h"
#include "esp_log.h"
#include "driver/mcpwm_prelude.h"
#include "driver/ledc.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "freertos/queue.h"

static const char *TAG = "servo";
static QueueHandle_t s_cmd_queue = NULL;

#define SERVO_HOLD_MS 2000

typedef struct {
    int pan_deg;
    int tilt_deg;
} servo_cmd_t;

static inline uint32_t degree_to_pulsewidth(int deg)
{
    if (deg < 0) deg = 0;
    if (deg > 180) deg = 180;
    return SERVO_MIN_PULSEWIDTH_US +
           (uint32_t)((SERVO_MAX_PULSEWIDTH_US - SERVO_MIN_PULSEWIDTH_US) * deg / 180);
}

// ---- Pan: MCPWM (known working on GPIO 6) ----

static mcpwm_cmpr_handle_t s_pan_cmp = NULL;
static mcpwm_timer_handle_t s_pan_timer = NULL;
static bool s_pan_running = false;

static esp_err_t init_pan(int gpio)
{
    mcpwm_timer_config_t timer_cfg = {
        .group_id = 0,
        .clk_src = MCPWM_TIMER_CLK_SRC_DEFAULT,
        .resolution_hz = 1000000,
        .period_ticks = 20000,
        .count_mode = MCPWM_TIMER_COUNT_MODE_UP,
    };
    ESP_ERROR_CHECK(mcpwm_new_timer(&timer_cfg, &s_pan_timer));

    mcpwm_oper_handle_t oper = NULL;
    mcpwm_operator_config_t oper_cfg = { .group_id = 0 };
    ESP_ERROR_CHECK(mcpwm_new_operator(&oper_cfg, &oper));
    ESP_ERROR_CHECK(mcpwm_operator_connect_timer(oper, s_pan_timer));

    mcpwm_comparator_config_t cmp_cfg = { .flags.update_cmp_on_tez = true };
    ESP_ERROR_CHECK(mcpwm_new_comparator(oper, &cmp_cfg, &s_pan_cmp));

    mcpwm_gen_handle_t gen = NULL;
    mcpwm_generator_config_t gen_cfg = { .gen_gpio_num = gpio };
    ESP_ERROR_CHECK(mcpwm_new_generator(oper, &gen_cfg, &gen));

    ESP_ERROR_CHECK(mcpwm_generator_set_action_on_timer_event(gen,
        MCPWM_GEN_TIMER_EVENT_ACTION(MCPWM_TIMER_DIRECTION_UP, MCPWM_TIMER_EVENT_EMPTY, MCPWM_GEN_ACTION_HIGH)));
    ESP_ERROR_CHECK(mcpwm_generator_set_action_on_compare_event(gen,
        MCPWM_GEN_COMPARE_EVENT_ACTION(MCPWM_TIMER_DIRECTION_UP, s_pan_cmp, MCPWM_GEN_ACTION_LOW)));

    ESP_ERROR_CHECK(mcpwm_timer_enable(s_pan_timer));

    ESP_LOGI(TAG, "Pan servo initialized on GPIO %d (MCPWM)", gpio);
    return ESP_OK;
}

static void pan_start(void)
{
    if (s_pan_running) return;
    mcpwm_timer_start_stop(s_pan_timer, MCPWM_TIMER_START_NO_STOP);
    s_pan_running = true;
}

static void pan_stop(void)
{
    if (!s_pan_running) return;
    // Set duty to 0 instead of stopping timer (stopping timer can block other tasks).
    mcpwm_comparator_set_compare_value(s_pan_cmp, 0);
    s_pan_running = false;
}

static void pan_set(int deg)
{
    pan_start();
    mcpwm_comparator_set_compare_value(s_pan_cmp, degree_to_pulsewidth(deg));
}

// ---- Tilt: LEDC (works on any GPIO) ----

#define TILT_LEDC_TIMER   LEDC_TIMER_0
#define TILT_LEDC_CHANNEL LEDC_CHANNEL_0
#define TILT_LEDC_FREQ_HZ 50
// LEDC resolution: 14-bit gives 16384 ticks per 20ms period = ~1.22us per tick.
// Good enough for servo control (500-2500us range = ~409-2048 ticks).
#define TILT_LEDC_RESOLUTION LEDC_TIMER_14_BIT
#define TILT_LEDC_MAX_DUTY   ((1 << 14) - 1)  // 16383

static bool s_tilt_inited = false;
static bool s_tilt_running = false;

static inline uint32_t degree_to_ledc_duty(int deg)
{
    uint32_t pulse_us = degree_to_pulsewidth(deg);
    // 20000us period, 14-bit resolution (16384 ticks)
    return (uint32_t)((uint64_t)pulse_us * TILT_LEDC_MAX_DUTY / 20000);
}

static esp_err_t init_tilt(int gpio)
{
    ledc_timer_config_t timer_cfg = {
        .speed_mode = LEDC_LOW_SPEED_MODE,
        .timer_num = TILT_LEDC_TIMER,
        .duty_resolution = TILT_LEDC_RESOLUTION,
        .freq_hz = TILT_LEDC_FREQ_HZ,
        .clk_cfg = LEDC_AUTO_CLK,
    };
    ESP_ERROR_CHECK(ledc_timer_config(&timer_cfg));

    ledc_channel_config_t ch_cfg = {
        .speed_mode = LEDC_LOW_SPEED_MODE,
        .channel = TILT_LEDC_CHANNEL,
        .timer_sel = TILT_LEDC_TIMER,
        .intr_type = LEDC_INTR_DISABLE,
        .gpio_num = gpio,
        .duty = 0,
        .hpoint = 0,
    };
    ESP_ERROR_CHECK(ledc_channel_config(&ch_cfg));

    s_tilt_inited = true;
    ESP_LOGI(TAG, "Tilt servo initialized on GPIO %d (LEDC)", gpio);
    return ESP_OK;
}

static void tilt_set(int deg)
{
    if (!s_tilt_inited) return;
    ledc_set_duty(LEDC_LOW_SPEED_MODE, TILT_LEDC_CHANNEL, degree_to_ledc_duty(deg));
    ledc_update_duty(LEDC_LOW_SPEED_MODE, TILT_LEDC_CHANNEL);
    s_tilt_running = true;
}

static void tilt_stop(void)
{
    if (!s_tilt_running) return;
    ledc_set_duty(LEDC_LOW_SPEED_MODE, TILT_LEDC_CHANNEL, 0);
    ledc_update_duty(LEDC_LOW_SPEED_MODE, TILT_LEDC_CHANNEL);
    s_tilt_running = false;
}

// ---- Servo task ----

static void servo_task(void *arg)
{
    servo_cmd_t cmd;
    while (true) {
        if (xQueueReceive(s_cmd_queue, &cmd, pdMS_TO_TICKS(SERVO_HOLD_MS)) == pdTRUE) {
            ESP_LOGI(TAG, "Servo move: pan=%d, tilt=%d", cmd.pan_deg, cmd.tilt_deg);
            pan_set(cmd.pan_deg);
            tilt_set(cmd.tilt_deg);
        } else {
            pan_stop();
            tilt_stop();
        }
    }
}

esp_err_t servo_task_start(void)
{
    s_cmd_queue = xQueueCreate(1, sizeof(servo_cmd_t));
    if (!s_cmd_queue) {
        return ESP_ERR_NO_MEM;
    }

    ESP_ERROR_CHECK(init_pan(SERVO_PAN_GPIO));
#if SERVO_TILT_GPIO >= 0
    ESP_ERROR_CHECK(init_tilt(SERVO_TILT_GPIO));
#else
    ESP_LOGI(TAG, "Tilt servo disabled");
#endif

    BaseType_t created = xTaskCreatePinnedToCore(
        servo_task, "servo", 2048, NULL, 3, NULL, 1);

    return (created == pdPASS) ? ESP_OK : ESP_FAIL;
}

void servo_task_stop(void)
{
    // TODO: clean up resources
}

void servo_set_position(int pan_deg, int tilt_deg)
{
    if (!s_cmd_queue) return;
    servo_cmd_t cmd = { .pan_deg = pan_deg, .tilt_deg = tilt_deg };
    xQueueOverwrite(s_cmd_queue, &cmd);
}
