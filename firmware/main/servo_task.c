#include "servo_task.h"
#include "config.h"
#include "esp_log.h"
#include "driver/mcpwm_prelude.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "freertos/queue.h"

static const char *TAG = "servo";
static mcpwm_cmpr_handle_t s_pan_cmp = NULL;
static mcpwm_cmpr_handle_t s_tilt_cmp = NULL;
static QueueHandle_t s_cmd_queue = NULL;

typedef struct {
    int pan_deg;
    int tilt_deg;
} servo_cmd_t;

static inline uint32_t degree_to_pulsewidth(int deg)
{
    // Map 0-180 degrees to min-max pulse width
    if (deg < 0) deg = 0;
    if (deg > 180) deg = 180;
    return SERVO_MIN_PULSEWIDTH_US +
           (uint32_t)((SERVO_MAX_PULSEWIDTH_US - SERVO_MIN_PULSEWIDTH_US) * deg / 180);
}

static esp_err_t init_servo_channel(int gpio, mcpwm_cmpr_handle_t *out_cmp)
{
    mcpwm_timer_handle_t timer = NULL;
    mcpwm_timer_config_t timer_cfg = {
        .group_id = 0,
        .clk_src = MCPWM_TIMER_CLK_SRC_DEFAULT,
        .resolution_hz = 1000000,  // 1MHz = 1us resolution
        .period_ticks = 20000,     // 20ms period (50Hz)
        .count_mode = MCPWM_TIMER_COUNT_MODE_UP,
    };
    ESP_ERROR_CHECK(mcpwm_new_timer(&timer_cfg, &timer));

    mcpwm_oper_handle_t oper = NULL;
    mcpwm_operator_config_t oper_cfg = { .group_id = 0 };
    ESP_ERROR_CHECK(mcpwm_new_operator(&oper_cfg, &oper));
    ESP_ERROR_CHECK(mcpwm_operator_connect_timer(oper, timer));

    mcpwm_comparator_config_t cmp_cfg = { .flags.update_cmp_on_tez = true };
    ESP_ERROR_CHECK(mcpwm_new_comparator(oper, &cmp_cfg, out_cmp));

    mcpwm_gen_handle_t gen = NULL;
    mcpwm_generator_config_t gen_cfg = { .gen_gpio_num = gpio };
    ESP_ERROR_CHECK(mcpwm_new_generator(oper, &gen_cfg, &gen));

    ESP_ERROR_CHECK(mcpwm_generator_set_action_on_timer_event(gen,
        MCPWM_GEN_TIMER_EVENT_ACTION(MCPWM_TIMER_DIRECTION_UP, MCPWM_TIMER_EVENT_EMPTY, MCPWM_GEN_ACTION_HIGH)));
    ESP_ERROR_CHECK(mcpwm_generator_set_action_on_compare_event(gen,
        MCPWM_GEN_COMPARE_EVENT_ACTION(MCPWM_TIMER_DIRECTION_UP, *out_cmp, MCPWM_GEN_ACTION_LOW)));

    ESP_ERROR_CHECK(mcpwm_timer_enable(timer));
    ESP_ERROR_CHECK(mcpwm_timer_start_stop(timer, MCPWM_TIMER_START_NO_STOP));

    // Center position
    ESP_ERROR_CHECK(mcpwm_comparator_set_compare_value(*out_cmp, degree_to_pulsewidth(95)));

    return ESP_OK;
}

static void servo_task(void *arg)
{
    servo_cmd_t cmd;
    while (true) {
        if (xQueueReceive(s_cmd_queue, &cmd, portMAX_DELAY) == pdTRUE) {
            ESP_LOGI(TAG, "Servo move: pan=%d, tilt=%d", cmd.pan_deg, cmd.tilt_deg);
            if (s_pan_cmp) {
                mcpwm_comparator_set_compare_value(s_pan_cmp, degree_to_pulsewidth(cmd.pan_deg));
            }
            if (s_tilt_cmp) {
                mcpwm_comparator_set_compare_value(s_tilt_cmp, degree_to_pulsewidth(cmd.tilt_deg));
            }
        }
    }
}

esp_err_t servo_task_start(void)
{
    s_cmd_queue = xQueueCreate(1, sizeof(servo_cmd_t));
    if (!s_cmd_queue) {
        return ESP_ERR_NO_MEM;
    }

    ESP_ERROR_CHECK(init_servo_channel(SERVO_PAN_GPIO, &s_pan_cmp));
#if SERVO_TILT_GPIO >= 0
    ESP_ERROR_CHECK(init_servo_channel(SERVO_TILT_GPIO, &s_tilt_cmp));
#else
    ESP_LOGI(TAG, "Tilt servo disabled");
#endif

    BaseType_t created = xTaskCreatePinnedToCore(
        servo_task, "servo", 2048, NULL, 3, NULL, 1);

    return (created == pdPASS) ? ESP_OK : ESP_FAIL;
}

void servo_task_stop(void)
{
    // TODO: clean up MCPWM resources
}

void servo_set_position(int pan_deg, int tilt_deg)
{
    if (!s_cmd_queue) return;
    servo_cmd_t cmd = { .pan_deg = pan_deg, .tilt_deg = tilt_deg };
    xQueueOverwrite(s_cmd_queue, &cmd);
}
