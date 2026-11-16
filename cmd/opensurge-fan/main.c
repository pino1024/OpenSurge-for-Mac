// opensurge-fan drives the Mac fan from a user curve. Each sensor group
// (performance cores, GPU, voltage regulators) drops its hottest reading and
// the hotter group summary is smoothed before it moves the curve. Speed steps
// toward the target instead of jumping, and every exit hands the fan back to
// Apple. Reading needs no privileges. Writing needs root.
//
// Adapted from https://github.com/HDZucht/Mac-mini-M6-fan-control-LLM (MIT).
// Tested upstream on Mac mini M6 (Mac18,5).
#include <CoreFoundation/CoreFoundation.h>
#include <IOKit/IOKitLib.h>
#include <math.h>
#include <pwd.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>
#include <time.h>

#define MODE_FILE "/Library/Application Support/OpenSurge/fan-mode"
#define CONF_FILE "/Library/Application Support/OpenSurge/fanguard.conf"

typedef struct { char major, minor, build, reserved[1]; UInt16 release; } Vers;
typedef struct { UInt16 version, length; UInt32 cpu, gpu, mem; } PLimit;
typedef struct { UInt32 dataSize, dataType; char dataAttributes; } KeyInfo;
typedef struct {
    UInt32 key;
    Vers vers;
    PLimit pl;
    KeyInfo ki;
    char result, status, data8;
    UInt32 data32;
    unsigned char bytes[32];
} SMCData;

enum { SMC_READ = 5, SMC_WRITE = 6, SMC_KEY_AT_INDEX = 8, SMC_KEY_INFO = 9 };

static io_connect_t con;
static int smc_call(SMCData *in, SMCData *out) {
    size_t o = sizeof(SMCData);
    return IOConnectCallStructMethod(con, 2, in, sizeof(SMCData), out, &o);
}
static UInt32 k2u(const char *s) { return ((UInt32)(unsigned char)s[0] << 24) | ((UInt32)(unsigned char)s[1] << 16) | ((UInt32)(unsigned char)s[2] << 8) | (UInt32)(unsigned char)s[3]; }
static void u2k(UInt32 u, char *s) { s[0] = (char)(u >> 24); s[1] = (char)(u >> 16); s[2] = (char)(u >> 8); s[3] = (char)u; s[4] = 0; }

static int smc_info(const char *key, KeyInfo *ki) {
    SMCData in = {0}, out = {0};
    in.key = k2u(key);
    in.data8 = SMC_KEY_INFO;
    if (smc_call(&in, &out) || out.result) return -1;
    *ki = out.ki;
    return 0;
}
static int smc_read(const char *key, KeyInfo *ki, unsigned char *b) {
    if (smc_info(key, ki)) return -1;
    SMCData in = {0}, out = {0};
    in.key = k2u(key);
    in.ki.dataSize = ki->dataSize;
    in.data8 = SMC_READ;
    if (smc_call(&in, &out) || out.result) return -1;
    memcpy(b, out.bytes, 32);
    return 0;
}
static int smc_write(const char *key, const unsigned char *b, UInt32 size) {
    SMCData in = {0}, out = {0};
    in.key = k2u(key);
    in.ki.dataSize = size;
    in.data8 = SMC_WRITE;
    memcpy(in.bytes, b, size);
    int r = smc_call(&in, &out);
    return r ? r : out.result;
}
static double smc_val(const char *key) {
    KeyInfo ki;
    unsigned char b[32];
    char t[5];
    if (smc_read(key, &ki, b)) return -1;
    u2k(ki.dataType, t);
    if (!strcmp(t, "flt ")) {
        float f;
        memcpy(&f, b, 4);
        return f;
    }
    if (!strcmp(t, "ui8 ")) return b[0];
    if (!strcmp(t, "ui16")) return (b[0] << 8) | b[1];
    return -1;
}
static int write_flt(const char *key, float f) {
    unsigned char b[4];
    memcpy(b, &f, 4);
    return smc_write(key, b, 4);
}
static int write_u8(const char *key, unsigned char v) { return smc_write(key, &v, 1); }

