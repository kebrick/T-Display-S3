/**
 * Mac/PC metrics over USB CDC, 320x170.
 * Поворот 180 градусов: LVGL sw_rotate + ROT_180 (не зеркало панели).
 * Протокол (основная строка): cpu%,ram%,load1m,disk%,cpuTempC,rx_Mb/s,tx_Mb/s[,wcpu,wram,wdisk,wtemp,net_max_Mb/s]\n
 *   Опционально 12 полей: пороги предупреждений и шкала сети с хоста (defaults как ниже).
 *   Отдельные строки:
 *   "H имя\\n" — подпись в статус-баре (до ~32 символов).
 *   "P n\\n" — экран n = 1..5 (как кнопки), сохраняется в NVS.
 *   "R" или "RESET_PEAKS" — сброс пиков pk/rk на экране Peaks.
 *   temp=-1 нет датчика; rx/tx — сумма не-loopback, мегабит/с.
 * Хост: host/statsfeed или host/mac_cpu_to_serial.py
 * Экраны: 1 CPU+RAM, 2 LOAD+DISK, 3 CPU+TEMP, 4 пики pk/rk, 5 сеть DN/UP.
 * Кнопка 0: след. экран, кнопка 14: пред.
 * Плавные дуги (lv_anim); пики и сеть на отдельных экранах.
 * Пороги: CPU/RAM на экране 1; диск экран 2; TEMP экран 3.
 */

#include "Arduino.h"
#include <Preferences.h>
#include "lv_conf.h"
#include "lvgl.h"
#include "OneButton.h"
#include "esp_lcd_panel_io.h"
#include "esp_lcd_panel_ops.h"
#include "esp_lcd_panel_vendor.h"
#include "pin_config.h"

#define LCD_MODULE_CMD_1

/* Как у factory/lv_demos — совпадает с буфером и драйвером панели */
#define UI_W         EXAMPLE_LCD_H_RES
#define UI_H         EXAMPLE_LCD_V_RES
#define NUM_PAGES    5
#define STATUS_H     22
#define LOAD_TO_ARC      35.0f
#define NET_METER_MAX_MBPS 100.0f
#define CPU_WARN_PCT  85.0f
#define RAM_WARN_PCT  88.0f
#define DISK_WARN_PCT 92.0f
#define TEMP_WARN_C   85.0f

/* Переопределяются хостом при 12 полях в строке метрик */
static float g_warn_cpu = CPU_WARN_PCT;
static float g_warn_ram = RAM_WARN_PCT;
static float g_warn_disk = DISK_WARN_PCT;
static float g_warn_temp = TEMP_WARN_C;
static float g_net_max_mbps = NET_METER_MAX_MBPS;

static String g_host_suffix = "";

static Preferences g_prefs;

typedef struct {
    uint8_t cmd;
    uint8_t data[14];
    uint8_t len;
} lcd_cmd_t;

static const lcd_cmd_t lcd_st7789v[] = {
    {0x11, {0}, 0 | 0x80},
    {0x3A, {0X05}, 1},
    {0xB2, {0X0B, 0X0B, 0X00, 0X33, 0X33}, 5},
    {0xB7, {0X75}, 1},
    {0xBB, {0X28}, 1},
    {0xC0, {0X2C}, 1},
    {0xC2, {0X01}, 1},
    {0xC3, {0X1F}, 1},
    {0xC6, {0X13}, 1},
    {0xD0, {0XA7}, 1},
    {0xD0, {0XA4, 0XA1}, 2},
    {0xD6, {0XA1}, 1},
    {0xE0, {0XF0, 0X05, 0X0A, 0X06, 0X06, 0X03, 0X2B, 0X32, 0X43, 0X36, 0X11, 0X10, 0X2B, 0X32}, 14},
    {0xE1, {0XF0, 0X08, 0X0C, 0X0B, 0X09, 0X24, 0X2B, 0X22, 0X43, 0X38, 0X15, 0X16, 0X2F, 0X37}, 14},
};

typedef struct {
    lv_obj_t *root;
    lv_meter_indicator_t *ind_a;
    lv_meter_indicator_t *ind_b;
    lv_obj_t *meter_a;
    lv_obj_t *meter_b;
    lv_obj_t *lbl_a;
    lv_obj_t *lbl_b;
} PageGauges;

esp_lcd_panel_io_handle_t io_handle = NULL;
static lv_disp_draw_buf_t disp_buf;
static lv_disp_drv_t disp_drv;
static lv_color_t *lv_disp_buf;
static bool is_initialized_lvgl = false;

static PageGauges pages[NUM_PAGES];
static int active_page = 0;
static lv_obj_t *lbl_status = NULL;

