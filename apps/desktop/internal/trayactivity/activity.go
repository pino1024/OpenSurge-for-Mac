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

// menuBarPad is a no-break space. AppKit trims ASCII spaces on status items,
// and figure spaces are missing from SF Mono so they pick up a narrower font.
const menuBarPad = "\u00A0"

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

// FormatDownloadRate is a fixed 4-character integer menubar label (no "/s",
// no decimal) so the status item does not change width as the rate ticks.
func FormatDownloadRate(bytesPerSecond int64) string {
	if bytesPerSecond < 0 {
		bytesPerSecond = 0
	}
	n, unit := bytesPerSecond, byte('B')
	switch {
	case bytesPerSecond >= 1_000_000_000:
		n, unit = int64(math.Round(float64(bytesPerSecond)/1_000_000_000)), 'G'
	case bytesPerSecond >= 1_000_000:
		n, unit = int64(math.Round(float64(bytesPerSecond)/1_000_000)), 'M'
	case bytesPerSecond >= 1000:
		n, unit = int64(math.Round(float64(bytesPerSecond)/1000)), 'K'
	}
	if n >= 1000 {
		switch unit {
		case 'K':
			n, unit = 1, 'M'
		case 'M':
			n, unit = 1, 'G'
		case 'G':
			n, unit = 1, 'T'
		}
	}
	if unit != 'B' && n < 1 {
		n = 1
	}
	return strings.ReplaceAll(fmt.Sprintf("%3d%c", n, unit), " ", menuBarPad)
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
	return strings.ReplaceAll(fmt.Sprintf("%3d%%", n), " ", menuBarPad)
}

// FormatMemoryPercent is the same 4-character percent width as CPU.
func FormatMemoryPercent(percent float64) string {
	return FormatCPUPercent(percent)
}

// FormatFanRPM is a fixed 4-character fan speed (`1000`, `4900`).
func FormatFanRPM(rpm float64, ok bool) string {
	if !ok || math.IsNaN(rpm) || rpm < 0 {
		return menuBarPad + menuBarPad + menuBarPad + "-"
	}
	n := int(math.Round(rpm))
	if n > 9999 {
		n = 9999
	}
	return strings.ReplaceAll(fmt.Sprintf("%4d", n), " ", menuBarPad)
}

// FormatCPUTemp is a fixed 4-character CPU-core temperature (` 42C`, `100C`).
func FormatCPUTemp(celsius float64, ok bool) string {
	if !ok || math.IsNaN(celsius) {
		return menuBarPad + "--C"
	}
	n := int(math.Round(celsius))
	if n < 0 {
		n = 0
	}
	if n > 999 {
		n = 999
	}
	return strings.ReplaceAll(fmt.Sprintf("%3dC", n), " ", menuBarPad)
}

// MenuBarShow selects which fixed-width meters appear beside the icon.
type MenuBarShow struct {
	Download    bool
	CPU         bool
	Memory      bool
	Disk        bool
	Temperature bool
	Fan         bool
}

func DefaultMenuBarShow() MenuBarShow {
	return MenuBarShow{Download: true, CPU: true, Memory: true, Disk: true, Temperature: true, Fan: true}
}

// FormatDiskPercent is the same 4-character percent width as CPU and memory.
func FormatDiskPercent(percent float64) string {
	return FormatCPUPercent(percent)
}

// FormatMenuBarStats is the combined menubar label: rate, CPU, memory, disk, core temp, fan RPM.
func FormatMenuBarStats(bytesPerSecond int64, cpuPercent, memPercent, diskPercent, tempC float64, tempOK bool, fanRPM float64, fanOK bool, show MenuBarShow) string {
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
	if show.Disk {
		parts = append(parts, FormatDiskPercent(diskPercent))
	}
	if show.Temperature {
		parts = append(parts, FormatCPUTemp(tempC, tempOK))
	}
	if show.Fan {
		parts = append(parts, FormatFanRPM(fanRPM, fanOK))
	}
	return strings.Join(parts, menuBarPad)
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
