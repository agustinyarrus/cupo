//go:build windows

package main

// placement_windows.go: dónde y con qué tamaño nace la ventana del panel, y cómo se recuerda.
//   - Al abrir: la última geometría guardada, reubicada en el monitor donde cae (con el DPI de ese monitor).
//     Si no hay, el tamaño inicial abajo a la derecha del monitor principal, junto a la bandeja. Se fija con un hook
//     CBT: la ventana nace en su lugar y con su tamaño, sin el parpadeo de la posición por defecto.
//   - Al mover, cambiar tamaño, maximizar o cerrar: se guarda la geometría (píxeles físicos + DPI).
//   - Si el monitor cambia la escala, Windows manda WM_DPICHANGED con el rectángulo que corresponde; se aplica.

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                = windows.NewLazySystemDLL("kernel32.dll")
	shcore                  = windows.NewLazySystemDLL("shcore.dll")
	pGetCurrentThreadId     = kernel32.NewProc("GetCurrentThreadId")
	pGetDpiForMonitor       = shcore.NewProc("GetDpiForMonitor")
	pMonitorFromRect        = user32.NewProc("MonitorFromRect")
	pGetMonitorInfoW        = user32.NewProc("GetMonitorInfoW")
	pGetWindowRect          = user32.NewProc("GetWindowRect")
	pGetWindowPlacement     = user32.NewProc("GetWindowPlacement")
	pGetDpiForWindow        = user32.NewProc("GetDpiForWindow")
	pGetSystemMetricsForDpi = user32.NewProc("GetSystemMetricsForDpi")
	pSetWindowsHookExW      = user32.NewProc("SetWindowsHookExW")
	pUnhookWindowsHookEx    = user32.NewProc("UnhookWindowsHookEx")
	pCallNextHookEx         = user32.NewProc("CallNextHookEx")
)

const (
	monitorDefaultToPrimary = 1
	monitorDefaultToNearest = 2
	mdtEffectiveDPI         = 0
	whCBT                   = 5
	hcbtCreateWnd           = 3
	wmGetMinMaxInfo         = 0x0024
	wmSize                  = 0x0005
	wmExitSizeMove          = 0x0232
	wmDpiChanged            = 0x02E0
	sizeRestored            = 0
	sizeMaximized           = 2
	swShowMinimized         = 2
	swShowMaximized         = 3
	swpNoActivateFlag       = 0x0010
	gwlExStyle              = ^uintptr(19) // GWL_EXSTYLE = -20
	wsExToolWindow          = 0x00000080
)

const (
	smCXScreen = 0 // GetSystemMetrics: ancho de la pantalla principal
	smCYScreen = 1 // alto de la pantalla principal
)

// monitorInfo es MONITORINFO; windowPlacement es WINDOWPLACEMENT; minMaxInfo es MINMAXINFO (x64).
type monitorInfo struct {
	CbSize    uint32
	RcMonitor winRect
	RcWork    winRect
	DwFlags   uint32
}

type windowPlacement struct {
	Length           uint32
	Flags            uint32
	ShowCmd          uint32
	PtMinPosition    winPoint
	PtMaxPosition    winPoint
	RcNormalPosition winRect
}

type minMaxInfo struct {
	PtReserved     winPoint
	PtMaxSize      winPoint
	PtMaxPosition  winPoint
	PtMinTrackSize winPoint
	PtMaxTrackSize winPoint
}

// createStructW es CREATESTRUCTW (x64). cbtCreateWnd es lo que recibe el hook WH_CBT al crear una ventana.
type createStructW struct {
	LpCreateParams uintptr
	HInstance      uintptr
	HMenu          uintptr
	HwndParent     uintptr
	Cy, Cx, Y, X   int32
	Style          int32
	LpszName       uintptr
	LpszClass      uintptr
	DwExStyle      uint32
}

type cbtCreateWnd struct {
	Lpcs            uintptr
	HwndInsertAfter uintptr
}

// ptrFrom convierte un uintptr que entrega Windows en puntero Go, sin la advertencia de vet.
func ptrFrom(u uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&u))
}

// u16ToString lee un texto UTF-16 terminado en cero que apunta una dirección de Windows.
func u16ToString(p uintptr) string {
	if p == 0 {
		return ""
	}
	base := ptrFrom(p)
	var buf []uint16
	for i := 0; i < 64; i++ {
		c := *(*uint16)(unsafe.Add(base, i*2))
		if c == 0 {
			break
		}
		buf = append(buf, c)
	}
	return windows.UTF16ToString(buf)
}

// monitorFor devuelve el monitor donde cae el rectángulo r (o el más cercano, si cae fuera de todos).
func monitorFor(r winRect) uintptr {
	return call(pMonitorFromRect, uintptr(unsafe.Pointer(&r)), monitorDefaultToNearest)
}

// primaryMonitor devuelve el monitor principal.
func primaryMonitor() uintptr {
	var r winRect
	return call(pMonitorFromRect, uintptr(unsafe.Pointer(&r)), monitorDefaultToPrimary)
}

// monitorData devuelve el área de trabajo del monitor (sin la barra de tareas) y su DPI efectivo.
func monitorData(hmon uintptr) (rect, int32) {
	mi := monitorInfo{CbSize: uint32(unsafe.Sizeof(monitorInfo{}))}
	call(pGetMonitorInfoW, hmon, uintptr(unsafe.Pointer(&mi)))
	work := rect{L: mi.RcWork.Left, T: mi.RcWork.Top, R: mi.RcWork.Right, B: mi.RcWork.Bottom}
	if work.R <= work.L || work.B <= work.T {
		// Sin datos del monitor: se usa la pantalla principal entera.
		work = rect{R: sysMetric(smCXScreen), B: sysMetric(smCYScreen)}
	}
	var dx, dy uint32
	if hr := call(pGetDpiForMonitor, hmon, mdtEffectiveDPI,
		uintptr(unsafe.Pointer(&dx)), uintptr(unsafe.Pointer(&dy))); hr != 0 || dx == 0 {
		return work, int32(call(pGetDpiForSystem))
	}
	return work, int32(dx)
}