typedef struct {
    lv_obj_t *m;
    lv_meter_indicator_t *i;
    int32_t cur;
} AnimMeter;

static AnimMeter g_ma0a, g_ma0b, g_ma1a, g_ma1b, g_ma2a, g_ma2b, g_ma3a, g_ma3b, g_ma4a, g_ma4b;
static float g_cpu_peak = 0.f;
static float g_ram_peak = 0.f;

static String serial_line;
static uint32_t last_rx_ms = 0;

static OneButton btn_boot(PIN_BUTTON_1, true);
static OneButton btn_io14(PIN_BUTTON_2, true);

static bool example_notify_lvgl_flush_ready(esp_lcd_panel_io_handle_t panel_io,
                                            esp_lcd_panel_io_event_data_t *edata, void *user_ctx)
{
    if (is_initialized_lvgl) {
        lv_disp_drv_t *disp_driver = (lv_disp_drv_t *)user_ctx;
        lv_disp_flush_ready(disp_driver);
    }
    return false;
}

static void example_lvgl_flush_cb(lv_disp_drv_t *drv, const lv_area_t *area, lv_color_t *color_map)
{
    esp_lcd_panel_handle_t panel_handle = (esp_lcd_panel_handle_t)drv->user_data;
    esp_lcd_panel_draw_bitmap(panel_handle, area->x1, area->y1, area->x2 + 1, area->y2 + 1, color_map);
}

static void style_screen(lv_obj_t *scr)
{
    lv_obj_set_style_bg_color(scr, lv_color_hex(0x07080d), LV_PART_MAIN);
    lv_obj_set_style_bg_grad_color(scr, lv_color_hex(0x12182a), LV_PART_MAIN);
    lv_obj_set_style_bg_grad_dir(scr, LV_GRAD_DIR_VER, LV_PART_MAIN);
    lv_obj_set_style_bg_opa(scr, LV_OPA_COVER, LV_PART_MAIN);
}

static void anim_meter_exec(void *var, int32_t v)
{
    AnimMeter *am = (AnimMeter *)var;
    lv_meter_set_indicator_end_value(am->m, am->i, v);
    am->cur = v;
}

static void anim_meter_go(AnimMeter *am, int32_t target)
{
    if (!am->m || !am->i)
        return;
    if (am->cur == target)
        return;
    lv_anim_del(am, anim_meter_exec);
    lv_anim_t a;
    lv_anim_init(&a);
    lv_anim_set_var(&a, am);
    lv_anim_set_exec_cb(&a, anim_meter_exec);
    lv_anim_set_values(&a, am->cur, target);
    lv_anim_set_time(&a, 220);
    lv_anim_set_path_cb(&a, lv_anim_path_ease_out);
    lv_anim_start(&a);
}

static void style_card(lv_obj_t *card)
{
    lv_obj_set_style_bg_color(card, lv_color_hex(0x161822), LV_PART_MAIN);
    lv_obj_set_style_bg_opa(card, LV_OPA_COVER, LV_PART_MAIN);
    lv_obj_set_style_radius(card, 12, LV_PART_MAIN);
    lv_obj_set_style_border_width(card, 1, LV_PART_MAIN);
    lv_obj_set_style_border_color(card, lv_color_hex(0x2d3348), LV_PART_MAIN);
    lv_obj_set_style_border_opa(card, LV_OPA_60, LV_PART_MAIN);
    lv_obj_set_style_pad_all(card, 4, LV_PART_MAIN);
}

