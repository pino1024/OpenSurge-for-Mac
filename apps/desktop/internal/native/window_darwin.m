#import <Cocoa/Cocoa.h>
#import <CoreFoundation/CoreFoundation.h>
#import <DiskArbitration/DiskArbitration.h>
#import <WebKit/WebKit.h>
#import <objc/runtime.h>
#include <IOKit/IOKitLib.h>
#include <ifaddrs.h>
#include <mach/mach.h>
#include <mach/mach_host.h>
#include <mach/processor_info.h>
#include <math.h>
#include <net/if.h>
#include <net/if_dl.h>
#include <stdint.h>
#include <string.h>
#include <sys/mount.h>
#include <sys/socket.h>
#include <sys/sysctl.h>
#include <unistd.h>

static const char guardKey;

// Intel notebooks identify themselves in hw.model. Apple silicon uses MacNN,N
// for every product, and only machines with a lid publish the clamshell key.
bool openSurgeNotebook(void) {
    char model[128] = {0};
    size_t len = sizeof model;
    if (sysctlbyname("hw.model", model, &len, NULL, 0) == 0 && strncmp(model, "MacBook", 7) == 0) return true;
    io_registry_entry_t root = IOServiceGetMatchingService(kIOMainPortDefault, IOServiceMatching("IOPMrootDomain"));
    if (!root) return false;
    CFTypeRef lid = IORegistryEntryCreateCFProperty(root, CFSTR("AppleClamshellState"), kCFAllocatorDefault, 0);
    IOObjectRelease(root);
    if (!lid) return false;
    CFRelease(lid);
    return true;
}
static BOOL presentingMainWindow;
static BOOL smokeActions(void) { return [NSProcessInfo.processInfo.arguments containsObject:@"--smoke-actions"]; }

void setOpenSurgeDockVisible(bool visible) {
    NSApplicationActivationPolicy policy = visible ? NSApplicationActivationPolicyRegular : NSApplicationActivationPolicyAccessory;
    if (NSApp.activationPolicy == policy) return;
    [NSApp setActivationPolicy:policy];
    if (smokeActions()) NSLog(@"OpenSurge Dock: %@", visible ? @"visible" : @"hidden");
}

static void activateOpenSurge(void) {
    if (@available(macOS 14.0, *)) [NSApp activate];
    else [NSApp activateIgnoringOtherApps:YES];
}

void presentOpenSurgeWindow(void *pointer) {
    NSWindow *window = (__bridge NSWindow *)pointer;
    // Policy changes themselves produce visibility notifications. Do not let an
    // intermediate hidden/occluded state undo the explicit foreground request.
    presentingMainWindow = YES;
    setOpenSurgeDockVisible(true);
    [NSApp unhideWithoutActivation];
    if (window.isMiniaturized) [window deminiaturize:nil];
    activateOpenSurge();
    [window makeKeyAndOrderFront:nil];
    [window orderFrontRegardless];
    presentingMainWindow = NO;
    // AppKit finishes accessory -> regular activation on the next event-loop
    // turn. Order once more then, without timers or a permanent floating level.
    dispatch_async(dispatch_get_main_queue(), ^{
        if (!window.isVisible || window.isMiniaturized || NSApp.isHidden) return;
        activateOpenSurge();
        [window makeKeyAndOrderFront:nil];
        [window orderFrontRegardless];
        if (smokeActions()) NSLog(@"OpenSurge foreground: active=%d key=%d visible=%d", NSApp.isActive, window.isKeyWindow, window.isVisible);
    });
}

// Draw before WebKit paints its first document. The same light palette is used
// by the HTML bootstrap; neither depends on loading JavaScript or API data.
@interface OpenSurgeStartupView : NSView
@end
@implementation OpenSurgeStartupView
- (BOOL)isOpaque { return YES; }
- (void)drawRect:(NSRect)dirtyRect {
    NSColor *white = [NSColor colorWithSRGBRed:0.98 green:0.99 blue:0.98 alpha:1];
    NSColor *mint = [NSColor colorWithSRGBRed:0.86 green:0.94 blue:0.90 alpha:1];
    [[[NSGradient alloc] initWithStartingColor:white endingColor:mint] drawInRect:self.bounds angle:35];
    NSRect icon = NSMakeRect(NSMidX(self.bounds) - 44, NSMidY(self.bounds) - 44, 88, 88);
    [NSApp.applicationIconImage drawInRect:icon fromRect:NSZeroRect operation:NSCompositingOperationSourceOver fraction:1];
}
@end

bool openSurgeSystemUsesEnglish(void) { return ![NSLocale.preferredLanguages.firstObject hasPrefix:@"zh"]; }

static BOOL isInternal(NSURL *url) {
    return [url.scheme.lowercaseString isEqualToString:@"wails"] &&
        [url.host.lowercaseString isEqualToString:@"localhost"] &&
        !url.user && !url.password && !url.port;
}

static BOOL isExternal(NSURL *url) {
    NSString *scheme = url.scheme.lowercaseString;
    return ([scheme isEqualToString:@"https"] || [scheme isEqualToString:@"http"]) &&
        url.host.length > 0 && !url.user && !url.password;
}

bool openSurgeExternalURLAllowed(const char *value) {
    return isExternal([NSURL URLWithString:[NSString stringWithUTF8String:value]]);
}

static WKWebView *findWebView(NSView *view) {
    if ([view isKindOfClass:WKWebView.class]) return (WKWebView *)view;
    for (NSView *child in view.subviews) {
        WKWebView *found = findWebView(child);
        if (found) return found;
    }
    return nil;
}

static void hideTrayMenu(NSWindow *parent, NSWindow *menu) {
    if (!menu) return;
    [parent removeChildWindow:menu];
    [menu orderOut:nil];
}

static NSRect trayMenuFrame(NSWindow *parent, NSRect frame) {
    NSRect available = (parent.screen ?: NSScreen.mainScreen).visibleFrame;
    frame.size.height = MIN(NSHeight(frame), NSHeight(available) - 16);
    frame.origin.x = MAX(NSMinX(available) + 8, MIN(NSMaxX(parent.frame) - 16 - NSWidth(frame), NSMaxX(available) - NSWidth(frame) - 8));
    frame.origin.y = MAX(NSMinY(available) + 8, MIN(NSMaxY(parent.frame) - 48, NSMaxY(available) - 8) - NSHeight(frame));
    return frame;
}

// Forward Wails' load, renderer-recovery and file-picker delegates. Keeping the
// original delegates preserves the framework's lifecycle without a Wails fork.
@interface OpenSurgeWebViewGuard : NSObject <WKNavigationDelegate, WKUIDelegate, WKScriptMessageHandler>
@property (weak) id<WKNavigationDelegate> navigation;
@property (weak) id<WKUIDelegate> ui;
@property (weak) WKWebView *webView;
@property BOOL english;
@property BOOL tray;
@property (weak) NSWindow *trayParent;
@property (weak) NSWindow *trayMenu;
@property (strong) id trayClickMonitor;
@property (strong) OpenSurgeStartupView *startup;
- (void)publishAppearance:(NSNotification *)notification;
- (void)publishTraySizing;
- (void)dismissTrayAfterFocusChange:(NSNotification *)notification;
- (void)showStartup;
- (void)finishStartup;
@end

