package main

import (
	"context"
	_ "embed"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"open-mihomo-gateway/apps/desktop/internal/fan"
	"open-mihomo-gateway/apps/desktop/internal/menustatus"
	"open-mihomo-gateway/apps/desktop/internal/native"
	"open-mihomo-gateway/apps/desktop/internal/trayactivity"
)

//go:embed Resources/OpenSurgeMenuBarIcon.png
var trayIcon []byte

type hostMeterSample struct {
	Download       int64     `json:"download"`
	DownloadOK     bool      `json:"download_ok"`
	CPU            float64   `json:"cpu"`
	CPUOK          bool      `json:"cpu_ok"`
	Memory         float64   `json:"memory"`
	MemoryOK       bool      `json:"memory_ok"`
	Disk           float64   `json:"disk"`
	DiskOK         bool      `json:"disk_ok"`
	Temperature    float64   `json:"temperature"`
	TempOK         bool      `json:"temperature_ok"`
	FanRPM         float64   `json:"fan_rpm"`
	FanOK          bool      `json:"fan_ok"`
	FanVisible     bool      `json:"fan_visible"`
	FanMin         float64   `json:"fan_min"`
	FanMax         float64   `json:"fan_max"`
	ControlTemp    float64   `json:"control_temp"`
	ControlPercent float64   `json:"control_percent"`
	ControlOK      bool      `json:"control_ok"`
	ControlActive  bool      `json:"control_active"`
	Cores          []float64 `json:"cores"`
	CoreTemps      []float64 `json:"core_temps"`
	PowerTemp      float64   `json:"power_temp"`
	GPUTemp        float64   `json:"gpu_temp"`
	PerfTemp       float64   `json:"perf_temp"`
}

type hostMeterState struct {
	mu     sync.Mutex
	sample hostMeterSample
}

func readFanControl() (fan.Status, bool) {
	path, err := fan.StatusPath()
	if err != nil {
		return fan.Status{}, false
	}
	return fan.ReadStatus(path)
}

func (h *desktopHost) recordHostMeters(cpu, memory, disk, temp, fan, fanMin, fanMax float64, cpuOK, memoryOK, diskOK, tempOK, fanOK, fanVisible bool, cores, coreTemps []float64, powerTemp, gpuTemp, perfTemp float64, download int64, downloadOK bool) {
	control, controlOK := readFanControl()
	h.meters.mu.Lock()
	defer h.meters.mu.Unlock()
	sample := &h.meters.sample
	sample.Download, sample.DownloadOK = download, downloadOK
	sample.CPU, sample.CPUOK = cpu, cpuOK
	sample.Memory, sample.MemoryOK = memory, memoryOK
	sample.Disk, sample.DiskOK = disk, diskOK
	sample.Temperature, sample.TempOK = temp, tempOK
	sample.FanRPM, sample.FanOK, sample.FanVisible = fan, fanOK, fanVisible
	if fanMax > fanMin && fanMin >= 0 {
		sample.FanMin, sample.FanMax = fanMin, fanMax
	} else {
		sample.FanMin, sample.FanMax = 0, 0
	}
	sample.ControlTemp, sample.ControlPercent, sample.ControlActive = control.Temp, control.Percent, control.Active
	sample.ControlOK = controlOK
	if cores == nil {
		sample.Cores = []float64{}
	} else {
		sample.Cores = append([]float64(nil), cores...)
	}
	if coreTemps == nil {
		sample.CoreTemps = []float64{}
	} else {
		sample.CoreTemps = append([]float64(nil), coreTemps...)
	}
	sample.PowerTemp, sample.GPUTemp, sample.PerfTemp = powerTemp, gpuTemp, perfTemp
}

func (h *desktopHost) hostMeters() hostMeterSample {
	h.meters.mu.Lock()
	defer h.meters.mu.Unlock()
	sample := h.meters.sample
	if sample.Cores == nil {
		sample.Cores = []float64{}
	} else {
		sample.Cores = append([]float64(nil), sample.Cores...)
	}
	if sample.CoreTemps == nil {
		sample.CoreTemps = []float64{}
	} else {
		sample.CoreTemps = append([]float64(nil), sample.CoreTemps...)
	}
	return sample
}

func (h *desktopHost) fanControl() fan.Config {
	path, err := fan.Path()
	if err != nil {
		return fan.Default()
	}
	cfg, err := fan.Load(path)
	if err != nil {
		return fan.Default()
	}
	return cfg
}