static void make_gauge_card(lv_obj_t *parent, const char *tag, lv_color_t accent, lv_meter_indicator_t **fg_out,
                            lv_obj_t **meter_out, lv_obj_t **lbl_out)
{
    lv_obj_t *card = lv_obj_create(parent);
    lv_obj_set_flex_grow(card, 1);
    lv_obj_set_height(card, LV_PCT(100));
    lv_obj_set_layout(card, LV_LAYOUT_FLEX);
    lv_obj_set_flex_flow(card, LV_FLEX_FLOW_COLUMN);
    lv_obj_set_flex_align(card, LV_FLEX_ALIGN_SPACE_BETWEEN, LV_FLEX_ALIGN_CENTER, LV_FLEX_ALIGN_CENTER);
    lv_obj_set_style_pad_row(card, 2, LV_PART_MAIN);
    style_card(card);
    lv_obj_clear_flag(card, LV_OBJ_FLAG_SCROLLABLE);

    lv_obj_t *tag_lbl = lv_label_create(card);
    lv_label_set_text(tag_lbl, tag);
    lv_obj_set_style_text_font(tag_lbl, &lv_font_montserrat_12, LV_PART_MAIN);
    lv_obj_set_style_text_color(tag_lbl, lv_color_hex(0x8b92a8), LV_PART_MAIN);

    lv_obj_t *meter = lv_meter_create(card);
    lv_obj_set_size(meter, 92, 72);
    lv_obj_set_flex_grow(meter, 1);
    lv_obj_remove_style(meter, NULL, LV_PART_INDICATOR);

    lv_meter_scale_t *sc = lv_meter_add_scale(meter);
    lv_meter_set_scale_ticks(meter, sc, 2, 0, 0, lv_color_hex(0x303040));
    lv_meter_set_scale_range(meter, sc, 0, 100, 220, 170);

    lv_meter_indicator_t *track = lv_meter_add_arc(meter, sc, 8, lv_color_hex(0x252836), -5);
    lv_meter_set_indicator_start_value(meter, track, 0);
    lv_meter_set_indicator_end_value(meter, track, 100);

    lv_meter_indicator_t *fg = lv_meter_add_arc(meter, sc, 8, accent, -5);
    lv_meter_set_indicator_start_value(meter, fg, 0);
    lv_meter_set_indicator_end_value(meter, fg, 0);
    *fg_out = fg;
    *meter_out = meter;

    lv_obj_t *pct = lv_label_create(card);
    lv_label_set_text(pct, "--");
    lv_obj_set_style_text_font(pct, &lv_font_montserrat_16, LV_PART_MAIN);
    lv_obj_set_style_text_color(pct, lv_color_hex(0xf0f2f8), LV_PART_MAIN);
    *lbl_out = pct;
}

static void build_page_landscape(lv_obj_t *parent, PageGauges *pg, const char *title_txt, const char *t_a,
                                 lv_color_t c_a, const char *t_b, lv_color_t c_b)
{
    lv_obj_set_layout(parent, LV_LAYOUT_FLEX);
    lv_obj_set_flex_flow(parent, LV_FLEX_FLOW_COLUMN);
    lv_obj_set_flex_align(parent, LV_FLEX_ALIGN_START, LV_FLEX_ALIGN_START, LV_FLEX_ALIGN_START);
    lv_obj_set_style_pad_left(parent, 4, LV_PART_MAIN);
    lv_obj_set_style_pad_right(parent, 4, LV_PART_MAIN);
    lv_obj_set_style_pad_top(parent, 2, LV_PART_MAIN);
    lv_obj_set_style_pad_bottom(parent, 2, LV_PART_MAIN);
    lv_obj_set_style_pad_row(parent, 4, LV_PART_MAIN);
    lv_obj_set_style_bg_opa(parent, LV_OPA_TRANSP, LV_PART_MAIN);
    lv_obj_set_style_border_width(parent, 0, LV_PART_MAIN);
    lv_obj_clear_flag(parent, LV_OBJ_FLAG_SCROLLABLE);

    lv_obj_t *head = lv_obj_create(parent);
    lv_obj_set_size(head, LV_PCT(100), 24);
    lv_obj_set_style_bg_opa(head, LV_OPA_TRANSP, LV_PART_MAIN);
    lv_obj_set_style_border_width(head, 0, LV_PART_MAIN);
    lv_obj_clear_flag(head, LV_OBJ_FLAG_SCROLLABLE);

    lv_obj_t *title = lv_label_create(head);
    lv_label_set_text(title, title_txt);
    lv_obj_set_style_text_font(title, &lv_font_montserrat_16, LV_PART_MAIN);
    lv_obj_set_style_text_color(title, lv_color_hex(0xe8ecf4), LV_PART_MAIN);
    lv_obj_align(title, LV_ALIGN_LEFT_MID, 0, 0);

    lv_obj_t *dot = lv_obj_create(head);
    lv_obj_set_size(dot, 6, 6);
    lv_obj_set_style_radius(dot, LV_RADIUS_CIRCLE, LV_PART_MAIN);
    lv_obj_set_style_bg_color(dot, lv_color_hex(0x22d3ee), LV_PART_MAIN);
    lv_obj_set_style_bg_opa(dot, LV_OPA_COVER, LV_PART_MAIN);
    lv_obj_set_style_border_width(dot, 0, LV_PART_MAIN);
    lv_obj_align(dot, LV_ALIGN_RIGHT_MID, 0, 0);

    lv_obj_t *row = lv_obj_create(parent);
    lv_obj_set_width(row, LV_PCT(100));
    lv_obj_set_flex_grow(row, 1);
    lv_obj_set_layout(row, LV_LAYOUT_FLEX);
    lv_obj_set_flex_flow(row, LV_FLEX_FLOW_ROW);
    lv_obj_set_flex_align(row, LV_FLEX_ALIGN_START, LV_FLEX_ALIGN_START, LV_FLEX_ALIGN_START);
    lv_obj_set_style_pad_column(row, 6, LV_PART_MAIN);
    lv_obj_set_style_bg_opa(row, LV_OPA_TRANSP, LV_PART_MAIN);
    lv_obj_set_style_border_width(row, 0, LV_PART_MAIN);
    lv_obj_clear_flag(row, LV_OBJ_FLAG_SCROLLABLE);

    make_gauge_card(row, t_a, c_a, &pg->ind_a, &pg->meter_a, &pg->lbl_a);
    make_gauge_card(row, t_b, c_b, &pg->ind_b, &pg->meter_b, &pg->lbl_b);
}

