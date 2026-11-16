// Package trayactivity retains a bounded view of Control API observations even
// when both WebViews are hidden. Rate calculation remains in the Control Service.
package trayactivity

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

const maxGap = 15 * time.Second // Same validity window as the Control API sampler.

type Point struct {
	Time     int64 `json:"time"`
	Upload   int64 `json:"upload"`
	Download int64 `json:"download"`
}

type Snapshot struct {
	Traffic         json.RawMessage `json:"traffic"`
	History         []Point         `json:"history"`
	Failed          bool            `json:"failed"`
	LocalDownload   int64           `json:"local_download"`
	GatewayDownload int64           `json:"gateway_download"`
}

type Monitor struct {
	read       func(context.Context) ([]byte, error)
	now        func() time.Time
	changed    func(Snapshot)
	mu         sync.Mutex
	op         sync.Mutex
	running    bool
	generation uint64
	cancel     context.CancelFunc
	snapshot   Snapshot
	sampledAt  time.Time
	failures   int
	wake       chan struct{}
}

// FormatDownloadRate is a fixed 4-character menubar label (no "/s") so the
// status item does not change width as the rate ticks.
func FormatDownloadRate(bytesPerSecond int64) string {
	if bytesPerSecond < 0 {
		bytesPerSecond = 0
	}
	value, unit := float64(bytesPerSecond), byte('B')
	switch {
	case bytesPerSecond >= 1_000_000_000:
		value, unit = float64(bytesPerSecond)/1_000_000_000, 'G'
	case bytesPerSecond >= 1_000_000:
		value, unit = float64(bytesPerSecond)/1_000_000, 'M'
	case bytesPerSecond >= 1000:
		value, unit = float64(bytesPerSecond)/1000, 'K'
	}
	label := ""
	switch {
	case unit == 'B':
		label = fmt.Sprintf("%3dB", bytesPerSecond)
	case value < 9.95:
		label = fmt.Sprintf("%3.1f%c", value, unit)
	default:
		rounded := math.Round(value)
		if rounded >= 1000 {
			switch unit {
			case 'K':
				label = "1.0M"
			case 'M':
				label = "1.0G"
			default:
				label = "1.0T"
			}
		} else {
			label = fmt.Sprintf("%3.0f%c", rounded, unit)
		}
	}
	// Figure spaces keep leading pads; AppKit trims ASCII spaces on status items.
	return strings.ReplaceAll(label, " ", "\u2007")
}

// FormatCPUPercent is a fixed 4-character host CPU label (`  8%`, `100%`).
func FormatCPUPercent(percent float64) string {
	if math.IsNaN(percent) || percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	n := int(math.Round(percent))
	if n > 100 {
		n = 100
	}
	return strings.ReplaceAll(fmt.Sprintf("%3d%%", n), " ", "\u2007")
}

// FormatMemoryPercent is the same 4-character percent width as CPU.
func FormatMemoryPercent(percent float64) string {
	return FormatCPUPercent(percent)
}

// FormatFanRPM is a fixed 4-character fan speed (`1000`, `4900`).
func FormatFanRPM(rpm float64, ok bool) string {
	if !ok || math.IsNaN(rpm) || rpm < 0 {
		return "\u2007\u2007\u2007-"
	}
	n := int(math.Round(rpm))
	if n > 9999 {
		n = 9999
	}
	return strings.ReplaceAll(fmt.Sprintf("%4d", n), " ", "\u2007")
}

// FormatCPUTemp is a fixed 4-character CPU-core temperature (` 42C`, `100C`).
func FormatCPUTemp(celsius float64, ok bool) string {
	if !ok || math.IsNaN(celsius) {
		return "\u2007--C"
	}
	n := int(math.Round(celsius))
	if n < 0 {
		n = 0
	}
	if n > 999 {
		n = 999
	}
	return strings.ReplaceAll(fmt.Sprintf("%3dC", n), " ", "\u2007")
}

// MenuBarShow selects which fixed-width meters appear beside the icon.
type MenuBarShow struct {
	Download    bool
	CPU         bool
	Memory      bool
	Temperature bool
	Fan         bool
}

func DefaultMenuBarShow() MenuBarShow {
	return MenuBarShow{Download: true, CPU: true, Memory: true, Temperature: true, Fan: true}
}

// FormatMenuBarStats is the combined menubar label: rate, CPU, memory, core temp, fan RPM.
func FormatMenuBarStats(bytesPerSecond int64, cpuPercent, memPercent, tempC float64, tempOK bool, fanRPM float64, fanOK bool, show MenuBarShow) string {
	var parts []string
	if show.Download {
		parts = append(parts, FormatDownloadRate(bytesPerSecond))
	}
	if show.CPU {
		parts = append(parts, FormatCPUPercent(cpuPercent))
	}
	if show.Memory {
		parts = append(parts, FormatMemoryPercent(memPercent))
	}
	if show.Temperature {
		parts = append(parts, FormatCPUTemp(tempC, tempOK))
	}
	if show.Fan {
		parts = append(parts, FormatFanRPM(fanRPM, fanOK))
	}
	return strings.Join(parts, "\u2007")
}

// CPUPercent is host occupancy from two HOST_CPU_LOAD_INFO samples.
func CPUPercent(prevUsed, prevTotal, used, total uint64) (float64, bool) {
	if used < prevUsed || total <= prevTotal {
		return 0, false
	}
	delta := total - prevTotal
	if delta == 0 {
		return 0, false
	}
	return 100 * float64(used-prevUsed) / float64(delta), true
}