func (h *desktopHost) setFan(cfg fan.Config) (fan.Config, error) {
	path, err := fan.Path()
	if err != nil {
		return fan.Config{}, err
	}
	if err := fan.Save(path, cfg); err != nil {
		return fan.Config{}, err
	}
	return cfg, nil
}

func (h *desktopHost) showTray() {
	_ = h.tray.PositionWindow(h.popup, 8)
	h.popup.Show().Focus()
}

func (h *desktopHost) runMenuBarTraffic(ctx context.Context) {
	var previous uint64
	var sampledAt time.Time
	var prevCPUUsed, prevCPUTotal uint64
	var cpuPercent, memPercent, diskPercent float64
	var memOK, diskOK bool
	var prevCoreUsed, prevCoreTotal []uint64
	var corePercents []float64
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			h.mu.Lock()
			show := h.menuBarShow
			h.mu.Unlock()
			current := native.LinkInboundBytes()
			cpuUsed, cpuTotal, cpuOK := native.HostCPUTicks()
			coreUsed, coreTotal, coreTicksOK := native.CoreCPUTicks()
			if sampledAt.IsZero() {
				previous, sampledAt = current, now
				if cpuOK {
					prevCPUUsed, prevCPUTotal = cpuUsed, cpuTotal
				}
				if coreTicksOK {
					prevCoreUsed, prevCoreTotal = coreUsed, coreTotal
				}
				if next, ok := native.HostMemoryPercent(); ok {
					memPercent = next
				}
				if next, ok := native.HostDiskPercent(); ok {
					diskPercent = next
					diskOK = true
				}
				continue
			}
			elapsed := now.Sub(sampledAt).Seconds()
			var link int64
			if elapsed > 0 && current >= previous {
				link = int64(float64(current-previous) / elapsed)
			}
			previous, sampledAt = current, now
			var percentOK bool
			if cpuOK {
				if next, ok := trayactivity.CPUPercent(prevCPUUsed, prevCPUTotal, cpuUsed, cpuTotal); ok {
					cpuPercent = next
					percentOK = true
				}
				prevCPUUsed, prevCPUTotal = cpuUsed, cpuTotal
			}
			if coreTicksOK && len(prevCoreUsed) == len(coreUsed) {
				nextCores := make([]float64, len(coreUsed))
				ready := false
				for i := range coreUsed {
					if percent, ok := trayactivity.CPUPercent(prevCoreUsed[i], prevCoreTotal[i], coreUsed[i], coreTotal[i]); ok {
						nextCores[i] = percent
						ready = true
					}
				}
				if ready {
					corePercents = nextCores
				}
			}
			if coreTicksOK {
				prevCoreUsed, prevCoreTotal = coreUsed, coreTotal
			}
			if next, ok := native.HostMemoryPercent(); ok {
				memPercent = next
				memOK = true
			}
			if next, ok := native.HostDiskPercent(); ok {
				diskPercent = next
				diskOK = true
			}
			fanC, fanTempOK, fanRPM, fanOK, fanMin, fanMax := native.FanSample()
			var tempC float64
			var tempOK bool
			if fanTempOK {
				tempC, tempOK = fanC, true
			} else {
				tempC, tempOK = native.CPUTempC()
			}
			var cores []float64
			if len(corePercents) > 0 {
				cores = corePercents
			}
			fanVisible := h.fanControl().Visible
			show.Fan = fanVisible
			powerTemp, gpuTemp, perfTemp, _ := native.SensorGroupTemps()
			if powerTemp < 0 {
				powerTemp = 0
			}
			if gpuTemp < 0 {
				gpuTemp = 0
			}
			if perfTemp < 0 {
				perfTemp = 0
			}
			download := trayactivity.MacDownloadRate(link, h.activity.Snapshot())
			h.recordHostMeters(cpuPercent, memPercent, diskPercent, tempC, fanRPM, fanMin, fanMax, percentOK, memOK, diskOK, tempOK, fanOK, fanVisible, cores, native.CoreTemps(), powerTemp, gpuTemp, perfTemp, download, elapsed > 0)
			native.SetMenuBarTraffic(trayactivity.FormatMenuBarStats(download, cpuPercent, memPercent, diskPercent, tempC, tempOK, fanRPM, fanOK, show))
		}
	}
}