static void show_page(int idx)
{
    idx = (idx % NUM_PAGES + NUM_PAGES) % NUM_PAGES;
    for (int i = 0; i < NUM_PAGES; i++) {
        if (i == idx)
            lv_obj_clear_flag(pages[i].root, LV_OBJ_FLAG_HIDDEN);
        else
            lv_obj_add_flag(pages[i].root, LV_OBJ_FLAG_HIDDEN);
    }
    active_page = idx;
}

static void build_ui(void)
{
    lv_obj_t *scr = lv_scr_act();
    lv_obj_clean(scr);
    lv_obj_set_size(scr, UI_W, UI_H);
    lv_obj_clear_flag(scr, LV_OBJ_FLAG_SCROLLABLE);
    lv_obj_set_scrollbar_mode(scr, LV_SCROLLBAR_MODE_OFF);
    style_screen(scr);

    lv_coord_t content_h = UI_H - STATUS_H - 2;

    for (int i = 0; i < NUM_PAGES; i++) {
        pages[i].root = lv_obj_create(scr);
        lv_obj_set_size(pages[i].root, UI_W, content_h);
        lv_obj_set_pos(pages[i].root, 0, 0);
        lv_obj_clear_flag(pages[i].root, LV_OBJ_FLAG_SCROLLABLE);
    }

    build_page_landscape(pages[0].root, &pages[0], "System", "CPU", lv_color_hex(0x22d3ee), "RAM",
                         lv_color_hex(0xa78bfa));
    build_page_landscape(pages[1].root, &pages[1], "System 2", "LOAD", lv_color_hex(0xfbbf24), "DISK",
                         lv_color_hex(0x34d399));
    build_page_landscape(pages[2].root, &pages[2], "System 3", "CPU", lv_color_hex(0x22d3ee), "TEMP",
                         lv_color_hex(0xf97316));
    build_page_landscape(pages[3].root, &pages[3], "Peaks", "pk", lv_color_hex(0xfb923c), "rk",
                         lv_color_hex(0xc4b5fd));
    build_page_landscape(pages[4].root, &pages[4], "Network", "DN", lv_color_hex(0x22c55e), "UP",
                         lv_color_hex(0x38bdf8));

    g_ma0a = {pages[0].meter_a, pages[0].ind_a, 0};
    g_ma0b = {pages[0].meter_b, pages[0].ind_b, 0};
    g_ma1a = {pages[1].meter_a, pages[1].ind_a, 0};
    g_ma1b = {pages[1].meter_b, pages[1].ind_b, 0};
    g_ma2a = {pages[2].meter_a, pages[2].ind_a, 0};
    g_ma2b = {pages[2].meter_b, pages[2].ind_b, 0};
    g_ma3a = {pages[3].meter_a, pages[3].ind_a, 0};
    g_ma3b = {pages[3].meter_b, pages[3].ind_b, 0};
    g_ma4a = {pages[4].meter_a, pages[4].ind_a, 0};
    g_ma4b = {pages[4].meter_b, pages[4].ind_b, 0};

    lbl_status = lv_label_create(scr);
    /* Текст статуса — только печатный ASCII (см. status_ascii_only + шрифт без лишних глифов). */
    lv_label_set_text(lbl_status, "USB wait");
    lv_obj_set_style_text_font(lbl_status, &lv_font_montserrat_12, LV_PART_MAIN);
    lv_obj_set_style_text_color(lbl_status, lv_color_hex(0x6b7280), LV_PART_MAIN);
    lv_obj_set_width(lbl_status, UI_W - 8);
    lv_label_set_long_mode(lbl_status, LV_LABEL_LONG_CLIP);
    lv_obj_set_style_text_align(lbl_status, LV_TEXT_ALIGN_CENTER, LV_PART_MAIN);
    lv_obj_align(lbl_status, LV_ALIGN_BOTTOM_MID, 0, -1);

    lv_obj_add_flag(pages[1].root, LV_OBJ_FLAG_HIDDEN);
    lv_obj_add_flag(pages[2].root, LV_OBJ_FLAG_HIDDEN);
    lv_obj_add_flag(pages[3].root, LV_OBJ_FLAG_HIDDEN);
    lv_obj_add_flag(pages[4].root, LV_OBJ_FLAG_HIDDEN);

    lv_obj_invalidate(scr);
    lv_refr_now(NULL);
}