static char sensor_prefixes[256] = "TVD,Tg,Tp";
static double t_start = 60, t_full = 85, interval = 2, hold = 10;
#define MAX_POINTS 6
static int npoints = 3, conf_n = 3;
static double pt_c[MAX_POINTS] = {60, 72, 85}, pt_p[MAX_POINTS] = {0, 26, 100};
static double conf_c[MAX_POINTS] = {60, 72, 85}, conf_p[MAX_POINTS] = {0, 26, 100};
static void apply_points(const double *c, const double *p, int n) {
    if (n < 2) n = 2;
    if (n > MAX_POINTS) n = MAX_POINTS;
    npoints = n;
    for (int i = 0; i < n; i++) { pt_c[i] = c[i]; pt_p[i] = p[i]; }
    t_start = pt_c[0];
    t_full = pt_c[npoints - 1];
}
static void curve_between(double start, double full, double *c, double *p, int *n) {
    *n = 3;
    c[0] = start; p[0] = 0;
    c[1] = start + (full - start) * 12.0 / 25.0; p[1] = 26;
    c[2] = full; p[2] = 100;
}
static void load_conf(const char *path) {
    FILE *f = fopen(path, "r");
    if (!f) return;
    char line[300];
    while (fgets(line, sizeof line, f)) {
        if (line[0] == '#') continue;
        char *v = strchr(line, '=');
        if (!v) continue;
        *v++ = 0;
        v[strcspn(v, "\r\n #")] = 0;
        if (!strcmp(line, "sensors")) snprintf(sensor_prefixes, sizeof sensor_prefixes, "%s", v);
        else if (!strcmp(line, "start")) t_start = atof(v);
        else if (!strcmp(line, "full")) t_full = atof(v);
        else if (!strcmp(line, "interval")) interval = atof(v);
        else if (!strcmp(line, "hold")) hold = atof(v);
    }
    fclose(f);
    curve_between(t_start, t_full, conf_c, conf_p, &conf_n);
    apply_points(conf_c, conf_p, conf_n);
}

#define MAX_SENSORS 400
static char skey[MAX_SENSORS][5];
static int nsens = 0, decides[MAX_SENSORS];
static int matches_prefix(const char *k) {
    char copy[256];
    snprintf(copy, sizeof copy, "%s", sensor_prefixes);
    for (char *p = strtok(copy, ","); p; p = strtok(NULL, ","))
        if (!strncmp(k, p, strlen(p))) return 1;
    return 0;
}
static void find_sensors(void) {
    KeyInfo ki;
    unsigned char b[32];
    smc_read("#KEY", &ki, b);
    UInt32 n = ((UInt32)b[0] << 24) | (b[1] << 16) | (b[2] << 8) | b[3];
    for (UInt32 i = 0; i < n && nsens < MAX_SENSORS; i++) {
        SMCData in = {0}, out = {0};
        in.data8 = SMC_KEY_AT_INDEX;
        in.data32 = i;
        if (smc_call(&in, &out)) continue;
        char k[5];
        u2k(out.key, k);
        if (k[0] != 'T') continue;
        double v = smc_val(k);
        if (v < 5 || v > 130) continue;
        strcpy(skey[nsens], k);
        decides[nsens] = matches_prefix(k);
        nsens++;
    }
    int any = 0;
    for (int i = 0; i < nsens; i++) any |= decides[i];
    if (!any) {
        for (int i = 0; i < nsens; i++) decides[i] = 1;
        snprintf(sensor_prefixes, sizeof sensor_prefixes, "all T*");
    }
}
static double hottest(char *which) {
    double m = -1;
    for (int i = 0; i < nsens; i++)
        if (decides[i]) {
            double v = smc_val(skey[i]);
            if (v > m && v < 130) {
                m = v;
                strcpy(which, skey[i]);
            }
        }
    return m;
}