// windowDPI devuelve el DPI de la ventana (el de su monitor). 96 si Windows no lo informa.
func windowDPI(hwnd uintptr) int32 {
	if d := call(pGetDpiForWindow, hwnd); d != 0 {
		return int32(d)
	}
	return 96
}

// sysMetricDPI es GetSystemMetricsForDpi: las medidas del marco según el DPI de la ventana, no del sistema.
func sysMetricDPI(index int, dpi int32) int32 {
	return int32(call(pGetSystemMetricsForDpi, uintptr(index), uintptr(dpi)))
}

// panelPlacement decide dónde y con qué tamaño nace la ventana: la geometría guardada (reubicada en el
// monitor donde cae, con su DPI) o, si no hay, el tamaño inicial centrado en el monitor principal.
func (a *App) panelPlacement() winGeom {
	if g := a.savedWindow(); g != nil && g.valid() {
		work, dpi := monitorData(monitorFor(winRect{Left: g.X, Top: g.Y, Right: g.X + g.W, Bottom: g.Y + g.H}))
		x, y, w, h := fitGeom(*g, dpi, work)
		return winGeom{X: x, Y: y, W: w, H: h, DPI: dpi, Max: g.Max}
	}
	work, dpi := monitorData(primaryMonitor())
	x, y, w, h := defaultGeom(dpi, work)
	return winGeom{X: x, Y: y, W: w, H: h, DPI: dpi}
}

// panelCreate es la geometría con la que nace la ventana en curso. La lee el hook CBT, en el mismo hilo.
var panelCreate winGeom

// panelApp es el dueño del panel abierto: la subclase le guarda ahí la geometría cuando cambia la ventana.
// panelSaveOK se pone en true recién cuando la ventana ya nació; antes no se guarda nada.
var (
	panelApp    *App
	panelSaveOK bool
	cbtCB       uintptr
	cbtHook     uintptr
)

// cbtProc corre dentro de CreateWindowEx: pone la posición y el tamaño de la ventana del panel antes de que
// exista, la subclasa ya sin marco (la primera pintura no trae barra de título) y la marca como ventana de
// herramientas antes de que se vea, para que no tenga botón en la barra de tareas.
func cbtProc(nCode, wParam, lParam uintptr) uintptr {
	if int32(nCode) == hcbtCreateWnd && lParam != 0 {
		cbt := (*cbtCreateWnd)(ptrFrom(lParam))
		if cbt.Lpcs != 0 {
			cs := (*createStructW)(ptrFrom(cbt.Lpcs))
			if cs.HwndParent == 0 && cs.LpszClass > 0xFFFF && u16ToString(cs.LpszClass) == "webview" {
				subclassFrameless(wParam)
				call(pSetWindowLongPtrW, wParam, gwlExStyle, call(pGetWindowLongPtrW, wParam, gwlExStyle)|wsExToolWindow)
				cs.X, cs.Y = panelCreate.X, panelCreate.Y
				cs.Cx, cs.Cy = panelCreate.W, panelCreate.H
			}
		}
	}
	r, _, _ := pCallNextHookEx.Call(0, nCode, wParam, lParam)
	return r
}

// installCBT activa el hook para la ventana que está por crearse en este hilo.
func installCBT() {
	if cbtCB == 0 {
		cbtCB = windows.NewCallback(cbtProc)
	}
	cbtHook = call(pSetWindowsHookExW, whCBT, cbtCB, 0, call(pGetCurrentThreadId))
}

// uninstallCBT quita el hook. Siempre se llama después de crear la ventana, haya salido bien o no.
func uninstallCBT() {
	if cbtHook != 0 {
		call(pUnhookWindowsHookEx, cbtHook)
		cbtHook = 0
	}
}

// subclassFrameless pone la subclase del panel en la ventana. Se puede llamar más de una vez sin duplicarla.
func subclassFrameless(hwnd uintptr) {
	if cur := call(pGetWindowLongPtrW, hwnd, gwlpWndProc); cur != framedProcCB {
		origWndProc = cur
		call(pSetWindowLongPtrW, hwnd, gwlpWndProc, framedProcCB)
	}
}

// saveWindowState guarda la geometría actual: la posición normal (la de antes de maximizar), el tamaño, el
// DPI de la ventana y si está maximizada. Minimizada no guarda nada (no tiene una geometría que recordar).
func saveWindowState(hwnd uintptr) {
	app := panelApp
	if app == nil || !panelSaveOK {
		return
	}
	var wpl windowPlacement
	wpl.Length = uint32(unsafe.Sizeof(wpl))
	call(pGetWindowPlacement, hwnd, uintptr(unsafe.Pointer(&wpl)))
	if wpl.ShowCmd == swShowMinimized {
		return
	}
	isMax := wpl.ShowCmd == swShowMaximized
	r := wpl.RcNormalPosition
	if !isMax {
		var wr winRect
		call(pGetWindowRect, hwnd, uintptr(unsafe.Pointer(&wr)))
		r = wr
	}
	app.saveWindow(winGeom{
		X: r.Left, Y: r.Top, W: r.Right - r.Left, H: r.Bottom - r.Top,
		DPI: windowDPI(hwnd), Max: isMax,
	})
}