static void apply_page0(float cpu, float ram, bool have_ram)
{
    if (cpu < 0.f)
        cpu = 0.f;
    if (cpu > 100.f)
        cpu = 100.f;
    anim_meter_go(&g_ma0a, (int32_t)(cpu + 0.5f));
    char buf[28];
    snprintf(buf, sizeof(buf), "%.0f%%", cpu);
    lv_label_set_text(pages[0].lbl_a, buf);
    if (cpu >= g_warn_cpu)
        lv_obj_set_style_text_color(pages[0].lbl_a, lv_color_hex(0xf87171), LV_PART_MAIN);
    else
        lv_obj_set_style_text_color(pages[0].lbl_a, lv_color_hex(0xf0f2f8), LV_PART_MAIN);

    if (have_ram) {
        if (ram < 0.f)
            ram = 0.f;
        if (ram > 100.f)
            ram = 100.f;
        anim_meter_go(&g_ma0b, (int32_t)(ram + 0.5f));
        snprintf(buf, sizeof(buf), "%.0f%%", ram);
        lv_label_set_text(pages[0].lbl_b, buf);
        if (ram >= g_warn_ram)
            lv_obj_set_style_text_color(pages[0].lbl_b, lv_color_hex(0xfbbf24), LV_PART_MAIN);
        else
            lv_obj_set_style_text_color(pages[0].lbl_b, lv_color_hex(0xf0f2f8), LV_PART_MAIN);
    }
}

static void apply_page3_peaks(void)
{
    char buf[24];
    int32_t pc = (int32_t)(g_cpu_peak + 0.5f);
    if (pc < 0)
        pc = 0;
    if (pc > 100)
        pc = 100;
    anim_meter_go(&g_ma3a, pc);
    snprintf(buf, sizeof(buf), "%.0f%%", g_cpu_peak);
    lv_label_set_text(pages[3].lbl_a, buf);

    int32_t pr = (int32_t)(g_ram_peak + 0.5f);
    if (pr < 0)
        pr = 0;
    if (pr > 100)
        pr = 100;
    anim_meter_go(&g_ma3b, pr);
    snprintf(buf, sizeof(buf), "%.0f%%", g_ram_peak);
    lv_label_set_text(pages[3].lbl_b, buf);
}

/* mbps — мегабит/с (как на хосте); подпись Mb/s vs kb/s для читаемости */
static void fmt_net_speed_mbps(char *buf, size_t sz, float mbps)
{
    if (mbps < 0.f)
        mbps = 0.f;
    if (mbps >= 100.f) {
        snprintf(buf, sz, "%.0f Mb/s", mbps);
    } else if (mbps >= 1.f) {
        snprintf(buf, sz, "%.1f Mb/s", mbps);
    } else if (mbps >= 0.001f) {
        float kbps = mbps * 1000.f;
        snprintf(buf, sz, "%.0f kb/s", kbps);
    } else {
        snprintf(buf, sz, "0 kb/s");
    }
}

static void apply_page4_net(float rx_mbps, float tx_mbps, bool have_net)
{
    char buf[28];
    if (!have_net) {
        lv_label_set_text(pages[4].lbl_a, "n/a");
        lv_label_set_text(pages[4].lbl_b, "n/a");
        lv_obj_set_style_text_color(pages[4].lbl_a, lv_color_hex(0x9ca3af), LV_PART_MAIN);
        lv_obj_set_style_text_color(pages[4].lbl_b, lv_color_hex(0x9ca3af), LV_PART_MAIN);
        anim_meter_go(&g_ma4a, 0);
        anim_meter_go(&g_ma4b, 0);
        return;
    }
    float max_ref = g_net_max_mbps;
    if (max_ref < 1.f)
        max_ref = 1.f;
    float arx = rx_mbps * (100.f / max_ref);
    if (arx < 0.f)
        arx = 0.f;
    if (arx > 100.f)
        arx = 100.f;
    float atx = tx_mbps * (100.f / max_ref);
    if (atx < 0.f)
        atx = 0.f;
    if (atx > 100.f)
        atx = 100.f;
    anim_meter_go(&g_ma4a, (int32_t)(arx + 0.5f));
    anim_meter_go(&g_ma4b, (int32_t)(atx + 0.5f));
    fmt_net_speed_mbps(buf, sizeof(buf), rx_mbps);
    lv_label_set_text(pages[4].lbl_a, buf);
    lv_obj_set_style_text_color(pages[4].lbl_a, lv_color_hex(0xf0f2f8), LV_PART_MAIN);
    fmt_net_speed_mbps(buf, sizeof(buf), tx_mbps);
    lv_label_set_text(pages[4].lbl_b, buf);
    lv_obj_set_style_text_color(pages[4].lbl_b, lv_color_hex(0xf0f2f8), LV_PART_MAIN);
}

