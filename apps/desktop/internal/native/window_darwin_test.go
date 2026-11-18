package native

import "testing"

func TestHostMemoryPercent(t *testing.T) {
	p, ok := HostMemoryPercent()
	if !ok {
		t.Fatal("host memory statistics unavailable")
	}
	if p < 0 || p > 100 {
		t.Fatalf("implausible memory percent %.1f", p)
	}
	t.Logf("host memory %.1f%%", p)
}

func TestHostDiskPercent(t *testing.T) {
	p, ok := HostDiskPercent()
	if !ok {
		t.Fatal("host disk statistics unavailable")
	}
	if p < 0 || p > 100 {
		t.Fatalf("implausible disk percent %.1f", p)
	}
	t.Logf("host disk %.1f%%", p)
}

func TestFanSample(t *testing.T) {
	c, cOK, rpm, rpmOK, minRPM, maxRPM := FanSample()
	if !rpmOK {
		t.Fatal("fan speed unavailable")
	}
	if rpm < 0 || rpm > 20000 {
		t.Fatalf("implausible fan rpm %.0f", rpm)
	}
	if maxRPM <= minRPM || minRPM < 0 || maxRPM > 20000 {
		t.Fatalf("implausible fan range %.0f–%.0f", minRPM, maxRPM)
	}
	if cOK && (c < 5 || c > 130) {
		t.Fatalf("implausible fan sensor %.1fC", c)
	}
	t.Logf("fan sensor %.1fC ok=%v rpm %.0f range %.0f–%.0f", c, cOK, rpm, minRPM, maxRPM)
}

func TestCoreCPUTicks(t *testing.T) {
	used, total, ok := CoreCPUTicks()
	if !ok || len(used) < 2 || len(used) != len(total) {
		t.Fatalf("cores used=%d total=%d ok=%v", len(used), len(total), ok)
	}
	for i := range used {
		if total[i] < used[i] || total[i] == 0 {
			t.Fatalf("core %d ticks used=%d total=%d", i, used[i], total[i])
		}
	}
	t.Logf("cores %d", len(used))
}

func TestCoreTemps(t *testing.T) {
	temps := CoreTemps()
	used, _, ok := CoreCPUTicks()
	if !ok || len(temps) != len(used) || len(temps) < 2 {
		t.Fatalf("core temps %d ticks %d ok=%v", len(temps), len(used), ok)
	}
	for i, temp := range temps {
		if temp < 5 || temp > 130 {
			t.Fatalf("core %d temp %.1f", i, temp)
		}
	}
	t.Logf("core temps %v", temps)
}

func TestSensorGroupTemps(t *testing.T) {
	power, gpu, performance, ok := SensorGroupTemps()
	if !ok {
		t.Fatal("sensor groups unavailable")
	}
	for _, temp := range []float64{power, gpu, performance} {
		if temp < 5 || temp > 130 {
			t.Fatalf("group temps power=%.1f gpu=%.1f performance=%.1f", power, gpu, performance)
		}
	}
	t.Logf("groups power %.1f gpu %.1f performance %.1f", power, gpu, performance)
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