func (h *desktopHost) createTray() {
	h.popup = h.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "menu-bar", Title: "OpenSurge Menu Bar", URL: "/desktop-tray",
		Width: 390, Height: 550, Hidden: true, Frameless: true, DisableResize: true,
		AlwaysOnTop: true, HideOnEscape: true,
		Mac: application.MacWindow{Backdrop: application.MacBackdropTranslucent, CollectionBehavior: application.MacWindowCollectionBehaviorCanJoinAllSpaces | application.MacWindowCollectionBehaviorFullScreenAuxiliary, TabbingMode: application.MacWindowTabbingModeDisallowed},
	})
	h.tray = h.app.SystemTray.New().SetTemplateIcon(trayIcon).AttachWindow(h.popup).WindowOffset(8)
	h.tray.SetTooltip("OpenSurge")
	h.popup.OnWindowEvent(events.Common.WindowShow, func(*application.WindowEvent) { h.status.SetRapid(true) })
	h.popup.OnWindowEvent(events.Common.WindowHide, func(*application.WindowEvent) { h.status.SetRapid(false) })
	h.popup.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) { event.Cancel(); h.popup.Hide() })
	h.trayMore = h.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "menu-bar-more", Title: "OpenSurge More Actions", URL: "/desktop-tray-menu",
		Width: 310, Height: 400, Hidden: true, Frameless: true, DisableResize: true,
		AlwaysOnTop: true,
		Mac:         application.MacWindow{Backdrop: application.MacBackdropTranslucent, CollectionBehavior: application.MacWindowCollectionBehaviorCanJoinAllSpaces | application.MacWindowCollectionBehaviorFullScreenAuxiliary, TabbingMode: application.MacWindowTabbingModeDisallowed},
	})
	h.trayMore.RegisterKeyBinding("escape", func(application.Window) { native.CloseTrayMenu(h.popup, h.trayMore) })
	h.trayMore.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		event.Cancel()
		native.CloseTrayMenu(h.popup, h.trayMore)
	})
}

func (h *desktopHost) readMenuStatus(ctx context.Context) (*menustatus.Status, error) {
	var status menustatus.Status
	err := h.client.ReadJSON(ctx, "/api/v1/menubar", &status)
	return &status, err
}

func (h *desktopHost) menuStatusChanged(snapshot menustatus.Snapshot) {
	h.activity.SetRunning(snapshot.Status != nil && (snapshot.Status.Gateway == "running" || snapshot.Status.Gateway == "degraded"))
	if status := snapshot.Status; status != nil {
		language := status.UIPreferences.Language
		if language == "system" {
			language = native.SystemLanguage()
		}
		if language == "en" || language == "zh-Hans" {
			h.setLanguage(language)
		}
	}
	h.mu.Lock()
	english := h.language == "en"
	if status := snapshot.Status; status != nil {
		if status.UIPreferences.MenuBar == nil {
			h.menuBarShow = trayactivity.DefaultMenuBarShow()
		} else {
			h.menuBarShow = trayactivity.MenuBarShow{
				Download:    status.UIPreferences.MenuBar.Download,
				CPU:         status.UIPreferences.MenuBar.CPU,
				Memory:      status.UIPreferences.MenuBar.Memory,
				Disk:        status.UIPreferences.MenuBar.Disk,
				Temperature: status.UIPreferences.MenuBar.Temperature,
				Fan:         true,
			}
		}
	}
	h.mu.Unlock()
	descriptions := map[string][2]string{
		"connecting": {"正在连接后台服务", "Connecting to service"},
		"stopped":    {"网关已停止", "Gateway stopped"}, "running": {"网关正在运行", "Gateway running"},
		"degraded": {"网关运行异常", "Gateway needs attention"}, "recovery": {"网络恢复尚未完成", "Network recovery incomplete"},
		"unreachable":  {"无法连接后台服务", "Service unreachable"},
		"starting":     {"正在启动网关", "Starting gateway"},
		"reloading":    {"正在应用配置", "Applying configuration"},
		"stopping":     {"正在停止网关", "Stopping gateway"},
		"recovering":   {"正在恢复代理引擎", "Recovering proxy engine"},
		"rolling_back": {"正在回滚网络改动", "Rolling back network changes"},
		"changing":     {"正在更新网关", "Updating gateway"},
		"interrupted":  {"重启后待清理", "Cleanup required after reboot"},
		"unknown":      {"状态暂不可用", "Status temporarily unavailable"},
	}
	i := 0
	if english {
		i = 1
	}
	native.SetMenuBarIndicator(snapshot.Indicator, "OpenSurge — "+descriptions[snapshot.Indicator][i])
}