void refreshOpenSurgeDockVisibility(void) {
    if (presentingMainWindow) return;
    BOOL visible = NO;
    for (NSWindow *window in NSApp.windows) {
        OpenSurgeWebViewGuard *guard = objc_getAssociatedObject(findWebView(window.contentView), &guardKey);
        if (guard && !guard.tray && (window.isVisible || window.isMiniaturized)) { visible = YES; break; }
    }
    setOpenSurgeDockVisible(visible && !NSApp.isHidden);
}

@implementation OpenSurgeWebViewGuard
- (void)showStartup {
    if (self.tray || self.startup) return;
    NSView *content = self.webView.window.contentView;
    self.startup = [[OpenSurgeStartupView alloc] initWithFrame:content.bounds];
    self.startup.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
    [content addSubview:self.startup positioned:NSWindowAbove relativeTo:nil];
    if (smokeActions()) NSLog(@"OpenSurge startup: native placeholder visible");
}
- (void)finishStartup {
    if (!self.startup) return;
    [self.startup removeFromSuperview];
    self.startup = nil;
    if (smokeActions()) NSLog(@"OpenSurge startup: first React frame ready");
}
- (BOOL)respondsToSelector:(SEL)selector {
    return [super respondsToSelector:selector] || [self.navigation respondsToSelector:selector] || [self.ui respondsToSelector:selector];
}
- (id)forwardingTargetForSelector:(SEL)selector {
    if ([self.navigation respondsToSelector:selector]) return self.navigation;
    if ([self.ui respondsToSelector:selector]) return self.ui;
    return [super forwardingTargetForSelector:selector];
}
- (void)webView:(WKWebView *)webView decidePolicyForNavigationAction:(WKNavigationAction *)action decisionHandler:(void (^)(WKNavigationActionPolicy))decision {
    NSURL *url = action.request.URL;
    BOOL localPage = isInternal(url) && ![url.path hasPrefix:@"/api/"] && ![url.path hasPrefix:@"/desktop/"];
    if (localPage && action.targetFrame.isMainFrame) {
        decision(WKNavigationActionPolicyAllow);
        return;
    }
    // Only a deliberate link activation can open the system browser. Redirects,
    // script navigation, subframes and file/custom schemes never leave the host.
    if (action.navigationType == WKNavigationTypeLinkActivated && action.sourceFrame.isMainFrame && isInternal(action.sourceFrame.request.URL) && isExternal(url)) {
        [NSWorkspace.sharedWorkspace openURL:url];
    }
    decision(WKNavigationActionPolicyCancel);
}
- (WKWebView *)webView:(WKWebView *)webView createWebViewWithConfiguration:(WKWebViewConfiguration *)configuration forNavigationAction:(WKNavigationAction *)action windowFeatures:(WKWindowFeatures *)features {
    return nil;
}
- (void)publishAppearance:(NSNotification *)notification {
    if (!self.tray) return;
    WKWebView *webView = self.webView;
    [webView.effectiveAppearance performAsCurrentDrawingAppearance:^{
        NSDictionary<NSString *, NSColor *> *colours = @{
            @"--tray-label": NSColor.labelColor, @"--tray-secondary": NSColor.secondaryLabelColor,
            @"--tray-separator": NSColor.separatorColor, @"--tray-control": NSColor.controlColor,
            @"--tray-control-text": NSColor.controlTextColor, @"--tray-disabled": NSColor.disabledControlTextColor,
            @"--tray-accent": NSColor.controlAccentColor, @"--tray-accent-text": NSColor.alternateSelectedControlTextColor,
            @"--tray-download": NSColor.systemBlueColor, @"--tray-upload": NSColor.systemOrangeColor,
            @"--tray-solid": NSColor.windowBackgroundColor
        };
        NSMutableDictionary *values = [NSMutableDictionary dictionary];
        for (NSString *key in colours) {
            NSColor *colour = [colours[key] colorUsingColorSpace:NSColorSpace.sRGBColorSpace];
            if (colour) values[key] = [NSString stringWithFormat:@"rgba(%.0f,%.0f,%.0f,%.3f)", colour.redComponent * 255, colour.greenComponent * 255, colour.blueComponent * 255, colour.alphaComponent];
        }
        NSData *data = [NSJSONSerialization dataWithJSONObject:values options:0 error:nil];
        NSString *json = [[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding];
        NSString *script = [NSString stringWithFormat:@"Object.entries(%@).forEach(([key,value])=>document.documentElement.style.setProperty(key,value));", json];
        [webView evaluateJavaScript:script completionHandler:nil];
    }];
}
- (void)publishVisibility:(NSNotification *)notification {
    NSWindow *window = self.webView.window;
    if (smokeActions()) NSLog(@"OpenSurge window visibility: title=%@ visible=%d minimised=%d",
        self.trayParent ? @"menu-bar-more" : self.tray ? @"menu-bar" : window.title, window.isVisible, window.isMiniaturized);
    // Occlusion pauses polling but must not remove a covered window from Dock.
    // A minimised window keeps its Dock entry so the normal restore path works.
    if (!self.tray) refreshOpenSurgeDockVisibility();
    BOOL visible = window.isVisible && !window.isMiniaturized && (window.occlusionState & NSWindowOcclusionStateVisible);
    NSString *script = [NSString stringWithFormat:@"window.__opensurgeWindowVisible=%@;document.dispatchEvent(new Event('visibilitychange'));", visible ? @"true" : @"false"];
    [self.webView evaluateJavaScript:script completionHandler:nil];
    if (self.trayParent) {
        NSString *menuState = [NSString stringWithFormat:@"window.dispatchEvent(new CustomEvent('opensurge:tray-menu',{detail:%@}));", window.isVisible ? @"true" : @"false"];
        [findWebView(self.trayParent.contentView) evaluateJavaScript:menuState completionHandler:nil];
    } else if (self.tray && !window.isVisible) {
        hideTrayMenu(window, self.trayMenu);
    }
    if (visible) [self publishTraySizing];
}
- (void)dismissTrayAfterFocusChange:(NSNotification *)notification {
    // Resigning key happens before the next window becomes key. Resolve the
    // pair after AppKit finishes that transfer, including app deactivation.
    dispatch_async(dispatch_get_main_queue(), ^{
        NSWindow *parent = self.trayParent ?: self.webView.window;
        NSWindow *menu = self.trayParent ? self.webView.window : self.trayMenu;
        if (NSApp.isActive && (NSApp.keyWindow == parent || NSApp.keyWindow == menu)) return;
        hideTrayMenu(parent, menu);
        [parent orderOut:nil];
    });
}
- (void)publishTraySizing {
    if (!self.tray) return;
    NSWindow *window = self.webView.window;
    CGFloat available = MAX(240, NSHeight((window.screen ?: NSScreen.mainScreen).visibleFrame) - 16);
    NSString *script = [NSString stringWithFormat:
        @"document.documentElement.style.setProperty('--tray-available-height','%.0fpx');"
         "if(!window.__opensurgeTraySizing){window.__opensurgeTraySizing=true;"
         "const attach=()=>{const root=document.querySelector('.tray-app');if(!root)return false;"
         "let previous=0;new ResizeObserver(()=>{const height=Math.ceil(root.getBoundingClientRect().height);"
         "if(height!==previous){previous=height;window.webkit.messageHandlers.opensurgeTraySize.postMessage(height);}}).observe(root);return true;};"
         "if(!attach()){const pending=new MutationObserver(()=>{if(attach())pending.disconnect();});pending.observe(document.body,{childList:true,subtree:true});}}", available];
    [self.webView evaluateJavaScript:script completionHandler:nil];
}
- (void)userContentController:(WKUserContentController *)controller didReceiveScriptMessage:(WKScriptMessage *)message {
    if (!message.frameInfo.isMainFrame || !isInternal(message.frameInfo.request.URL)) return;
    if (!self.tray && [message.name isEqualToString:@"opensurgeUIReady"] && [message.body isEqual:@"ready"]) {
        [self finishStartup];
        return;
    }
    if (!self.tray || ![message.name isEqualToString:@"opensurgeTraySize"] || !message.frameInfo.isMainFrame ||
        !isInternal(message.frameInfo.request.URL) || ![message.body isKindOfClass:NSNumber.class]) return;
    CGFloat height = [message.body doubleValue];
    if (!isfinite(height) || height < 100 || height > 2000) return;
    NSWindow *window = self.webView.window;
    NSRect available = (window.screen ?: NSScreen.mainScreen).visibleFrame;
    NSRect frame = window.frame;
    CGFloat top = MIN(NSMaxY(frame), NSMaxY(available) - 8);
    frame.size.height = MIN(ceil(height), NSHeight(available) - 16);
    frame.origin.y = MAX(NSMinY(available) + 8, top - frame.size.height);
    if (self.trayParent) frame = trayMenuFrame(self.trayParent, frame);
    if (NSEqualRects(frame, window.frame)) return;
    // The disclosure animates its intrinsic height in CSS. Follow each measured
    // frame directly, preserving the menu-bar edge, instead of starting competing
    // native animations or scrolling the whole panel inside a fixed window.
    [window setFrame:frame display:YES];
    if (self.trayMenu.isVisible) [self.trayMenu setFrame:trayMenuFrame(window, self.trayMenu.frame) display:YES];
    if (smokeActions()) NSLog(@"OpenSurge tray geometry: content=%.0f window=%.0f top=%.0f panel=%@", height, NSHeight(window.frame), NSMaxY(window.frame), self.trayParent ? @"more" : @"overview");
}
- (void)webView:(WKWebView *)webView didFinishNavigation:(WKNavigation *)navigation {
    if ([self.navigation respondsToSelector:_cmd]) [self.navigation webView:webView didFinishNavigation:navigation];
    [self publishVisibility:nil];
    [self publishAppearance:nil];
    [self publishTraySizing];
    if (!self.tray) [webView evaluateJavaScript:@"document.documentElement.dataset.uiReady === 'true'" completionHandler:^(id ready, NSError *error) {
        if ([ready isEqual:@YES]) [self finishStartup];
    }];
}
- (void)webViewWebContentProcessDidTerminate:(WKWebView *)webView {
    [self showStartup];
    if ([self.navigation respondsToSelector:_cmd]) [self.navigation webViewWebContentProcessDidTerminate:webView];
}
- (NSAlert *)alert:(NSString *)message {
    NSAlert *alert = [NSAlert new];
    alert.messageText = @"OpenSurge";
    alert.informativeText = message;
    return alert;
}
- (void)webView:(WKWebView *)webView runJavaScriptAlertPanelWithMessage:(NSString *)message initiatedByFrame:(WKFrameInfo *)frame completionHandler:(void (^)(void))completion {
    if (!frame.isMainFrame || !isInternal(frame.request.URL)) { completion(); return; }
    NSAlert *alert = [self alert:message];
    [alert addButtonWithTitle:self.english ? @"OK" : @"好"];
    [alert beginSheetModalForWindow:webView.window completionHandler:^(NSModalResponse result) { completion(); }];
}
- (void)webView:(WKWebView *)webView runJavaScriptConfirmPanelWithMessage:(NSString *)message initiatedByFrame:(WKFrameInfo *)frame completionHandler:(void (^)(BOOL))completion {
    if (!frame.isMainFrame || !isInternal(frame.request.URL)) { completion(NO); return; }
    NSAlert *alert = [self alert:message];
    [alert addButtonWithTitle:self.english ? @"Continue" : @"继续"];
    [alert addButtonWithTitle:self.english ? @"Cancel" : @"取消"];
    [alert beginSheetModalForWindow:webView.window completionHandler:^(NSModalResponse result) { completion(result == NSAlertFirstButtonReturn); }];
}
- (void)dealloc {
    [NSNotificationCenter.defaultCenter removeObserver:self];
    if (self.trayClickMonitor) [NSEvent removeMonitor:self.trayClickMonitor];
}
@end

void configureOpenSurgeWindow(void *pointer, bool rememberFrame, bool tray) {
    NSWindow *window = (__bridge NSWindow *)pointer;
    WKWebView *webView = findWebView(window.contentView);
    if (!webView || objc_getAssociatedObject(webView, &guardKey)) return;
    OpenSurgeWebViewGuard *guard = [OpenSurgeWebViewGuard new];
    guard.webView = webView;
    guard.tray = tray;
    if (guard.tray) {
        // Wails owns the effect view and transparent WKWebView. Use the AppKit
        // popover material rather than a fixed web colour or Liquid Glass.
        window.opaque = NO;
        window.backgroundColor = NSColor.clearColor;
        [webView.configuration.userContentController addScriptMessageHandler:guard name:@"opensurgeTraySize"];
        for (NSView *child in window.contentView.subviews) {
            if ([child isKindOfClass:NSVisualEffectView.class]) {
                NSVisualEffectView *effect = (NSVisualEffectView *)child;
                effect.material = NSVisualEffectMaterialPopover;
                effect.blendingMode = NSVisualEffectBlendingModeBehindWindow;
            }
        }
        [NSNotificationCenter.defaultCenter addObserver:guard selector:@selector(publishAppearance:) name:NSSystemColorsDidChangeNotification object:nil];
    } else {
        webView.underPageBackgroundColor = [NSColor colorWithSRGBRed:242.0/255 green:247.0/255 blue:244.0/255 alpha:1];
        [webView.configuration.userContentController addScriptMessageHandler:guard name:@"opensurgeUIReady"];
        [guard showStartup];
        for (NSNotificationName name in @[NSApplicationDidHideNotification, NSApplicationDidUnhideNotification]) {
            [NSNotificationCenter.defaultCenter addObserver:guard selector:@selector(publishVisibility:) name:name object:NSApp];
        }
    }
    guard.navigation = webView.navigationDelegate;
    guard.ui = webView.UIDelegate;
    guard.english = ![NSLocale.preferredLanguages.firstObject hasPrefix:@"zh"];
    objc_setAssociatedObject(webView, &guardKey, guard, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
    webView.navigationDelegate = guard;
    webView.UIDelegate = guard;
    for (NSNotificationName name in @[NSWindowDidChangeOcclusionStateNotification, NSWindowDidMiniaturizeNotification, NSWindowDidDeminiaturizeNotification]) {
        [NSNotificationCenter.defaultCenter addObserver:guard selector:@selector(publishVisibility:) name:name object:window];
    }
    if (rememberFrame) {
        [window setFrameUsingName:@"OpenSurgeMainWindow"];
        // The first launch is deliberately spacious. Fit smaller displays and
        // frames restored from a larger monitor without overriding user sizing.
        NSRect frame = window.frame;
        NSRect available = (window.screen ?: NSScreen.mainScreen).visibleFrame;
        frame.size.width = MIN(frame.size.width, available.size.width);
        frame.size.height = MIN(frame.size.height, available.size.height);
        [window setFrame:[window constrainFrameRect:frame toScreen:window.screen] display:NO];
        [window setFrameAutosaveName:@"OpenSurgeMainWindow"];
    }
    [guard publishVisibility:nil];
    if (!guard.tray) [webView evaluateJavaScript:@"document.documentElement.dataset.uiReady === 'true'" completionHandler:^(id ready, NSError *error) {
        if ([ready isEqual:@YES]) [guard finishStartup];
    }];
}

void configureOpenSurgeTrayMenu(void *parentPointer, void *menuPointer) {
    NSWindow *parent = (__bridge NSWindow *)parentPointer;
    NSWindow *menu = (__bridge NSWindow *)menuPointer;
    configureOpenSurgeWindow(menuPointer, false, true);
    OpenSurgeWebViewGuard *parentGuard = objc_getAssociatedObject(findWebView(parent.contentView), &guardKey);
    OpenSurgeWebViewGuard *menuGuard = objc_getAssociatedObject(findWebView(menu.contentView), &guardKey);
    parentGuard.trayMenu = menu;
    menuGuard.trayParent = parent;
    [NSNotificationCenter.defaultCenter addObserver:parentGuard selector:@selector(dismissTrayAfterFocusChange:) name:NSWindowDidResignKeyNotification object:parent];
    [NSNotificationCenter.defaultCenter addObserver:menuGuard selector:@selector(dismissTrayAfterFocusChange:) name:NSWindowDidResignKeyNotification object:menu];
    [NSNotificationCenter.defaultCenter addObserver:parentGuard selector:@selector(dismissTrayAfterFocusChange:) name:NSApplicationDidResignActiveNotification object:NSApp];
    __weak NSWindow *weakParent = parent;
    __weak NSWindow *weakMenu = menu;
    parentGuard.trayClickMonitor = [NSEvent addLocalMonitorForEventsMatchingMask:NSEventMaskLeftMouseDown | NSEventMaskRightMouseDown | NSEventMaskOtherMouseDown handler:^NSEvent *(NSEvent *event) {
        NSWindow *overview = weakParent, *actions = weakMenu;
        if (!overview.isVisible || event.window == actions) return event;
        if (event.window == overview) {
            // Let the trigger toggle the menu itself. All other overview clicks
            // dismiss the child while remaining available to the clicked control.
            NSPoint point = [overview.contentView convertPoint:event.locationInWindow fromView:nil];
            NSRect trigger = NSMakeRect(NSWidth(overview.contentView.bounds) - 42, NSHeight(overview.contentView.bounds) - 41, 26, 25);
            if (!NSPointInRect(point, trigger)) hideTrayMenu(overview, actions);
        } else {
            hideTrayMenu(overview, actions);
            [overview orderOut:nil];
        }
        return event;
    }];
}

void closeOpenSurgeTrayMenu(void *parentPointer, void *menuPointer) {
    NSWindow *parent = (__bridge NSWindow *)parentPointer;
    NSWindow *menu = (__bridge NSWindow *)menuPointer;
    hideTrayMenu(parent, menu);
    if (parent.isVisible) {
        [parent makeKeyWindow];
        [findWebView(parent.contentView) evaluateJavaScript:@"document.querySelector('.tray-more-trigger')?.focus();" completionHandler:nil];
    }
}

void toggleOpenSurgeTrayMenu(void *parentPointer, void *menuPointer) {
    NSWindow *parent = (__bridge NSWindow *)parentPointer;
    NSWindow *menu = (__bridge NSWindow *)menuPointer;
    if (!parent.isVisible) return;
    if (menu.isVisible) { closeOpenSurgeTrayMenu(parentPointer, menuPointer); return; }
    [menu setFrame:trayMenuFrame(parent, menu.frame) display:NO];
    [parent addChildWindow:menu ordered:NSWindowAbove];
    [menu makeKeyAndOrderFront:nil];
    if (smokeActions()) NSLog(@"OpenSurge tray menu opened: overview=%.0f top=%.0f menu=%.0f", NSHeight(parent.frame), NSMaxY(parent.frame), NSHeight(menu.frame));
}

void setOpenSurgeWindowLanguage(void *pointer, bool english) {
    NSWindow *window = (__bridge NSWindow *)pointer;
    WKWebView *webView = findWebView(window.contentView);
    OpenSurgeWebViewGuard *guard = objc_getAssociatedObject(webView, &guardKey);
    guard.english = english;
}

void setOpenSurgeWindowAppearance(void *pointer, int preference) {
    NSWindow *window = (__bridge NSWindow *)pointer;
    // System must clear the override, so WebKit's matchMedia can observe macOS
    // again after an explicit light/dark choice.
    window.appearance = preference == 0 ? nil : [NSAppearance appearanceNamed:preference == 2 ? NSAppearanceNameDarkAqua : NSAppearanceNameAqua];
    WKWebView *webView = findWebView(window.contentView);
    OpenSurgeWebViewGuard *guard = objc_getAssociatedObject(webView, &guardKey);
    [guard publishAppearance:nil];
}

static __weak NSStatusBarButton *menuBarButton;
static NSImage *menuBarBrandImage;
static NSString *menuBarIndicator = @"connecting";
static NSString *menuBarDescription = @"OpenSurge";
static NSString *menuBarTraffic = @"";

static void applyMenuBarButtonLayout(NSStatusBarButton *button) {
    BOOL hasTitle = menuBarTraffic.length > 0;
    button.imagePosition = hasTitle ? NSImageLeft : NSImageOnly;
    // Full monospace so B/K/M/G/%/C are the same width as digits. Tabular-digit
    // SF Pro still draws K narrower than B, which jumps the status item.
    NSFont *font = [NSFont monospacedSystemFontOfSize:11 weight:NSFontWeightMedium];
    button.font = font;
    if (!hasTitle) {
        button.title = @"";
        return;
    }
    button.attributedTitle = [[NSAttributedString alloc] initWithString:menuBarTraffic attributes:@{NSFontAttributeName: font}];
}

void setOpenSurgeMenuBarTraffic(const char *title) {
    NSString *next = title ? [NSString stringWithUTF8String:title] : @"";
    if ([menuBarTraffic isEqualToString:next] && menuBarButton) return;
    menuBarTraffic = next;
    if (!menuBarButton) return;
    applyMenuBarButtonLayout(menuBarButton);
}

void setOpenSurgeMenuBarIndicator(const char *indicator, const char *description) {
    NSString *next = [NSString stringWithUTF8String:indicator];
    NSString *label = [NSString stringWithUTF8String:description];
    if ([menuBarIndicator isEqualToString:next] && [menuBarDescription isEqualToString:label]) return;
    menuBarIndicator = next;
    menuBarDescription = label;
    // Reapply the cached brand source through the same adaptation. Never use a
    // previously rendered warning symbol as the next state's brand source.
    if (menuBarButton && menuBarBrandImage) menuBarButton.image = menuBarBrandImage;
}

// Wails beta.26 scales every assigned image to NSStatusBar.thickness and does
// not expose its NSStatusItem. Status-item windows aren't in NSApp.windows.
// Intercept only NSStatusBarButton's public setter, and only for buttons owned
// by Wails' StatusItemController in this process. Install before tray creation.
// Keeping this narrow adapter here avoids a Wails fork or an inherited NSButton
// method replacement that would change ordinary controls.
void configureOpenSurgeMenuBarIcon(void) {
    static dispatch_once_t once;
    dispatch_once(&once, ^{
        Class buttonClass = NSStatusBarButton.class;
        SEL selector = @selector(setImage:);
        Method method = class_getInstanceMethod(buttonClass, selector);
        IMP original = method_getImplementation(method);
        IMP sized = imp_implementationWithBlock(^(NSStatusBarButton *button, NSImage *image) {
            Class controllerClass = NSClassFromString(@"StatusItemController");
            BOOL owned = controllerClass && [button.target isKindOfClass:controllerClass];
            if (owned && image) {
                menuBarButton = button;
                menuBarBrandImage = [image copy];
                NSString *symbol = nil;
                if ([menuBarIndicator isEqualToString:@"degraded"]) symbol = @"exclamationmark.circle";
                else if ([menuBarIndicator isEqualToString:@"recovery"]) symbol = @"exclamationmark.triangle.fill";
                else if ([menuBarIndicator isEqualToString:@"interrupted"]) symbol = @"exclamationmark.triangle.fill";
                else if ([menuBarIndicator isEqualToString:@"unknown"]) symbol = @"questionmark.circle";
                else if ([menuBarIndicator isEqualToString:@"rolling_back"]) symbol = @"arrow.uturn.backward";
                else if ([@[@"starting", @"reloading", @"stopping", @"recovering", @"changing"] containsObject:menuBarIndicator]) symbol = @"arrow.triangle.2.circlepath";
                image = (symbol ? [NSImage imageWithSystemSymbolName:symbol accessibilityDescription:nil] : nil) ?: [menuBarBrandImage copy];
                image.size = NSMakeSize(18, 18);
                image.template = YES;
                button.imageScaling = NSImageScaleNone;
                // Match the Swift host: stopped/offline dim the same brand
                // image. Live Mac download rate sits beside the icon when sampled.
                button.alphaValue = [menuBarIndicator isEqualToString:@"connecting"] ? 0.75 :
                    [menuBarIndicator isEqualToString:@"stopped"] ? 0.55 :
                    [menuBarIndicator isEqualToString:@"unreachable"] ? 0.35 : 1.0;
                applyMenuBarButtonLayout(button);
                button.toolTip = menuBarDescription;
                [button setAccessibilityLabel:menuBarDescription];
            }
            ((void (*)(id, SEL, NSImage *))original)(button, selector, image);
            if (owned && image && smokeActions()) NSLog(@"OpenSurge menu bar image: %.0f x %.0f pt; template=%d; state=%@; opacity=%.2f", button.image.size.width, button.image.size.height, button.image.isTemplate, menuBarIndicator, button.alphaValue);
        });
        if (!class_addMethod(buttonClass, selector, sized, method_getTypeEncoding(method))) {
            method_setImplementation(class_getInstanceMethod(buttonClass, selector), sized);
        }
    });
}

static BOOL openSurgeSkipLink(const char *name, unsigned flags) {
    if (flags & (IFF_LOOPBACK | IFF_POINTOPOINT)) return YES;
    static const char *prefixes[] = {"awdl", "llw", "bridge", "gif", "stf", "ap", "anpi", "vmenet", "vmnet", "utun", NULL};
    for (int i = 0; prefixes[i]; i++) {
        size_t n = strlen(prefixes[i]);
        if (strncmp(name, prefixes[i], n) == 0) return YES;
    }
    return NO;
}

uint64_t openSurgeLinkIbytes(void) {
    struct ifaddrs *addrs = NULL;
    if (getifaddrs(&addrs) != 0 || addrs == NULL) return 0;
    uint64_t total = 0;
    for (struct ifaddrs *ifa = addrs; ifa != NULL; ifa = ifa->ifa_next) {
        if (ifa->ifa_addr == NULL || ifa->ifa_addr->sa_family != AF_LINK || ifa->ifa_data == NULL) continue;
        if (openSurgeSkipLink(ifa->ifa_name, ifa->ifa_flags)) continue;
        struct if_data *data = (struct if_data *)ifa->ifa_data;
        total += data->ifi_ibytes;
    }
    freeifaddrs(addrs);
    return total;
}

int openSurgeCoreCPU(unsigned long long *used, unsigned long long *total, int capacity) {
    if (used == NULL || total == NULL || capacity <= 0) return 0;
    natural_t processors = 0;
    processor_info_array_t raw = NULL;
    mach_msg_type_number_t rawCount = 0;
    if (host_processor_info(mach_host_self(), PROCESSOR_CPU_LOAD_INFO, &processors, &raw, &rawCount) != KERN_SUCCESS || processors == 0 || raw == NULL) return 0;
    processor_cpu_load_info_t load = (processor_cpu_load_info_t)raw;
    int n = (int)processors;
    if (n > capacity) n = capacity;
    for (int i = 0; i < n; i++) {
        unsigned long long busy = (unsigned long long)load[i].cpu_ticks[CPU_STATE_USER]
            + (unsigned long long)load[i].cpu_ticks[CPU_STATE_SYSTEM]
            + (unsigned long long)load[i].cpu_ticks[CPU_STATE_NICE];
        unsigned long long idle = (unsigned long long)load[i].cpu_ticks[CPU_STATE_IDLE];
        used[i] = busy;
        total[i] = busy + idle;
    }
    vm_deallocate(mach_task_self(), (vm_address_t)raw, (vm_size_t)rawCount * sizeof(integer_t));
    return n;
}

int openSurgeHostCPU(unsigned long long *used, unsigned long long *total) {
    if (used == NULL || total == NULL) return 0;
    host_cpu_load_info_data_t info;
    mach_msg_type_number_t count = HOST_CPU_LOAD_INFO_COUNT;
    if (host_statistics(mach_host_self(), HOST_CPU_LOAD_INFO, (host_info_t)&info, &count) != KERN_SUCCESS) return 0;
    unsigned long long busy = (unsigned long long)info.cpu_ticks[CPU_STATE_USER]
        + (unsigned long long)info.cpu_ticks[CPU_STATE_SYSTEM]
        + (unsigned long long)info.cpu_ticks[CPU_STATE_NICE];
    unsigned long long idle = (unsigned long long)info.cpu_ticks[CPU_STATE_IDLE];
    *used = busy;
    *total = busy + idle;
    return 1;
}

extern int memorystatus_get_level(unsigned int *level);

static int openSurgeHostMemoryFromPages(double *percent) {
    vm_statistics64_data_t info;
    mach_msg_type_number_t count = HOST_VM_INFO64_COUNT;
    if (host_statistics64(mach_host_self(), HOST_VM_INFO64, (host_info64_t)&info, &count) != KERN_SUCCESS) return 0;
    uint64_t total = 0;
    size_t len = sizeof(total);
    if (sysctlbyname("hw.memsize", &total, &len, NULL, 0) != 0 || total == 0) return 0;
    uint64_t page = (uint64_t)getpagesize();
    uint64_t used = ((uint64_t)info.active_count + (uint64_t)info.wire_count + (uint64_t)info.compressor_page_count) * page;
    if (used > total) used = total;
    *percent = 100.0 * (double)used / (double)total;
    return 1;
}

int openSurgeHostMemory(double *percent) {
    if (percent == NULL) return 0;
    // Same source as memory_pressure: kernel free percentage, including
    // reclaimable cache. The menu bar shows occupancy, so invert it.
    unsigned int freeLevel = 0;
    if (memorystatus_get_level(&freeLevel) == 0 && freeLevel <= 100) {
        *percent = 100.0 - (double)freeLevel;
        return 1;
    }
    return openSurgeHostMemoryFromPages(percent);
}

static bool openSurgeSkipFS(const char *type) {
    static const char *skip[] = {"autofs", "devfs", "fdesc", "nfs", "smbfs", "afpfs", "webdav", "fuse", "nullfs", "overlay", "cddafs", NULL};
    if (type == NULL) return true;
    for (int i = 0; skip[i] != NULL; i++) {
        if (strcmp(type, skip[i]) == 0) return true;
    }
    return false;
}

static bool openSurgeCFBool(CFDictionaryRef desc, CFStringRef key, bool fallback) {
    if (desc == NULL || key == NULL) return fallback;
    CFBooleanRef value = CFDictionaryGetValue(desc, key);
    if (value == NULL || CFGetTypeID(value) != CFBooleanGetTypeID()) return fallback;
    return CFBooleanGetValue(value);
}

static bool openSurgeDiskInternal(DASessionRef session, const char *mntfrom, char *wholeOut, size_t wholeLen) {
    if (session == NULL || mntfrom == NULL) return false;
    if (strncmp(mntfrom, "/dev/", 5) != 0) return false;
    const char *bsd = mntfrom + 5;
    if (strncmp(bsd, "disk", 4) != 0) return false;
    DADiskRef disk = DADiskCreateFromBSDName(kCFAllocatorDefault, session, bsd);
    if (disk == NULL) return false;
    DADiskRef whole = DADiskCopyWholeDisk(disk);
    CFRelease(disk);
    if (whole == NULL) return false;
    const char *wholeBSD = DADiskGetBSDName(whole);
    if (wholeBSD != NULL && wholeOut != NULL && wholeLen > 0) {
        strlcpy(wholeOut, wholeBSD, wholeLen);
    }
    CFDictionaryRef desc = DADiskCopyDescription(whole);
    CFRelease(whole);
    if (desc == NULL) return false;
    bool internal = openSurgeCFBool(desc, kDADiskDescriptionDeviceInternalKey, false);
    bool removable = openSurgeCFBool(desc, kDADiskDescriptionMediaRemovableKey, false);
    bool ejectable = openSurgeCFBool(desc, kDADiskDescriptionMediaEjectableKey, false);
    CFStringRef protocol = CFDictionaryGetValue(desc, kDADiskDescriptionDeviceProtocolKey);
    bool externalBus = false;
    if (protocol != NULL && CFGetTypeID(protocol) == CFStringGetTypeID()) {
        externalBus = CFStringCompare(protocol, CFSTR("USB"), 0) == kCFCompareEqualTo
            || CFStringCompare(protocol, CFSTR("Secure Digital"), 0) == kCFCompareEqualTo
            || CFStringCompare(protocol, CFSTR("FireWire"), 0) == kCFCompareEqualTo
            || CFStringCompare(protocol, CFSTR("Disk Image"), 0) == kCFCompareEqualTo;
    }
    CFStringRef model = CFDictionaryGetValue(desc, kDADiskDescriptionDeviceModelKey);
    bool image = model != NULL && CFGetTypeID(model) == CFStringGetTypeID()
        && CFStringFind(model, CFSTR("Disk Image"), kCFCompareCaseInsensitive).location != kCFNotFound;
    CFRelease(desc);
    return internal && !removable && !ejectable && !externalBus && !image;
}

typedef struct {
    char whole[32];
    uint64_t total;
    uint64_t used;
} OpenSurgeDiskAcc;

static int openSurgeDiskAccIndex(OpenSurgeDiskAcc *acc, int n, const char *whole) {
    if (whole == NULL || whole[0] == 0) return -1;
    for (int i = 0; i < n; i++) {
        if (strcmp(acc[i].whole, whole) == 0) return i;
    }
    return -1;
}

int openSurgeHostDisk(double *percent) {
    if (percent == NULL) return 0;
    DASessionRef session = DASessionCreate(kCFAllocatorDefault);
    OpenSurgeDiskAcc acc[8] = {{0}};
    int accN = 0;
    struct statfs *mnts = NULL;
    int n = getmntinfo(&mnts, MNT_NOWAIT);
    if (session != NULL && mnts != NULL) {
        for (int i = 0; i < n; i++) {
            if ((mnts[i].f_flags & MNT_LOCAL) == 0) continue;
            if ((mnts[i].f_flags & MNT_SNAPSHOT) != 0) continue;
            if ((mnts[i].f_flags & MNT_REMOVABLE) != 0) continue;
            if (openSurgeSkipFS(mnts[i].f_fstypename)) continue;
            if (mnts[i].f_blocks == 0) continue;
            char whole[32] = {0};
            if (!openSurgeDiskInternal(session, mnts[i].f_mntfromname, whole, sizeof whole)) continue;
            uint64_t bsize = (uint64_t)mnts[i].f_bsize;
            uint64_t blocks = (uint64_t)mnts[i].f_blocks * bsize;
            uint64_t busy = ((uint64_t)mnts[i].f_blocks - (uint64_t)mnts[i].f_bfree) * bsize;
            int idx = openSurgeDiskAccIndex(acc, accN, whole);
            if (idx < 0) {
                if (accN >= 8 || whole[0] == 0) continue;
                idx = accN++;
                strlcpy(acc[idx].whole, whole, sizeof acc[idx].whole);
                acc[idx].total = blocks;
                acc[idx].used = busy;
                continue;
            }
            // Keep the largest volume of an APFS container so Preboot/VM do not replace Data.
            if (blocks > acc[idx].total) {
                acc[idx].total = blocks;
                acc[idx].used = busy;
            }
        }
    }
    if (session != NULL) CFRelease(session);
    uint64_t total = 0;
    uint64_t used = 0;
    for (int i = 0; i < accN; i++) {
        total += acc[i].total;
        used += acc[i].used;
    }
    if (total == 0) {
        struct statfs root;
        if (statfs("/", &root) != 0 || root.f_blocks == 0) return 0;
        uint64_t bsize = (uint64_t)root.f_bsize;
        total = (uint64_t)root.f_blocks * bsize;
        used = ((uint64_t)root.f_blocks - (uint64_t)root.f_bfree) * bsize;
    }
    *percent = 100.0 * (double)used / (double)total;
    if (*percent < 0) *percent = 0;
    if (*percent > 100) *percent = 100;
    return 1;
}

typedef struct CF_BRIDGED_TYPE(id) __IOHIDEventSystemClient *OpenSurgeHIDClient;
typedef struct CF_BRIDGED_TYPE(id) __IOHIDServiceClient *OpenSurgeHIDService;
typedef struct __IOHIDEvent *OpenSurgeHIDEvent;
extern OpenSurgeHIDClient IOHIDEventSystemClientCreate(CFAllocatorRef);
extern int IOHIDEventSystemClientSetMatching(OpenSurgeHIDClient, CFDictionaryRef);
extern CFArrayRef IOHIDEventSystemClientCopyServices(OpenSurgeHIDClient);
extern CFTypeRef IOHIDServiceClientCopyProperty(OpenSurgeHIDService, CFStringRef);
extern OpenSurgeHIDEvent IOHIDServiceClientCopyEvent(OpenSurgeHIDService, int64_t, int32_t, int64_t);
extern double IOHIDEventGetFloatValue(OpenSurgeHIDEvent, int32_t);

enum {
    kOpenSurgeHIDTemperature = 15,
    kOpenSurgeHIDTemperatureField = kOpenSurgeHIDTemperature << 16
};

static BOOL openSurgeCPUSensorName(NSString *name) {
    NSString *n = name.lowercaseString;
    if (n.length == 0) return NO;
    if ([n containsString:@"gpu"] || [n containsString:@"nand"] || [n containsString:@"battery"] || [n containsString:@"wifi"] || [n containsString:@"airport"] || [n containsString:@"tcal"]) return NO;
    return [n containsString:@"cpu"] || [n containsString:@"pacc"] || [n containsString:@"eacc"] || [n containsString:@"tdie"];
}

int openSurgeCPUTempC(double *celsius) {
    if (celsius == NULL) return 0;
    static OpenSurgeHIDClient client;
    static dispatch_once_t once;
    dispatch_once(&once, ^{
        client = IOHIDEventSystemClientCreate(kCFAllocatorDefault);
        if (client) {
            NSDictionary *match = @{@"PrimaryUsagePage": @(0xff00), @"PrimaryUsage": @5};
            IOHIDEventSystemClientSetMatching(client, (__bridge CFDictionaryRef)match);
        }
    });
    if (!client) return 0;
    CFArrayRef services = IOHIDEventSystemClientCopyServices(client);
    if (!services) return 0;
    double hottest = -1000;
    CFIndex count = CFArrayGetCount(services);
    for (CFIndex i = 0; i < count; i++) {
        OpenSurgeHIDService service = (OpenSurgeHIDService)CFArrayGetValueAtIndex(services, i);
        CFStringRef product = (CFStringRef)IOHIDServiceClientCopyProperty(service, CFSTR("Product"));
        NSString *name = product ? (__bridge NSString *)product : @"";
        BOOL cpu = openSurgeCPUSensorName(name);
        if (product) CFRelease(product);
        if (!cpu) continue;
        OpenSurgeHIDEvent event = IOHIDServiceClientCopyEvent(service, kOpenSurgeHIDTemperature, 0, 0);
        if (!event) continue;
        double t = IOHIDEventGetFloatValue(event, kOpenSurgeHIDTemperatureField);
        CFRelease(event);
        if (t > 200 && t < 400) t -= 273.15;
        if (t < 0 || t > 150) continue;
        if (t > hottest) hottest = t;
    }
    CFRelease(services);
    if (hottest < 0) return 0;
    *celsius = hottest;
    return 1;
}

// Fan sample uses the same SMC keys as the opensurge-fan curve: the hottest
// voltage-regulator, GPU, or performance-core sensor, plus the live fan RPM.
// Reading does not need root.

typedef struct { char major, minor, build, reserved[1]; UInt16 release; } OpenSurgeSMCVers;
typedef struct { UInt16 version, length; UInt32 cpu, gpu, mem; } OpenSurgeSMCLimit;
typedef struct { UInt32 dataSize, dataType; char dataAttributes; } OpenSurgeSMCKeyInfo;
typedef struct {
    UInt32 key;
    OpenSurgeSMCVers vers;
    OpenSurgeSMCLimit plimit;
    OpenSurgeSMCKeyInfo ki;
    char result, status, data8;
    UInt32 data32;
    unsigned char bytes[32];
} OpenSurgeSMCData;

enum { kOpenSurgeSMCRead = 5, kOpenSurgeSMCKeyAtIndex = 8, kOpenSurgeSMCKeyInfo = 9 };

static io_connect_t openSurgeSMC;
static char openSurgeSensorKeys[400][5];
static int openSurgeSensorCount;
static char openSurgeTeKeys[96][5];
static char openSurgeTpKeys[96][5];
static char openSurgeTgKeys[96][5];
static char openSurgeTvdKeys[96][5];
static int openSurgeTeCount, openSurgeTpCount, openSurgeTgCount, openSurgeTvdCount;
static char openSurgeCoreCluster[64];
static int openSurgeCoreClusterCount;
static int openSurgeCoreClustersReady;
static int openSurgeSensorReady;

static UInt32 openSurgeSMCKey(const char *s) {
    return ((UInt32)(unsigned char)s[0] << 24) | ((UInt32)(unsigned char)s[1] << 16) | ((UInt32)(unsigned char)s[2] << 8) | (UInt32)(unsigned char)s[3];
}

static void openSurgeSMCKeyString(UInt32 key, char *out) {
    out[0] = (char)(key >> 24);
    out[1] = (char)(key >> 16);
    out[2] = (char)(key >> 8);
    out[3] = (char)key;
    out[4] = 0;
}

static int openSurgeSMCCall(OpenSurgeSMCData *in, OpenSurgeSMCData *out) {
    size_t outSize = sizeof(*out);
    return IOConnectCallStructMethod(openSurgeSMC, 2, in, sizeof(*in), out, &outSize);
}

static int openSurgeSMCRead(const char *key, unsigned char *bytes, char type[5]) {
    OpenSurgeSMCData in = {0}, out = {0};
    in.key = openSurgeSMCKey(key);
    in.data8 = kOpenSurgeSMCKeyInfo;
    if (openSurgeSMCCall(&in, &out) || out.result) return 0;
    if (type) openSurgeSMCKeyString(out.ki.dataType, type);
    UInt32 size = out.ki.dataSize;
    if (size > 32) size = 32;
    memset(&in, 0, sizeof(in));
    in.key = openSurgeSMCKey(key);
    in.ki.dataSize = size;
    in.data8 = kOpenSurgeSMCRead;
    memset(&out, 0, sizeof(out));
    if (openSurgeSMCCall(&in, &out) || out.result) return 0;
    memcpy(bytes, out.bytes, 32);
    return 1;
}

static double openSurgeSMCNumber(const char *key) {
    unsigned char bytes[32];
    char type[5] = {0};
    if (!openSurgeSMCRead(key, bytes, type)) return -1;
    if (strcmp(type, "flt ") == 0) {
        float value;
        memcpy(&value, bytes, 4);
        return value;
    }
    if (strcmp(type, "ui8 ") == 0) return bytes[0];
    if (strcmp(type, "ui16") == 0) return (bytes[0] << 8) | bytes[1];
    return -1;
}

static int openSurgeDecidingSensor(const char *key) {
    return strncmp(key, "TVD", 3) == 0 || strncmp(key, "Tg", 2) == 0 || strncmp(key, "Tp", 2) == 0;
}

static int openSurgeSMCOpen(void) {
    if (openSurgeSMC) return 1;
    io_service_t service = IOServiceGetMatchingService(kIOMainPortDefault, IOServiceMatching("AppleSMC"));
    if (!service) return 0;
    kern_return_t result = IOServiceOpen(service, mach_task_self(), 0, &openSurgeSMC);
    IOObjectRelease(service);
    if (result != KERN_SUCCESS || !openSurgeSMC) {
        openSurgeSMC = 0;
        return 0;
    }
    if (openSurgeSensorReady) return 1;
    unsigned char bytes[32];
    if (!openSurgeSMCRead("#KEY", bytes, NULL)) return 1;
    UInt32 count = ((UInt32)bytes[0] << 24) | ((UInt32)bytes[1] << 16) | ((UInt32)bytes[2] << 8) | bytes[3];
    for (UInt32 i = 0; i < count; i++) {
        OpenSurgeSMCData in = {0}, out = {0};
        in.data8 = kOpenSurgeSMCKeyAtIndex;
        in.data32 = i;
        if (openSurgeSMCCall(&in, &out)) continue;
        char key[5];
        openSurgeSMCKeyString(out.key, key);
        if (strncmp(key, "Te", 2) == 0 && openSurgeTeCount < 96) memcpy(openSurgeTeKeys[openSurgeTeCount++], key, 5);
        else if (strncmp(key, "Tp", 2) == 0 && openSurgeTpCount < 96) memcpy(openSurgeTpKeys[openSurgeTpCount++], key, 5);
        else if (strncmp(key, "Tg", 2) == 0 && openSurgeTgCount < 96) memcpy(openSurgeTgKeys[openSurgeTgCount++], key, 5);
        else if (strncmp(key, "TVD", 3) == 0 && openSurgeTvdCount < 96) memcpy(openSurgeTvdKeys[openSurgeTvdCount++], key, 5);
        if (!openSurgeDecidingSensor(key) || openSurgeSensorCount >= 400) continue;
        double value = openSurgeSMCNumber(key);
        if (value < 5 || value > 130) continue;
        memcpy(openSurgeSensorKeys[openSurgeSensorCount], key, 5);
        openSurgeSensorCount++;
    }
    openSurgeSensorReady = 1;
    return 1;
}

static void openSurgeLoadCoreClusters(void) {
    if (openSurgeCoreClustersReady) return;
    openSurgeCoreClustersReady = 1;
    io_iterator_t iter = IO_OBJECT_NULL;
    if (IOServiceGetMatchingServices(kIOMainPortDefault, IOServiceMatching("IOPlatformDevice"), &iter) != KERN_SUCCESS) return;
    io_service_t service;
    while ((service = IOIteratorNext(iter))) {
        CFTypeRef idRef = IORegistryEntryCreateCFProperty(service, CFSTR("logical-cpu-id"), kCFAllocatorDefault, 0);
        CFTypeRef typeRef = IORegistryEntryCreateCFProperty(service, CFSTR("cluster-type"), kCFAllocatorDefault, 0);
        int cpu = -1;
        if (idRef && CFGetTypeID(idRef) == CFNumberGetTypeID()) CFNumberGetValue((CFNumberRef)idRef, kCFNumberIntType, &cpu);
        char kind = 0;
        if (typeRef && CFGetTypeID(typeRef) == CFDataGetTypeID()) {
            const UInt8 *bytes = CFDataGetBytePtr((CFDataRef)typeRef);
            if (bytes && CFDataGetLength((CFDataRef)typeRef) > 0) kind = (char)bytes[0];
        } else if (typeRef && CFGetTypeID(typeRef) == CFStringGetTypeID()) {
            char tmp[4] = {0};
            CFStringGetCString((CFStringRef)typeRef, tmp, sizeof tmp, kCFStringEncodingUTF8);
            kind = tmp[0];
        }
        if (cpu >= 0 && cpu < 64 && (kind == 'E' || kind == 'P' || kind == 'M' || kind == 'S')) {
            openSurgeCoreCluster[cpu] = kind;
            if (cpu + 1 > openSurgeCoreClusterCount) openSurgeCoreClusterCount = cpu + 1;
        }
        if (idRef) CFRelease(idRef);
        if (typeRef) CFRelease(typeRef);
        IOObjectRelease(service);
    }
    IOObjectRelease(iter);
}

static double openSurgeGroupTemp(char keys[][5], int count) {
    double sum = 0;
    int used = 0;
    for (int i = 0; i < count; i++) {
        double value = openSurgeSMCNumber(keys[i]);
        if (value < 5 || value > 130) continue;
        sum += value;
        used++;
    }
    return used ? sum / used : -1;
}

int openSurgeCoreTemps(double *out, int capacity) {
    if (out == NULL || capacity <= 0 || !openSurgeSMCOpen()) return 0;
    unsigned long long used[64], total[64];
    int n = openSurgeCoreCPU(used, total, 64);
    if (n <= 0) return 0;
    if (n > capacity) n = capacity;
    openSurgeLoadCoreClusters();
    double efficiency = openSurgeGroupTemp(openSurgeTeKeys, openSurgeTeCount);
    double performance = openSurgeGroupTemp(openSurgeTpKeys, openSurgeTpCount);
    for (int i = 0; i < n; i++) {
        char kind = i < 64 ? openSurgeCoreCluster[i] : 0;
        double value = kind == 'E' ? (efficiency >= 0 ? efficiency : performance) : (performance >= 0 ? performance : efficiency);
        out[i] = value;
    }
    return n;
}

static double openSurgeGroupFinal(char keys[][5], int count) {
    double values[96];
    int n = 0;
    for (int i = 0; i < count && n < 96; i++) {
        double value = openSurgeSMCNumber(keys[i]);
        if (value < 5 || value >= 130) continue;
        values[n++] = value;
    }
    if (n <= 0) return -1;
    if (n >= 3) {
        int hot = 0;
        for (int i = 1; i < n; i++) if (values[i] > values[hot]) hot = i;
        double sum = 0;
        for (int i = 0; i < n; i++) if (i != hot) sum += values[i];
        return sum / (n - 1);
    }
    double sum = 0;
    for (int i = 0; i < n; i++) sum += values[i];
    return sum / n;
}

int openSurgeGroupTemps(double *power, double *gpu, double *performance) {
    if (power == NULL || gpu == NULL || performance == NULL || !openSurgeSMCOpen()) return 0;
    *power = openSurgeGroupFinal(openSurgeTvdKeys, openSurgeTvdCount);
    *gpu = openSurgeGroupFinal(openSurgeTgKeys, openSurgeTgCount);
    *performance = openSurgeGroupFinal(openSurgeTpKeys, openSurgeTpCount);
    return (*power >= 0 || *gpu >= 0 || *performance >= 0) ? 1 : 0;
}

static const char *openSurgeFanKey(int fan, const char *suffix) {
    static char keys[4][5];
    snprintf(keys[fan & 3], 5, "F%d%s", fan, suffix);
    return keys[fan & 3];
}

int openSurgeFanSample(double *celsius, int *tempOK, double *rpm, double *minRPM, double *maxRPM) {
    if (celsius == NULL || tempOK == NULL || rpm == NULL || minRPM == NULL || maxRPM == NULL) return 0;
    *tempOK = 0;
    *celsius = 0;
    *rpm = 0;
    *minRPM = 0;
    *maxRPM = 0;
    if (!openSurgeSMCOpen()) return 0;
    int fans = (int)openSurgeSMCNumber("FNum");
    if (fans < 1) fans = 1;
    if (fans > 4) fans = 4;
    double fastest = -1;
    for (int i = 0; i < fans; i++) {
        double actual = openSurgeSMCNumber(openSurgeFanKey(i, "Ac"));
        if (actual > fastest) fastest = actual;
    }
    if (fastest < 0) return 0;
    *rpm = fastest;
    double low = openSurgeSMCNumber(openSurgeFanKey(0, "Mn"));
    double high = openSurgeSMCNumber(openSurgeFanKey(0, "Mx"));
    if (high > low && low >= 0) {
        *minRPM = low;
        *maxRPM = high;
    }
    double hottest = -1;
    for (int i = 0; i < openSurgeSensorCount; i++) {
        double value = openSurgeSMCNumber(openSurgeSensorKeys[i]);
        if (value > hottest && value < 130) hottest = value;
    }
    if (hottest >= 0) {
        *celsius = hottest;
        *tempOK = 1;
    }
    return 1;
}
