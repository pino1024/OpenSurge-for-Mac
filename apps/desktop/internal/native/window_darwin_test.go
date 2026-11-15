package native

import "testing"

func TestHostMemoryPercent(t *testing.T) {
	p, ok := HostMemoryPercent()
	if !ok {
		t.Fatal("host memory statistics unavailable")
	}
	if p < 1 || p > 100 {
		t.Fatalf("implausible memory percent %.1f", p)
	}
	t.Logf("host memory %.1f%%", p)
}

func TestCPUTempC(t *testing.T) {
	c, ok := CPUTempC()
	if !ok {
		t.Fatal("CPU core temperature sensor not found")
	}
	if c < 10 || c > 120 {
		t.Fatalf("implausible CPU temp %.1fC", c)
	}
	t.Logf("CPU core temp %.1fC", c)
}

func TestExternalURLBoundary(t *testing.T) {
	for _, url := range []string{"https://github.com/YTwsy/OpenSurge-for-Mac", "http://192.0.2.1/"} {
		if !ExternalURLAllowed(url) {
			t.Fatalf("valid link rejected: %s", url)
		}
	}
	for _, url := range []string{"file:///etc/passwd", "javascript:alert(1)", "wails://localhost/api/v1/overview", "https://user:secret@example.com", "mailto:me@example.com", "https:", ""} {
		if ExternalURLAllowed(url) {
			t.Fatalf("unsafe link accepted: %s", url)
		}
	}
}
