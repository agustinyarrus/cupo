//go:build windows

package main

// Declaraciones Win32 que no están en x/sys. Mismo enfoque que el vigía y folio: NewProc directo, sin cgo.

import "golang.org/x/sys/windows"

var (
	user32  = windows.NewLazySystemDLL("user32.dll")
	shell32 = windows.NewLazySystemDLL("shell32.dll")
	dwmapi  = windows.NewLazySystemDLL("dwmapi.dll")
)

var (
	pRegisterClassExW              = user32.NewProc("RegisterClassExW")
	pCreateWindowExW               = user32.NewProc("CreateWindowExW")
	pDefWindowProcW                = user32.NewProc("DefWindowProcW")
	pGetMessageW                   = user32.NewProc("GetMessageW")
	pTranslateMessage              = user32.NewProc("TranslateMessage")
	pDispatchMessageW              = user32.NewProc("DispatchMessageW")
	pPostMessageW                  = user32.NewProc("PostMessageW")
	pPostQuitMessage               = user32.NewProc("PostQuitMessage")
	pSendMessageW                  = user32.NewProc("SendMessageW")
	pFindWindowW                   = user32.NewProc("FindWindowW")
	pRegisterWindowMessageW        = user32.NewProc("RegisterWindowMessageW")
	pCreatePopupMenu               = user32.NewProc("CreatePopupMenu")
	pAppendMenuW                   = user32.NewProc("AppendMenuW")
	pSetMenuDefaultItem            = user32.NewProc("SetMenuDefaultItem")
	pTrackPopupMenu                = user32.NewProc("TrackPopupMenu")
	pDestroyMenu                   = user32.NewProc("DestroyMenu")
	pGetCursorPos                  = user32.NewProc("GetCursorPos")
	pSetForegroundWindow           = user32.NewProc("SetForegroundWindow")
	pAllowSetForegroundWindow      = user32.NewProc("AllowSetForegroundWindow")
	pCreateIconFromResourceEx      = user32.NewProc("CreateIconFromResourceEx")
	pDestroyIcon                   = user32.NewProc("DestroyIcon")
	pSetWindowLongPtrW             = user32.NewProc("SetWindowLongPtrW")
	pGetWindowLongPtrW             = user32.NewProc("GetWindowLongPtrW")
	pCallWindowProcW               = user32.NewProc("CallWindowProcW")
	pSetClassLongPtrW              = user32.NewProc("SetClassLongPtrW")
	pReleaseCapture                = user32.NewProc("ReleaseCapture")
	pShowWindow                    = user32.NewProc("ShowWindow")
	pIsZoomed                      = user32.NewProc("IsZoomed")
	pIsIconic                      = user32.NewProc("IsIconic")
	pGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	pGetDpiForSystem               = user32.NewProc("GetDpiForSystem")
	pSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	pSetWindowPos                  = user32.NewProc("SetWindowPos")
	pMessageBoxW                   = user32.NewProc("MessageBoxW")
	pShellNotifyIconW              = shell32.NewProc("Shell_NotifyIconW")
	pDwmSetWindowAttribute         = dwmapi.NewProc("DwmSetWindowAttribute")
)

const (
	wmNull            = 0x0000
	wmDestroy         = 0x0002
	wmActivate        = 0x0006
	wmClose           = 0x0010
	wmSetIcon         = 0x0080
	wmNCCalcSize      = 0x0083
	wmNCLButtonDown   = 0x00A1
	wmMouseMove       = 0x0200
	wmLButtonUp       = 0x0202
	wmRButtonUp       = 0x0205
	wmContextMenu     = 0x007B
	wmApp             = 0x8000
	waInactive        = 0
	htCaption         = 2
	swMaximize        = 3
	swRestore         = 9
	swShow            = 5
	smCXSizeFrame     = 32
	smCXPaddedBorder  = 92
	smCXSmIcon        = 49
	gwlpWndProc       = ^uintptr(3) // GWLP_WNDPROC = -4
	gclpHbrBackground = ^uintptr(9) // GCLP_HBRBACKGROUND = -10
	iconSmall         = 0
	iconBig           = 1
	swpNoSize         = 0x0001
	swpNoMove         = 0x0002
	swpNoZOrder       = 0x0004
	swpFrameChanged   = 0x0020
	tpmRightButton    = 0x0002
	tpmReturnCmd      = 0x0100
	tpmNonotify       = 0x0080
	tpmBottomAlign    = 0x0020
	mfString          = 0x0000
	mfGrayed          = 0x0001
	mfSeparator       = 0x0800
	mbTopMost         = 0x00040000
	asfwAny           = ^uintptr(0) // ASFW_ANY = -1
	dpiAwareV2        = ^uintptr(3) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
	dwmaDarkMode      = 20
	dwmaCornerPref    = 33
	dwmaBorderColor   = 34
	dwmwcpRound       = 2
)

// call ejecuta un proc de user32/shell32 y devuelve su retorno (los errores se miran por separado).
func call(p interface {
	Call(a ...uintptr) (uintptr, uintptr, error)
}, args ...uintptr) uintptr {
	r, _, _ := p.Call(args...)
	return r
}

func utf16Ptr(s string) *uint16 {
	p, _ := windows.UTF16PtrFromString(s)
	return p
}

func sysMetric(i int) int32 { return int32(call(pGetSystemMetrics, uintptr(i))) }

// setDPIAware declara Per-Monitor-V2 antes de crear ventanas; si no, WebView2 se dibuja a 96 DPI y Windows lo estira.
func setDPIAware() {
	if call(pSetProcessDpiAwarenessContext, dpiAwareV2) == 0 {
		user32.NewProc("SetProcessDPIAware").Call()
	}
}

// winPoint, winMsg y winRect replican POINT, MSG y RECT (x64).
type winPoint struct{ X, Y int32 }

type winMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      winPoint
}

type winRect struct{ Left, Top, Right, Bottom int32 }

// wndClassEx es WNDCLASSEXW (80 bytes en x64).
type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type ncCalcSizeParams struct {
	Rgrc [3]winRect
	Pos  uintptr
}