static void apply_page2(float cpu, float temp_c, bool have_temp)
{
    if (cpu < 0.f)
        cpu = 0.f;
    if (cpu > 100.f)
        cpu = 100.f;
    anim_meter_go(&g_ma2a, (int32_t)(cpu + 0.5f));
    char buf[24];
    snprintf(buf, sizeof(buf), "%.0f%%", cpu);
    lv_label_set_text(pages[2].lbl_a, buf);
    if (cpu >= g_warn_cpu)
        lv_obj_set_style_text_color(pages[2].lbl_a, lv_color_hex(0xf87171), LV_PART_MAIN);
    else
        lv_obj_set_style_text_color(pages[2].lbl_a, lv_color_hex(0xf0f2f8), LV_PART_MAIN);

    int32_t t_arc = 0;
    if (have_temp && temp_c >= 0.f && temp_c < 150.f) {
        float t = temp_c;
        if (t > 100.f)
            t = 100.f;
        t_arc = (int32_t)(t + 0.5f);
        snprintf(buf, sizeof(buf), "%.0fC", temp_c);
        if (temp_c >= g_warn_temp)
            lv_obj_set_style_text_color(pages[2].lbl_b, lv_color_hex(0xf87171), LV_PART_MAIN);
        else
            lv_obj_set_style_text_color(pages[2].lbl_b, lv_color_hex(0xf0f2f8), LV_PART_MAIN);
    } else {
        snprintf(buf, sizeof(buf), "n/a");
        lv_obj_set_style_text_color(pages[2].lbl_b, lv_color_hex(0x9ca3af), LV_PART_MAIN);
    }
    lv_label_set_text(pages[2].lbl_b, buf);
    anim_meter_go(&g_ma2b, t_arc);
}

static void apply_page1(float load1, float disk_pct, bool have_disk)
{
    float arc = load1 * LOAD_TO_ARC;
    if (arc < 0.f)
        arc = 0.f;
    if (arc > 100.f)
        arc = 100.f;
    anim_meter_go(&g_ma1a, (int32_t)(arc + 0.5f));
    char buf[24];
    snprintf(buf, sizeof(buf), "%.2f", load1);
    lv_label_set_text(pages[1].lbl_a, buf);

    if (have_disk) {
        if (disk_pct < 0.f)
            disk_pct = 0.f;
        if (disk_pct > 100.f)
            disk_pct = 100.f;
        anim_meter_go(&g_ma1b, (int32_t)(disk_pct + 0.5f));
        snprintf(buf, sizeof(buf), "%.0f%%", disk_pct);
        lv_label_set_text(pages[1].lbl_b, buf);
        if (disk_pct >= g_warn_disk)
            lv_obj_set_style_text_color(pages[1].lbl_b, lv_color_hex(0xf87171), LV_PART_MAIN);
        else
            lv_obj_set_style_text_color(pages[1].lbl_b, lv_color_hex(0xf0f2f8), LV_PART_MAIN);
    }
}

/* Подпись в статусе: только печатный ASCII — в lv_font_montserrat_12 нет «·», дефиса и др., иначе «тофу». */
static void status_ascii_only(char *dst, size_t dstsz, const char *src)
{
    size_t j = 0;
    if (!dst || dstsz == 0)
        return;
    if (!src) {
        dst[0] = '\0';
        return;
    }
    for (size_t i = 0; src[i] != '\0' && j + 1 < dstsz; i++) {
        unsigned char c = (unsigned char)src[i];
        if (c >= 0x20 && c <= 0x7e)
            dst[j++] = (char)c;
    }
    dst[j] = '\0';
}

static void set_status_usb_live(void)
{
    if (g_host_suffix.length() > 0) {
        char hostdisp[40];
        status_ascii_only(hostdisp, sizeof(hostdisp), g_host_suffix.c_str());
        char st[64];
        if (hostdisp[0] != '\0')
            snprintf(st, sizeof(st), "USB live %s", hostdisp);
        else
            snprintf(st, sizeof(st), "USB live");
        lv_label_set_text(lbl_status, st);
    } else {
        lv_label_set_text(lbl_status, "USB live");
    }
    lv_obj_set_style_text_color(lbl_status, lv_color_hex(0x34d399), LV_PART_MAIN);
}

