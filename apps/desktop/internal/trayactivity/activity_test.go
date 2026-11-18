package trayactivity

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func observation(at time.Time) []byte {
	data, _ := json.Marshal(map[string]any{"schema_version": 1, "sampled_at": at, "gateway_rates": map[string]int{"upload": 125, "download": 4000}, "gateway_local": map[string]int{"download_rate": 2500}, "devices": []any{}})
	return data
}

func TestFormatDownloadRate(t *testing.T) {
	pad := "\u00A0"
	cases := map[int64]string{
		0: pad + pad + "0B", 12: pad + "12B", 999: "999B", 1000: pad + pad + "1K", 1500: pad + pad + "2K",
		9_449: pad + pad + "9K", 9_500: pad + "10K", 12_300: pad + "12K", 125_000: "125K",
		999_500: pad + pad + "1M", 1_500_000: pad + pad + "2M", 12_000_000: pad + "12M",
	}
	for input, want := range cases {
		got := FormatDownloadRate(input)
		if got != want {
			t.Fatalf("FormatDownloadRate(%d)=%q want %q", input, got, want)
		}
		if n := len([]rune(got)); n != 4 {
			t.Fatalf("FormatDownloadRate(%d)=%q width %d", input, got, n)
		}
	}
}

func TestFormatCPUPercentAndMenuBarStats(t *testing.T) {
	pad := "\u00A0"
	for input, want := range map[float64]string{
		0: pad + pad + "0%", 8.2: pad + pad + "8%", 23.4: pad + "23%", 99.5: "100%", 100: "100%", -3: pad + pad + "0%", 140: "100%",
	} {
		got := FormatCPUPercent(input)
		if got != want {
			t.Fatalf("FormatCPUPercent(%v)=%q want %q", input, got, want)
		}
		if n := len([]rune(got)); n != 4 {
			t.Fatalf("FormatCPUPercent(%v)=%q width %d", input, got, n)
		}
	}
	if got, want := FormatCPUTemp(42.4, true), pad+"42C"; got != want {
		t.Fatalf("FormatCPUTemp=%q want %q", got, want)
	}
	if got, want := FormatCPUTemp(0, false), pad+"--C"; got != want {
		t.Fatalf("missing temp=%q", got)
	}
	if got, want := FormatMemoryPercent(35.4), pad+"35%"; got != want {
		t.Fatalf("FormatMemoryPercent=%q want %q", got, want)
	}
	if got, want := FormatDiskPercent(80.4), pad+"80%"; got != want {
		t.Fatalf("FormatDiskPercent=%q want %q", got, want)
	}
	if got, want := FormatFanRPM(3500, true), "3500"; got != want {
		t.Fatalf("FormatFanRPM=%q want %q", got, want)
	}
	if got, want := FormatFanRPM(0, false), pad+pad+pad+"-"; got != want {
		t.Fatalf("missing fan=%q", got)
	}
	got := FormatMenuBarStats(12_300, 8.2, 35.4, 80.4, 42.4, true, 3500, true, DefaultMenuBarShow())
	want := pad + "12K" + pad + pad + pad + "8%" + pad + pad + "35%" + pad + pad + "80%" + pad + pad + "42C" + pad + "3500"
	if got != want {
		t.Fatalf("FormatMenuBarStats=%q want %q", got, want)
	}
	if n := len([]rune(got)); n != 29 {
		t.Fatalf("combined width %d", n)
	}
	if got := FormatMenuBarStats(12_300, 8.2, 35.4, 80.4, 42.4, true, 0, false, MenuBarShow{Temperature: true}); got != pad+"42C" {
		t.Fatalf("temp-only=%q", got)
	}
	if got := FormatMenuBarStats(12_300, 8.2, 35.4, 80.4, 42.4, true, 0, false, MenuBarShow{}); got != "" {
		t.Fatalf("hidden=%q", got)
	}
	if p, ok := CPUPercent(10, 100, 25, 200); !ok || p != 15 {
		t.Fatalf("CPUPercent=%v ok=%v", p, ok)
	}
	if _, ok := CPUPercent(50, 100, 40, 90); ok {
		t.Fatal("wrapped ticks must be skipped")
	}
}

func TestMacDownloadRateKeepsLocalLinkAndDropsDownstream(t *testing.T) {
	live := Snapshot{Traffic: []byte(`{}`), LocalDownload: 1_000_000, GatewayDownload: 4_000_000}
	if got := MacDownloadRate(5_000_000, live); got != 2_000_000 {
		t.Fatalf("mac rate = %d", got)
	}
	if got := MacDownloadRate(800_000, live); got != 0 {
		t.Fatalf("clamped rate = %d", got)
	}
	if got := MacDownloadRate(3_000_000, Snapshot{Failed: true}); got != 3_000_000 {
		t.Fatalf("nic-only rate = %d", got)
	}
}

