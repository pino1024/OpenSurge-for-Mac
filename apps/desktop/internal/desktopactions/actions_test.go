package desktopactions

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHostMetersActionIsArgumentFree(t *testing.T) {
	calls := 0
	a := &Actions{HostMeters: func() any { calls++; return map[string]any{"cpu": 12.0, "history": []float64{12}} }}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("POST", "/desktop/v1/host-meters", strings.NewReader(`{}`)))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"cpu":12`) || calls != 1 {
		t.Fatalf("host meters: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("POST", "/desktop/v1/host-meters", strings.NewReader(`{"page":"network"}`)))
	if w.Code != 400 {
		t.Fatalf("host meters rejected extra arguments with %d", w.Code)
	}
}

func TestFanControlReadsAndWrites(t *testing.T) {
	a := &Actions{FanControl: func() any {
		return map[string]any{"enabled": true, "points": []FanPoint{{C: 60, P: 0}, {C: 72, P: 26}, {C: 85, P: 100}}}
	}}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("POST", "/desktop/v1/fan-control", strings.NewReader(`{}`)))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Fatalf("fan read: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("POST", "/desktop/v1/fan-control", strings.NewReader(`{"fan_enabled":false}`)))
	if w.Code != 400 {
		t.Fatalf("partial fan write: %d", w.Code)
	}
	var saved bool
	a.SetFan = func(visible, enabled bool, points []FanPoint) (any, error) {
		saved = visible && !enabled && len(points) == 2 && points[0] == (FanPoint{C: 55, P: 0}) && points[1] == (FanPoint{C: 80, P: 100})
		return map[string]any{"visible": visible, "enabled": enabled, "points": points}, nil
	}
	w = httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("POST", "/desktop/v1/fan-control", strings.NewReader(`{"fan_visible":true,"fan_enabled":false,"fan_points":[{"c":55,"p":0},{"c":80,"p":100}]}`)))
	if w.Code != 200 || !saved || !strings.Contains(w.Body.String(), `"c":55`) {
		t.Fatalf("fan write: %d saved=%v %s", w.Code, saved, w.Body.String())
	}
}

func TestBrowserActionIsArgumentFreeAndDoesNotExposeGrant(t *testing.T) {
	calls := 0
	a := &Actions{OpenBrowser: func(context.Context) error { calls++; return nil }}
	for _, body := range []string{`{}`, `{"url":"https://example.com"}`, `{"page":"network"}`, `{"enabled":true}`} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("POST", "/desktop/v1/open-browser", strings.NewReader(body)))
		want := 400
		if body == `{}` {
			want = 200
			if strings.TrimSpace(w.Body.String()) != `{"ok":true}` {
				t.Fatal("browser response must contain only acknowledgement")
			}
		}
		if w.Code != want {
			t.Fatalf("%s: got %d", body, w.Code)
		}
	}
	if calls != 1 {
		t.Fatalf("unexpected browser actions: %d", calls)
	}
	a.OpenBrowser = func(context.Context) error { return errors.New("opener failed: /bootstrap?code=private-grant") }
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("POST", "/desktop/v1/open-browser", strings.NewReader(`{}`)))
	if w.Code != 503 || strings.Contains(w.Body.String(), "private-grant") {
		t.Fatal("browser failure must be reported without exposing its grant")
	}
}

func TestClosedNativeCapabilities(t *testing.T) {
	opened := ""
	a := &Actions{OpenExternal: func(value string) error { opened = value; return nil }}
	for _, test := range []struct {
		path, body string
		want       int
	}{
		{"open-external", `{"url":"https://github.com/YTwsy/OpenSurge-for-Mac"}`, 200},
		{"open-external", `{"url":"file:///tmp/private"}`, 400},
		{"open-external", `{"url":"https://token@example.com"}`, 400},
		{"open-external", `{"url":"https://example.com\u0000"}`, 400},
		{"open-external", `{"url":"https://example.com","command":"run"}`, 400},
		{"language", `{"language":"invalid"}`, 400},
		{"exec", `{"text":"echo no"}`, 404},
	} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("POST", "/desktop/v1/"+test.path, strings.NewReader(test.body)))
		if w.Code != test.want {
			t.Fatalf("%s: got %d", test.body, w.Code)
		}
	}
	if opened != "https://github.com/YTwsy/OpenSurge-for-Mac" {
		t.Fatal("an untrusted URL reached the native opener")
	}
}

func TestTrayNavigationAndAppearance(t *testing.T) {
	target, theme := "", ""
	a := &Actions{ShowMain: func(value string) { target = value }, SetTrayAppearance: func(value string) { theme = value }}
	for _, test := range []struct {
		action, body string
		want         int
		wantTarget   string
	}{
		{"show-main", `{"page":"devices"}`, 200, "devices"},
		{"show-main", `{"page":"dashboard","section":"active-devices"}`, 200, "dashboard#active-devices"},
		{"show-main", `{"page":"connections","owner":"device:tv & family"}`, 200, "connections?owner=device%3Atv+%26+family"},
		{"show-main", `{"page":"https://example.com"}`, 400, ""},
		{"show-main", `{"page":"network","owner":"device:tv"}`, 400, ""},
		{"show-main", `{"page":"connections","owner":"bad\nowner"}`, 400, ""},
		{"show-main", `{"page":"network","section":"active-devices"}`, 400, ""},
		{"show-main", `{"page":"dashboard","section":"active-devices?owner=all"}`, 400, ""},
		{"tray-appearance", `{"theme":"light"}`, 200, ""},
		{"tray-appearance", `{"theme":"dark"}`, 200, ""},
		{"tray-appearance", `{"theme":"arbitrary"}`, 400, ""},
	} {
		target = ""
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("POST", "/desktop/v1/"+test.action, strings.NewReader(test.body)))
		if w.Code != test.want {
			t.Fatalf("%s: got %d: %s", test.body, w.Code, w.Body.String())
		}
		if target != test.wantTarget {
			t.Fatalf("%s: target=%q, want %q", test.body, target, test.wantTarget)
		}
	}
	if theme != "dark" {
		t.Fatalf("target=%q theme=%q", target, theme)
	}
}

func TestTrayMenuIsArgumentFree(t *testing.T) {
	calls := 0
	a := &Actions{ToggleTrayMenu: func() { calls++ }}
	for _, body := range []string{`{}`, `{"page":"network"}`, `{"url":"https://example.com"}`, `{"height":2000}`} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("POST", "/desktop/v1/toggle-tray-menu", strings.NewReader(body)))
		want := 400
		if body == `{}` {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("%s: got %d", body, w.Code)
		}
	}
	if calls != 1 {
		t.Fatalf("unexpected menu toggles: %d", calls)
	}
}

func TestSettingsCapabilities(t *testing.T) {
	calls, theme := 0, ""
	a := &Actions{ShowSettings: func() { calls++ }, SetSettingsAppearance: func(value string) { theme = value }}
	for _, test := range []struct {
		action, body string
		want         int
	}{
		{"show-settings", `{}`, 200},
		{"show-settings", `{"page":"dashboard"}`, 400},
		{"show-settings", `{"url":"https://example.com"}`, 400},
		{"show-settings", `{"enabled":true}`, 400},
		{"settings-appearance", `{"theme":"dark"}`, 200},
		{"settings-appearance", `{"theme":"light"}`, 200},
		{"settings-appearance", `{"theme":"system"}`, 200},
		{"settings-appearance", `{"theme":"arbitrary"}`, 400},
	} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("POST", "/desktop/v1/"+test.action, strings.NewReader(test.body)))
		if w.Code != test.want {
			t.Fatalf("%s: %s: got %d", test.action, test.body, w.Code)
		}
	}
	if calls != 1 || theme != "system" {
		t.Fatalf("calls=%d theme=%q", calls, theme)
	}
}

func TestDNSExportIsBoundedAndUsesNativeSaveSelection(t *testing.T) {
	calls := 0
	a := &Actions{SaveDNSFile: func(name, text string) (bool, error) {
		calls++
		if name != "custom-dns.yaml" || text != "hosts: {}\n" {
			t.Fatalf("unexpected export: %q %q", name, text)
		}
		return true, nil
	}}
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"filename":"custom-dns.yaml","text":"hosts: {}\n"}`, 200},
		{`{"filename":"/tmp/custom-dns.yaml","text":"hosts: {}"}`, 400},
		{`{"filename":"custom-dns.yaml","text":"hosts: {}","url":"https://example.com"}`, 400},
		{`{"filename":"custom-dns.yaml","text":""}`, 400},
	} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("POST", "/desktop/v1/save-dns-file", strings.NewReader(tc.body)))
		if w.Code != tc.want {
			t.Fatalf("got %d: %s", w.Code, w.Body.String())
		}
	}
	if calls != 1 {
		t.Fatalf("save panel calls=%d", calls)
	}
}