static int nfans = 1;
static double fmn[8], fmx[8], fmin0, fmax0;
static char *fkey(int i, const char *s) {
    static char b[8][5];
    static int r = 0;
    r = (r + 1) % 8;
    snprintf(b[r], 5, "F%d%s", i, s);
    return b[r];
}
static void read_fans(void) {
    nfans = (int)smc_val("FNum");
    if (nfans < 1) nfans = 1;
    if (nfans > 8) nfans = 8;
    for (int i = 0; i < nfans; i++) {
        fmn[i] = smc_val(fkey(i, "Mn"));
        fmx[i] = smc_val(fkey(i, "Mx"));
    }
    fmin0 = fmn[0];
    fmax0 = fmx[0];
}
static double current_rpm(void) {
    double m = 0;
    for (int i = 0; i < nfans; i++) {
        double v = smc_val(fkey(i, "Ac"));
        if (v > m) m = v;
    }
    return m;
}
static int is_manual(void) {
    for (int i = 0; i < nfans; i++)
        if (smc_val(fkey(i, "md")) > 0) return 1;
    return 0;
}
static int set_manual(float rpm) {
    double x = fmax0 > fmin0 ? (rpm - fmin0) / (fmax0 - fmin0) : 1;
    if (x < 0) x = 0;
    if (x > 1) x = 1;
    int err = 0;
    for (int i = 0; i < nfans; i++) {
        if (write_u8(fkey(i, "md"), 1)) {
            write_u8("Ftst", 1);
            usleep(500000);
            if (write_u8(fkey(i, "md"), 1)) {
                err = -1;
                continue;
            }
        }
        if (write_flt(fkey(i, "Tg"), (float)(fmn[i] + (fmx[i] - fmn[i]) * x))) err = -1;
    }
    return err;
}
static int set_auto(void) {
    int r = 0;
    for (int i = 0; i < nfans; i++) r |= write_u8(fkey(i, "md"), 0);
    write_u8("Ftst", 0);
    return r;
}

static void read_mode(char *mode, size_t n) {
    snprintf(mode, n, "curve");
    FILE *f = fopen(MODE_FILE, "r");
    if (f) {
        if (fgets(mode, (int)n, f)) mode[strcspn(mode, "\r\n ")] = 0;
        fclose(f);
    }
}

static int json_number(const char *json, const char *key, double *out) {
    char pattern[64];
    snprintf(pattern, sizeof pattern, "\"%s\"", key);
    const char *p = strstr(json, pattern);
    if (!p) return 0;
    p = strchr(p + strlen(pattern), ':');
    if (!p) return 0;
    *out = atof(p + 1);
    return 1;
}

static int user_file(const char *name, char *path, size_t n) {
    struct stat st;
    if (stat("/dev/console", &st) != 0) return 0;
    struct passwd *pw = getpwuid(st.st_uid);
    if (!pw || !pw->pw_dir) return 0;
    int wrote = snprintf(path, n, "%s/Library/Application Support/OpenSurge/%s", pw->pw_dir, name);
    return wrote > 0 && (size_t)wrote < n;
}

static int parse_points(const char *json, double *c, double *p) {
    const char *key = strstr(json, "\"points\"");
    if (!key) return 0;
    const char *start = strchr(key, '[');
    const char *end = start ? strchr(start, ']') : NULL;
    if (!start || !end) return 0;
    int n = 0;
    const char *cursor = start;
    while (n < MAX_POINTS && cursor < end) {
        const char *ck = strstr(cursor, "\"c\"");
        if (!ck || ck >= end) break;
        const char *pk = strstr(ck, "\"p\"");
        if (!pk || pk >= end) break;
        double cv, pv;
        if (!json_number(ck, "c", &cv) || !json_number(pk, "p", &pv)) break;
        if (cv < 20 || cv > 110 || pv < 0 || pv > 100) break;
        if (n > 0 && (cv < c[n - 1] + 2 || pv < p[n - 1])) break;
        c[n] = cv;
        p[n] = pv;
        n++;
        cursor = pk + 3;
    }
    return n;
}