// MacDownloadRate is this Mac's inbound rate: physical NIC bytes minus
// downstream bypass-router sessions. LAN and DIRECT downloads on the NIC stay
// included; phone traffic through OpenSurge is removed.
func MacDownloadRate(linkBytesPerSecond int64, snapshot Snapshot) int64 {
	if linkBytesPerSecond < 0 {
		linkBytesPerSecond = 0
	}
	if snapshot.Failed || len(snapshot.Traffic) == 0 {
		return linkBytesPerSecond
	}
	downstream := snapshot.GatewayDownload - snapshot.LocalDownload
	if downstream < 0 {
		downstream = 0
	}
	rate := linkBytesPerSecond - downstream
	if rate < 0 {
		return 0
	}
	return rate
}

func New(read func(context.Context) ([]byte, error)) *Monitor {
	return &Monitor{read: read, now: time.Now, wake: make(chan struct{}, 1)}
}

func (m *Monitor) SetChanged(changed func(Snapshot)) {
	m.mu.Lock()
	m.changed = changed
	m.mu.Unlock()
}

func (m *Monitor) SetRunning(running bool) {
	m.mu.Lock()
	if m.running == running {
		m.mu.Unlock()
		return
	}
	m.running = running
	m.generation++
	m.snapshot, m.sampledAt, m.failures = Snapshot{}, time.Time{}, 0
	if m.cancel != nil {
		m.cancel()
	}
	select {
	case m.wake <- struct{}{}:
	default:
	}
	m.mu.Unlock()
	m.emit()
}

func (m *Monitor) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.copySnapshotLocked()
}

func (m *Monitor) copySnapshotLocked() Snapshot {
	if !m.sampledAt.IsZero() && (m.now().Sub(m.sampledAt) > maxGap || m.now().Before(m.sampledAt)) {
		return Snapshot{Failed: true}
	}
	return Snapshot{
		Traffic:         append(json.RawMessage(nil), m.snapshot.Traffic...),
		History:         append([]Point(nil), m.snapshot.History...),
		Failed:          m.snapshot.Failed,
		LocalDownload:   m.snapshot.LocalDownload,
		GatewayDownload: m.snapshot.GatewayDownload,
	}
}

func (m *Monitor) emit() {
	m.mu.Lock()
	snap := m.copySnapshotLocked()
	changed := m.changed
	m.mu.Unlock()
	if changed != nil {
		changed(snap)
	}
}

func (m *Monitor) refresh(ctx context.Context) {
	m.op.Lock()
	defer m.op.Unlock()
	m.mu.Lock()
	if !m.running || ctx.Err() != nil {
		m.mu.Unlock()
		return
	}
	generation := m.generation
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	m.cancel = cancel
	m.mu.Unlock()
	defer cancel()
	data, err := m.read(ctx)
	var sample struct {
		SchemaVersion   int       `json:"schema_version"`
		SampledAt       time.Time `json:"sampled_at"`
		ConnectionError string    `json:"connection_error"`
		Rates           struct {
			Upload   int64 `json:"upload"`
			Download int64 `json:"download"`
		} `json:"gateway_rates"`
		Local struct {
			DownloadRate int64 `json:"download_rate"`
		} `json:"gateway_local"`
	}
	parseErr := json.Unmarshal(data, &sample)
	notify := false
	m.mu.Lock()
	m.cancel = nil
	if generation != m.generation || !m.running || ctx.Err() == context.Canceled {
		m.mu.Unlock()
		return // A stopped/restarted gateway cannot be overwritten by an old read.
	}
	if err != nil || ctx.Err() != nil || parseErr != nil || sample.SchemaVersion != 1 || sample.SampledAt.IsZero() || sample.ConnectionError != "" || m.now().Sub(sample.SampledAt) > maxGap || sample.SampledAt.After(m.now()) {
		m.snapshot, m.sampledAt = Snapshot{Failed: true}, time.Time{}
		m.failures++
		notify = true
	} else {
		m.failures = 0
		history := m.snapshot.History
		if sample.SampledAt.Before(m.sampledAt) || sample.SampledAt.Sub(m.sampledAt) > maxGap {
			history = nil // Sleep/service gaps need a new baseline, never invented zeroes.
		}
		if !sample.SampledAt.Equal(m.sampledAt) {
			history = append(history, Point{Time: sample.SampledAt.UnixMilli(), Upload: sample.Rates.Upload, Download: sample.Rates.Download})
		}
		cutoff := sample.SampledAt.Add(-time.Minute).UnixMilli()
		for len(history) > 30 || (len(history) > 0 && history[0].Time <= cutoff) {
			history = history[1:]
		}
		m.sampledAt = sample.SampledAt
		m.snapshot = Snapshot{Traffic: append(json.RawMessage(nil), data...), History: history, LocalDownload: sample.Local.DownloadRate, GatewayDownload: sample.Rates.Download}
		notify = true
	}
	m.mu.Unlock()
	if notify {
		m.emit()
	}
}

func (m *Monitor) Run(ctx context.Context) {
	for ctx.Err() == nil {
		m.refresh(ctx)
		m.mu.Lock()
		running, failures := m.running, m.failures
		m.mu.Unlock()
		var tick <-chan time.Time
		var timer *time.Timer
		if running {
			timer = time.NewTimer(min(2*time.Second*time.Duration(1<<min(failures, 4)), 30*time.Second))
			tick = timer.C
		}
		select {
		case <-ctx.Done():
		case <-m.wake:
		case <-tick:
		}
		if timer != nil {
			timer.Stop()
		}
	}
}