static void parse_line(const String &line)
{
    String s = line;
    s.trim();
    if (s.length() == 0)
        return;

    if (s.equalsIgnoreCase("R") || s.equalsIgnoreCase("RESET_PEAKS")) {
        g_cpu_peak = 0.f;
        g_ram_peak = 0.f;
        apply_page3_peaks();
        last_rx_ms = millis();
        set_status_usb_live();
        return;
    }

    if (s.length() >= 3 && s[0] == 'P' && s[1] == ' ') {
        int pv = s.substring(2).toInt();
        if (pv >= 1 && pv <= NUM_PAGES) {
            show_page(pv - 1);
            g_prefs.putUChar("page", (uint8_t)(pv - 1));
            last_rx_ms = millis();
            set_status_usb_live();
        }
        return;
    }

    if (s.length() >= 2 && (s[0] == 'H' || s[0] == 'h') && s[1] == ' ') {
        String rest = s.substring(2);
        rest.trim();
        if (rest.length() > 32)
            rest = rest.substring(0, 32);
        g_host_suffix = rest;
        last_rx_ms = millis();
        set_status_usb_live();
        return;
    }

    char tmp[200];
    if (s.length() >= (int)sizeof(tmp))
        return;
    s.toCharArray(tmp, sizeof(tmp));

    float cpu = 0, ram = 0, loadv = 0, diskv = 0, tempc = -1.f, netrx = 0.f, nettx = 0.f;
    float wc = g_warn_cpu, wr = g_warn_ram, wd = g_warn_disk, wt = g_warn_temp, nmax = g_net_max_mbps;
    int n = sscanf(tmp, "%f,%f,%f,%f,%f,%f,%f,%f,%f,%f,%f,%f",
                   &cpu, &ram, &loadv, &diskv, &tempc, &netrx, &nettx, &wc, &wr, &wd, &wt, &nmax);

    last_rx_ms = millis();

    if (n >= 12) {
        if (wc >= 1.f && wc <= 100.f)
            g_warn_cpu = wc;
        if (wr >= 1.f && wr <= 100.f)
            g_warn_ram = wr;
        if (wd >= 1.f && wd <= 100.f)
            g_warn_disk = wd;
        if (wt >= 1.f && wt <= 125.f)
            g_warn_temp = wt;
        if (nmax >= 1.f && nmax <= 10000.f)
            g_net_max_mbps = nmax;
    }

    if (n >= 2) {
        apply_page0(cpu, ram, true);
        if (cpu > g_cpu_peak)
            g_cpu_peak = cpu;
        if (ram > g_ram_peak)
            g_ram_peak = ram;
        apply_page3_peaks();
        apply_page2(cpu, tempc, n >= 5);
    }
    if (n >= 4)
        apply_page1(loadv, diskv, true);
    else if (n >= 3)
        apply_page1(loadv, 0.f, false);

    if (n >= 7)
        apply_page4_net(netrx, nettx, true);
    else
        apply_page4_net(0.f, 0.f, false);

    set_status_usb_live();
}

static void poll_serial(void)
{
    while (Serial.available()) {
        char c = (char)Serial.read();
        if (c == '\r')
            continue;
        if (c == '\n') {
            parse_line(serial_line);
            serial_line = "";
            continue;
        }
        if (serial_line.length() < 192)
            serial_line += c;
    }

    if (last_rx_ms != 0 && millis() - last_rx_ms > 3000) {
        lv_label_set_text(lbl_status, "USB no data");
        lv_obj_set_style_text_color(lbl_status, lv_color_hex(0xf87171), LV_PART_MAIN);
        last_rx_ms = 0;
        g_cpu_peak = 0.f;
        g_ram_peak = 0.f;
        apply_page3_peaks();
        apply_page4_net(0.f, 0.f, false);
    }
}