// The signed-in user edits this file from the menu bar. It overrides the
// installed curve while it exists. Missing points fall back to the old
// start/full pair so a file written by the previous build still applies.
static int read_user_fan(char *mode, size_t n) {
    char path[1024];
    if (!user_file("fan.json", path, sizeof path)) return 0;
    FILE *f = fopen(path, "r");
    if (!f) return 0;
    char buf[2048];
    size_t got = fread(buf, 1, sizeof buf - 1, f);
    fclose(f);
    if (got == 0) return 0;
    buf[got] = 0;
    int enabled = strstr(buf, "\"enabled\":true") || strstr(buf, "\"enabled\": true");
    snprintf(mode, n, "%s", enabled ? "curve" : "auto");
    double c[MAX_POINTS], p[MAX_POINTS];
    int np = parse_points(buf, c, p);
    if (np >= 2) {
        apply_points(c, p, np);
        return 1;
    }
    double start = 0, full = 0;
    if (json_number(buf, "start", &start) && json_number(buf, "full", &full) && full >= start + 5 && start >= 20 && full <= 110) {
        curve_between(start, full, c, p, &np);
        apply_points(c, p, np);
    }
    return 1;
}

static void read_control(char *mode, size_t n) {
    apply_points(conf_c, conf_p, conf_n);
    if (!read_user_fan(mode, n)) read_mode(mode, n);
}
static void stamp(void) {
    time_t now = time(0);
    printf("%.19s ", ctime(&now));
}
static volatile sig_atomic_t stop = 0;
static void on_signal(int s) {
    (void)s;
    stop = 1;
}
static void nap(void) {
    for (int i = 0; i < interval * 10 && !stop; i++) usleep(100000);
}

static const char *group_name(const char *key) {
    if (!strncmp(key, "TVD", 3)) return "TVD";
    if (!strncmp(key, "Tg", 2)) return "Tg";
    if (!strncmp(key, "Tp", 2)) return "Tp";
    return NULL;
}

// Drop the hottest sensor in a group of three or more, then keep the hotter
// group. A single hot sensor cannot command full speed. When that group
// temperature stays at least 3 degrees above the smoothed value for two
// samples, a real load such as a game is underway and the fan is allowed
// to catch up quickly.
static double control_sample(double *raw_max) {
    char names[8][8];
    double vals[8][64];
    int counts[8], ngroups = 0;
    *raw_max = -1;
    for (int i = 0; i < nsens; i++) {
        if (!decides[i]) continue;
        double v = smc_val(skey[i]);
        if (v < 5 || v >= 130) continue;
        if (v > *raw_max) *raw_max = v;
        const char *name = group_name(skey[i]);
        if (!name) continue;
        int g = -1;
        for (int j = 0; j < ngroups; j++)
            if (!strcmp(names[j], name)) g = j;
        if (g < 0) {
            if (ngroups >= 8) continue;
            g = ngroups++;
            snprintf(names[g], sizeof names[g], "%s", name);
            counts[g] = 0;
        }
        if (counts[g] < 64) vals[g][counts[g]++] = v;
    }
    double best = -1;
    for (int g = 0; g < ngroups; g++) {
        int n = counts[g];
        if (n <= 0) continue;
        double summary;
        if (n >= 3) {
            int hot = 0;
            for (int k = 1; k < n; k++)
                if (vals[g][k] > vals[g][hot]) hot = k;
            double sum = 0;
            for (int k = 0; k < n; k++)
                if (k != hot) sum += vals[g][k];
            summary = sum / (n - 1);
        } else {
            double sum = 0;
            for (int k = 0; k < n; k++) sum += vals[g][k];
            summary = sum / n;
        }
        if (summary > best) best = summary;
    }
    return best;
}

static double ema = -1;
static double smooth(double sample, int fast_rise) {
    if (sample < 0) return ema;
    if (ema < 0) { ema = sample; return ema; }
    double tau = sample >= ema ? (fast_rise ? 4.0 : 20.0) : 12.0;
    double dt = interval > 0 ? interval : 2;
    double alpha = 1 - exp(-dt / tau);
    ema += alpha * (sample - ema);
    return ema;
}

static double curve_percent(double t) {
    if (npoints < 1) return 0;
    if (t <= pt_c[0]) return pt_p[0];
    if (t >= pt_c[npoints - 1]) return pt_p[npoints - 1];
    for (int i = 1; i < npoints; i++) {
        if (t <= pt_c[i]) {
            double span = pt_c[i] - pt_c[i - 1];
            double x = span > 0 ? (t - pt_c[i - 1]) / span : 1;
            return pt_p[i - 1] + (pt_p[i] - pt_p[i - 1]) * x;
        }
    }
    return pt_p[npoints - 1];
}