func TestLocalDownloadUsesGatewayLocalRate(t *testing.T) {
	now := time.Now().UTC()
	m := New(func(context.Context) ([]byte, error) { return observation(now), nil })
	m.now = func() time.Time { return now }
	var last Snapshot
	m.SetChanged(func(snapshot Snapshot) { last = snapshot })
	m.SetRunning(true)
	m.refresh(context.Background())
	if last.Failed || last.LocalDownload != 2500 || last.GatewayDownload != 4000 || FormatDownloadRate(last.LocalDownload) != "\u00A0\u00A03K" {
		t.Fatalf("local download = %+v", last)
	}
}

func TestHistorySurvivesWithoutRendererReadsAndRemainsBounded(t *testing.T) {
	now := time.Now().UTC()
	reads := 0
	m := New(func(context.Context) ([]byte, error) { reads++; return observation(now), nil })
	m.now = func() time.Time { return now }
	m.refresh(context.Background())
	if reads != 0 {
		t.Fatal("stopped gateways must not be sampled")
	}
	m.SetRunning(true)
	// No renderer reads occur for two minutes. The native loop still retains
	// exactly the latest minute; reopening needs no new baseline from the UI.
	for range 60 {
		m.refresh(context.Background())
		now = now.Add(2 * time.Second)
	}
	snapshot := m.Snapshot()
	if snapshot.Failed || len(snapshot.History) != 30 || len(snapshot.Traffic) == 0 || snapshot.History[0].Time != now.Add(-time.Minute).UnixMilli() {
		t.Fatalf("unexpected history: %+v", snapshot)
	}
	last := snapshot.History[29]
	if last.Download != 4000 || last.Upload != 125 {
		t.Fatal("native cache must preserve API rates")
	}
	snapshot.History[0].Download = -1
	snapshot.Traffic[0] = '!'
	if next := m.Snapshot(); next.History[0].Download < 0 || !json.Valid(next.Traffic) {
		t.Fatal("callers must not mutate the retained snapshot")
	}
	m.SetRunning(false)
	m.refresh(context.Background())
	if snapshot := m.Snapshot(); reads != 60 || len(snapshot.Traffic) != 0 || len(snapshot.History) != 0 {
		t.Fatal("stop must clear observations and cease reads")
	}
}

func TestGapsDuplicateSamplesAndFailures(t *testing.T) {
	now := time.Now().UTC()
	var readErr error
	m := New(func(context.Context) ([]byte, error) { return observation(now), readErr })
	m.now = func() time.Time { return now }
	m.SetRunning(true)
	m.refresh(context.Background())
	m.refresh(context.Background())
	if len(m.Snapshot().History) != 1 {
		t.Fatal("cached API timestamps must not become duplicate chart points")
	}
	now = now.Add(2 * time.Second)
	m.refresh(context.Background())
	if len(m.Snapshot().History) != 2 {
		t.Fatal("second observation should make rates ready")
	}
	now = now.Add(20 * time.Second)
	if snapshot := m.Snapshot(); !snapshot.Failed || len(snapshot.Traffic) != 0 {
		t.Fatal("stale observations after sleep cannot look live")
	}
	m.refresh(context.Background())
	if len(m.Snapshot().History) != 1 {
		t.Fatal("sleep gap requires a fresh baseline")
	}
	readErr = errors.New("offline")
	m.refresh(context.Background())
	if snapshot := m.Snapshot(); !snapshot.Failed || len(snapshot.History) != 0 || len(snapshot.Traffic) != 0 {
		t.Fatal("a failed read must not appear as zero or healthy traffic")
	}
	readErr = nil
	now = now.Add(2 * time.Second)
	m.refresh(context.Background())
	if snapshot := m.Snapshot(); snapshot.Failed || len(snapshot.History) != 1 {
		t.Fatal("recovery needs a fresh baseline")
	}
}

func TestStopCancelsReadAndRejectsLateResult(t *testing.T) {
	started, done := make(chan struct{}), make(chan struct{})
	m := New(func(ctx context.Context) ([]byte, error) {
		close(started)
		<-ctx.Done()
		return observation(time.Now().UTC()), nil // Simulate a client returning late.
	})
	m.SetRunning(true)
	go func() { m.refresh(context.Background()); close(done) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("read did not start")
	}
	m.SetRunning(false)
	m.SetRunning(true)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stop did not cancel pending observation")
	}
	if snapshot := m.Snapshot(); len(snapshot.Traffic) != 0 || len(snapshot.History) != 0 {
		t.Fatal("old generation overwrote a restarted gateway")
	}
}

func TestRunWakesForGatewayAndCancelsWithHost(t *testing.T) {
	read := make(chan struct{}, 4)
	m := New(func(context.Context) ([]byte, error) { read <- struct{}{}; return observation(time.Now().UTC()), nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	m.SetRunning(true)
	select {
	case <-read:
	case <-time.After(time.Second):
		t.Fatal("gateway start did not wake the native observation loop")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("host exit did not stop the observation loop")
	}
}
