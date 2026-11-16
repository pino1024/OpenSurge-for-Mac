// Package native contains the small AppKit/WKWebView adaptations not exposed by
// the pinned Wails host API. All gateway actions remain in the Control Service.
package native

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework WebKit -framework IOKit
#include <stdlib.h>
#include <stdbool.h>
void configureOpenSurgeWindow(void *window, bool rememberFrame, bool tray);
void setOpenSurgeWindowLanguage(void *window, bool english);
bool openSurgeExternalURLAllowed(const char *url);
bool openSurgeSystemUsesEnglish(void);
void setOpenSurgeWindowAppearance(void *window, int preference);
void configureOpenSurgeMenuBarIcon(void);
void setOpenSurgeMenuBarIndicator(const char *indicator, const char *description);
void setOpenSurgeMenuBarTraffic(const char *title);
unsigned long long openSurgeLinkIbytes(void);
int openSurgeHostCPU(unsigned long long *used, unsigned long long *total);
int openSurgeHostMemory(double *percent);
int openSurgeCPUTempC(double *celsius);
void setOpenSurgeDockVisible(bool visible);
void refreshOpenSurgeDockVisibility(void);
void presentOpenSurgeWindow(void *window);
void configureOpenSurgeTrayMenu(void *parent, void *menu);
void toggleOpenSurgeTrayMenu(void *parent, void *menu);
void closeOpenSurgeTrayMenu(void *parent, void *menu);
*/
import "C"

import (
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func Configure(window *application.WebviewWindow, rememberFrame bool) {
	application.InvokeSync(func() {
		C.configureOpenSurgeWindow(window.NativeWindow(), C.bool(rememberFrame), C.bool(!rememberFrame))
	})
}

func ConfigureSettings(window *application.WebviewWindow) {
	application.InvokeSync(func() { C.configureOpenSurgeWindow(window.NativeWindow(), false, false) })
}

func ConfigureTrayMenu(parent, menu *application.WebviewWindow) {
	application.InvokeSync(func() { C.configureOpenSurgeTrayMenu(parent.NativeWindow(), menu.NativeWindow()) })
}

func ToggleTrayMenu(parent, menu *application.WebviewWindow) {
	application.InvokeSync(func() { C.toggleOpenSurgeTrayMenu(parent.NativeWindow(), menu.NativeWindow()) })
}

func CloseTrayMenu(parent, menu *application.WebviewWindow) {
	application.InvokeSync(func() { C.closeOpenSurgeTrayMenu(parent.NativeWindow(), menu.NativeWindow()) })
}

func RefreshDockVisibility() {
	application.InvokeSync(func() { C.refreshOpenSurgeDockVisibility() })
}

func SetLanguage(window *application.WebviewWindow, english bool) {
	application.InvokeSync(func() { C.setOpenSurgeWindowLanguage(window.NativeWindow(), C.bool(english)) })
}

func ExternalURLAllowed(value string) bool {
	url := C.CString(value)
	defer C.free(unsafe.Pointer(url))
	return bool(C.openSurgeExternalURLAllowed(url))
}

func SystemLanguage() string {
	if bool(C.openSurgeSystemUsesEnglish()) {
		return "en"
	}
	return "zh-Hans"
}

// Apply the popup/settings preference, including clearing overrides for System.
func SetAppearance(window *application.WebviewWindow, theme string) {
	preference := C.int(0)
	if theme == "light" {
		preference = 1
	} else if theme == "dark" {
		preference = 2
	}
	application.InvokeSync(func() { C.setOpenSurgeWindowAppearance(window.NativeWindow(), preference) })
}

func ConfigureMenuBarIcon() {
	C.configureOpenSurgeMenuBarIcon()
}

func SetMenuBarIndicator(indicator, description string) {
	value := C.CString(indicator)
	defer C.free(unsafe.Pointer(value))
	label := C.CString(description)
	defer C.free(unsafe.Pointer(label))
	application.InvokeSync(func() { C.setOpenSurgeMenuBarIndicator(value, label) })
}

func SetMenuBarTraffic(title string) {
	value := C.CString(title)
	defer C.free(unsafe.Pointer(value))
	application.InvokeSync(func() { C.setOpenSurgeMenuBarTraffic(value) })
}

// LinkInboundBytes is inbound octets on this Mac's physical NICs. Loopback
// and point-to-point interfaces such as utun are omitted so TUN delivery is
// not double-counted.
func LinkInboundBytes() uint64 {
	return uint64(C.openSurgeLinkIbytes())
}

// HostCPUTicks is this Mac's cumulative CPU ticks (user+system+nice vs all
// states). Consecutive samples yield host-wide occupancy.
func HostCPUTicks() (used, total uint64, ok bool) {
	var usedTicks, totalTicks C.ulonglong
	if C.openSurgeHostCPU(&usedTicks, &totalTicks) == 0 {
		return 0, 0, false
	}
	return uint64(usedTicks), uint64(totalTicks), true
}

// HostMemoryPercent is memory occupancy from the kernel free level used by
// memory_pressure: 100 minus that free percentage.
func HostMemoryPercent() (float64, bool) {
	var percent C.double
	if C.openSurgeHostMemory(&percent) == 0 {
		return 0, false
	}
	return float64(percent), true
}

// CPUTempC is the hottest CPU-core HID temperature in Celsius.
func CPUTempC() (float64, bool) {
	var celsius C.double
	if C.openSurgeCPUTempC(&celsius) == 0 {
		return 0, false
	}
	return float64(celsius), true
}

func SetDockVisible(visible bool) {
	application.InvokeSync(func() { C.setOpenSurgeDockVisible(C.bool(visible)) })
}

// Present is only for explicit launch, reopen and user-triggered dialogs. It does
// not make the main window permanently floating or reactivate background polls.
func Present(window *application.WebviewWindow) {
	application.InvokeSync(func() { C.presentOpenSurgeWindow(window.NativeWindow()) })
}