static float commanded = -1;
static float approach(float target, int fast) {
    if (commanded < 0) commanded = target;
    float scale = (float)(interval / 2.0);
    if (scale < 0.5f) scale = 0.5f;
    float up = (fast ? 1200.f : 250.f) * scale;
    float down = 200.f * scale;
    if (target > commanded + up) commanded += up;
    else if (target < commanded - down) commanded -= down;
    else commanded = target;
    return commanded;
}

static void write_status(double temp, double rpm, double percent, int active) {
    char path[1024];
    if (!user_file("fan-status.json", path, sizeof path)) return;
    char tmp[1100];
    snprintf(tmp, sizeof tmp, "%s.tmp", path);
    FILE *f = fopen(tmp, "w");
    if (!f) return;
    fprintf(f, "{\"temp\":%.1f,\"rpm\":%.0f,\"percent\":%.1f,\"active\":%s}\n", temp < 0 ? 0 : temp, rpm < 0 ? 0 : rpm, percent, active ? "true" : "false");
    fclose(f);
    rename(tmp, path);
}

static void print_curve(void) {
    for (int i = 0; i < npoints; i++) printf(" %.0fC/%.0f%%", pt_c[i], pt_p[i]);
}

static void status(void) {
    char mode[32];
    read_control(mode, sizeof mode);
    char which[5] = "-";
    double raw = hottest(which);
    double peak = -1;
    double sample = control_sample(&peak);
    for (int i = 0; i < nfans; i++)
        printf("Fan %d: %.0f rpm (target %.0f, min %.0f, max %.0f), %s\n", i, smc_val(fkey(i, "Ac")), smc_val(fkey(i, "Tg")), fmn[i], fmx[i], smc_val(fkey(i, "md")) > 0 ? "manual" : "automatic");
    printf("Deciding sensors (%s): hottest %s = %.1f C, group sample %.1f C, raw max %.1f C\n", sensor_prefixes, which, raw, sample, peak);
    printf("Curve");
    print_curve();
    printf(", takeover %.0f C, every %.0f s\n", t_start, interval);
    printf("Service mode: %s\n", mode);
}

static int run(void) {
    int active = 0, hot_ticks = 0, load_ticks = 0;
    double floor_rpm = fmin0;
    float last = -1;
    char last_mode[32] = "curve";
    printf("opensurge-fan running: sensors %s, curve", sensor_prefixes);
    print_curve();
    printf(", every %.0f s\n", interval);
    fflush(stdout);
    while (!stop) {
        char mode[32];
        read_control(mode, sizeof mode);
        if (strcmp(mode, last_mode)) {
            stamp();
            printf("mode %s -> %s\n", last_mode, mode);
            fflush(stdout);
            snprintf(last_mode, sizeof last_mode, "%s", mode);
            active = 0;
            last = -1;
            commanded = -1;
            hot_ticks = 0;
            load_ticks = 0;
            set_auto();
        }
        double raw_max = -1;
        double sample = control_sample(&raw_max);
        int rising_load = sample >= 0 && ema >= 0 && sample >= ema + 3.0;
        if (rising_load) load_ticks++;
        else load_ticks = 0;
        int fast_load = load_ticks >= 2;
        double t = smooth(sample, fast_load);
        int forced = mode[0] >= '0' && mode[0] <= '9';
        int fast = fast_load || raw_max >= 95;
        if (npoints > 0 && raw_max >= pt_c[npoints - 1]) hot_ticks++;
        else hot_ticks = 0;
        if (hot_ticks * interval >= hold) fast = 1;
        if (!strcmp(mode, "auto")) {
            write_status(t, current_rpm(), curve_percent(t < 0 ? pt_c[0] : t), 0);
            nap();
            continue;
        }
        if (!forced && sample < 0 && !active) {
            write_status(t, current_rpm(), 0, 0);
            nap();
            continue;
        }
        if (!forced && !active && t >= t_start) {
            active = 1;
            floor_rpm = smc_val("F0Ac");
            if (floor_rpm < fmin0) floor_rpm = fmin0;
            commanded = (float)floor_rpm;
            last = -1;
        }
        if (!forced && active && t < t_start - 5) {
            active = 0;
            last = -1;
            commanded = -1;
            set_auto();
            stamp();
            printf("control %.1f C -> automatic\n", t);
            fflush(stdout);
        }
        if (forced || active) {
            double pct = curve_percent(t);
            if (forced) {
                double p = atof(mode);
                if (p < 0) p = 0;
                if (p > 100) p = 100;
                if (p > pct) pct = p;
            }
            float target = (float)(fmin0 + (fmax0 - fmin0) * pct / 100.0);
            if (!forced && target < floor_rpm) target = (float)floor_rpm;
            if (commanded < 0) commanded = (float)current_rpm();
            float rpm = approach(target, fast);
            float delta = rpm - last;
            if (delta < 0) delta = -delta;
            if (last < 0 || delta >= 1) {
                int r = set_manual(rpm);
                last = rpm;
                stamp();
                printf("control %.1f C -> %.0f rpm%s%s\n", t, rpm, fast ? " fast" : "", r ? " (WRITE FAILED)" : "");
                fflush(stdout);
            }
            write_status(t, rpm, pct, 1);
        } else {
            write_status(t, current_rpm(), curve_percent(t < 0 ? pt_c[0] : t), 0);
        }
        nap();
    }
    set_auto();
    write_status(ema, current_rpm(), 0, 0);
    printf("Stopped, fans back on automatic.\n");
    return 0;
}