void setup()
{
    pinMode(PIN_POWER_ON, OUTPUT);
    digitalWrite(PIN_POWER_ON, HIGH);

    Serial.begin(115200);

    pinMode(PIN_LCD_RD, OUTPUT);
    digitalWrite(PIN_LCD_RD, HIGH);

    esp_lcd_i80_bus_handle_t i80_bus = NULL;
    esp_lcd_i80_bus_config_t bus_config = {
        .dc_gpio_num = PIN_LCD_DC,
        .wr_gpio_num = PIN_LCD_WR,
        .clk_src = LCD_CLK_SRC_PLL160M,
        .data_gpio_nums =
            {
                PIN_LCD_D0, PIN_LCD_D1, PIN_LCD_D2, PIN_LCD_D3,
                PIN_LCD_D4, PIN_LCD_D5, PIN_LCD_D6, PIN_LCD_D7,
            },
        .bus_width = 8,
        .max_transfer_bytes = LVGL_LCD_BUF_SIZE * sizeof(uint16_t),
        .psram_trans_align = 0,
        .sram_trans_align = 0,
    };
    ESP_ERROR_CHECK(esp_lcd_new_i80_bus(&bus_config, &i80_bus));

    esp_lcd_panel_io_i80_config_t io_config = {
        .cs_gpio_num = PIN_LCD_CS,
        .pclk_hz = EXAMPLE_LCD_PIXEL_CLOCK_HZ,
        .trans_queue_depth = 20,
        .on_color_trans_done = example_notify_lvgl_flush_ready,
        .user_ctx = &disp_drv,
        .lcd_cmd_bits = 8,
        .lcd_param_bits = 8,
        .dc_levels =
            {
                .dc_idle_level = 0,
                .dc_cmd_level = 0,
                .dc_dummy_level = 0,
                .dc_data_level = 1,
            },
    };
    ESP_ERROR_CHECK(esp_lcd_new_panel_io_i80(i80_bus, &io_config, &io_handle));

    esp_lcd_panel_handle_t panel_handle = NULL;
    esp_lcd_panel_dev_config_t panel_config = {
        .reset_gpio_num = PIN_LCD_RES,
        .color_space = ESP_LCD_COLOR_SPACE_RGB,
        .bits_per_pixel = 16,
        .vendor_config = NULL,
    };
    ESP_ERROR_CHECK(esp_lcd_new_panel_st7789(io_handle, &panel_config, &panel_handle));
    ESP_ERROR_CHECK(esp_lcd_panel_reset(panel_handle));
    ESP_ERROR_CHECK(esp_lcd_panel_init(panel_handle));
    ESP_ERROR_CHECK(esp_lcd_panel_invert_color(panel_handle, true));
    /* Как factory (USB слева); поворот 180 делаем в LVGL, не двойным mirror */
    ESP_ERROR_CHECK(esp_lcd_panel_swap_xy(panel_handle, true));
    ESP_ERROR_CHECK(esp_lcd_panel_mirror(panel_handle, false, true));
    ESP_ERROR_CHECK(esp_lcd_panel_set_gap(panel_handle, 0, 35));

    for (size_t i = 0; i < sizeof(lcd_st7789v) / sizeof(lcd_st7789v[0]); i++) {
        esp_lcd_panel_io_tx_param(io_handle, lcd_st7789v[i].cmd, lcd_st7789v[i].data, lcd_st7789v[i].len & 0x7f);
        if (lcd_st7789v[i].len & 0x80)
            delay(120);
    }

    pinMode(PIN_LCD_BL, OUTPUT);
    digitalWrite(PIN_LCD_BL, HIGH);

    lv_init();
    lv_disp_buf = (lv_color_t *)heap_caps_malloc(LVGL_LCD_BUF_SIZE * sizeof(lv_color_t), MALLOC_CAP_DMA | MALLOC_CAP_INTERNAL);
    lv_disp_draw_buf_init(&disp_buf, lv_disp_buf, NULL, LVGL_LCD_BUF_SIZE);

    lv_disp_drv_init(&disp_drv);
    disp_drv.hor_res = UI_W;
    disp_drv.ver_res = UI_H;
    /* Поворот всего UI на 180 (другой бок корпуса); те же 320x170, иной код чем ROT_90 */
    disp_drv.sw_rotate = 1;
    disp_drv.rotated = LV_DISP_ROT_180;
    disp_drv.flush_cb = example_lvgl_flush_cb;
    disp_drv.draw_buf = &disp_buf;
    disp_drv.user_data = panel_handle;
    lv_disp_drv_register(&disp_drv);

    is_initialized_lvgl = true;

    g_prefs.begin("statsfeed", false);
    build_ui();
    {
        uint8_t sp = g_prefs.getUChar("page", 0);
        if (sp < NUM_PAGES)
            show_page((int)sp);
    }

    btn_boot.attachClick([]() {
        show_page(active_page + 1);
        g_prefs.putUChar("page", (uint8_t)active_page);
    });
    btn_io14.attachClick([]() {
        show_page(active_page - 1);
        g_prefs.putUChar("page", (uint8_t)active_page);
    });
}

void loop()
{
    btn_boot.tick();
    btn_io14.tick();
    poll_serial();
    lv_timer_handler();
    delay(5);
}