int main(int argc, char **argv) {
    const char *conf = getenv("FANGUARD_CONF");
    if (!conf) conf = access(CONF_FILE, R_OK) == 0 ? CONF_FILE : NULL;
    if (!conf) {
        static char beside[1024];
        snprintf(beside, sizeof beside, "%s", argv[0]);
        char *slash = strrchr(beside, '/');
        if (slash) strcpy(slash + 1, "fanguard.conf");
        else strcpy(beside, "fanguard.conf");
        conf = beside;
    }
    load_conf(conf);
    io_service_t sv = IOServiceGetMatchingService(kIOMainPortDefault, IOServiceMatching("AppleSMC"));
    if (!sv || IOServiceOpen(sv, mach_task_self(), 0, &con)) {
        fprintf(stderr, "Cannot open the SMC.\n");
        return 1;
    }
    if (sv) IOObjectRelease(sv);
    const char *cmd = argc > 1 ? argv[1] : "status";
    find_sensors();
    read_fans();

    if (!strcmp(cmd, "line")) {
        char which[5] = "-";
        double t = hottest(which);
        char mode[32];
        read_control(mode, sizeof mode);
        printf("%s %.1f %.0f %s %s\n", which, t, current_rpm(), is_manual() ? "manual" : "auto", mode);
        return 0;
    }
    if (!strcmp(cmd, "status")) {
        status();
        return 0;
    }
    if (!strcmp(cmd, "sensors")) {
        for (int i = 0; i < nsens; i++) printf("%s %s %.1f\n", decides[i] ? "*" : " ", skey[i], smc_val(skey[i]));
        printf("* = deciding (sensors=%s)\n", sensor_prefixes);
        return 0;
    }
    if (geteuid() != 0) {
        fprintf(stderr, "'%s' writes to the SMC and needs root.\n", cmd);
        return 2;
    }
    signal(SIGINT, on_signal);
    signal(SIGTERM, on_signal);
    if (!strcmp(cmd, "auto")) {
        int r = set_auto();
        printf("Automatic %s\n", r ? "FAILED" : "restored");
        return r != 0;
    }
    if (!strcmp(cmd, "set") && argc > 2) {
        double p = atof(argv[2]);
        if (p < 0) p = 0;
        if (p > 100) p = 100;
        float rpm = (float)(fmin0 + (fmax0 - fmin0) * p / 100);
        int r = set_manual(rpm);
        printf("Fan %s %.0f %% = %.0f rpm\n", r ? "FAILED to set to" : "set to", p, rpm);
        return r != 0;
    }
    if (!strcmp(cmd, "run")) return run();
    fprintf(stderr, "Commands: status | sensors | line | set <0-100> | auto | run\n");
    return 2;
}
