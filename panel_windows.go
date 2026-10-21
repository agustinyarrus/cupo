//go:build windows

package main

// Panel: una ventana WebView2 sin marco (mismo enfoque que el vigía, reparto y folio) que se porta como los paneles
// de la bandeja de Windows: no va en la barra de tareas y se cierra al perder el foco. Cerrarla termina los procesos
// de WebView2 y devuelve la memoria: abierto pesa lo que pesa un navegador; cerrado, nada.
// Dónde nace la ventana y cómo se recuerda está en placement_windows.go.

import (
	"embed"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/jchv/go-webview2"
	"github.com/jchv/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
)

//go:embed ui/index.html ui/app.css ui/app.js
var uiFS embed.FS

var (
	gdi32            = windows.NewLazySystemDLL("gdi32.dll")
	pCreateSolidBrsh = gdi32.NewProc("CreateSolidBrush")
)

// panelState guarda el panel abierto. Sólo hay una ventana a la vez.
type panelState struct {
	mu             sync.Mutex
	open           bool
	wv             webview2.WebView
	hwnd           uintptr
	cerradoPorFoco time.Time
}

const panelBg = 0x000D0B0A // #0A0B0D en COLORREF (0x00BBGGRR)

// devtools (CUPO_DEVTOOLS=1, desarrollo): DevTools y el puerto de depuración prendidos, y el panel no se cierra al
// perder el foco (si no, inspeccionarlo lo cierra).
var devtools = os.Getenv("CUPO_DEVTOOLS") != ""

var (
	frameOnce    sync.Once
	framedProcCB uintptr
	origWndProc  uintptr
	panelBrush   uintptr
)

func setupPanelGlobals() {
	framedProcCB = windows.NewCallback(panelWndProc)
	panelBrush = call(pCreateSolidBrsh, panelBg)
}

func (a *App) panelAbierto() bool {
	a.panel.mu.Lock()
	defer a.panel.mu.Unlock()
	return a.panel.open
}

// openPanel abre el panel (o lo trae al frente si ya está abierto) y pide lo que tenga más de un minuto.
func (a *App) openPanel() {
	a.actualizar(edadPanel)
	a.panel.mu.Lock()
	if a.panel.open {
		wv, hwnd := a.panel.wv, a.panel.hwnd
		a.panel.mu.Unlock()
		if wv != nil {
			wv.Dispatch(func() { bringToFront(hwnd) })
		}
		return
	}
	a.panel.open = true
	a.panel.mu.Unlock()
	go a.runPanel()
}

// alternarPanel: el clic en el icono abre el panel o lo cierra. Si ese mismo clic le acaba de sacar el foco (y por
// eso se cerró), no se vuelve a abrir.
func (a *App) alternarPanel() {
	a.panel.mu.Lock()
	abierto, hwnd := a.panel.open, a.panel.hwnd
	recien := time.Since(a.panel.cerradoPorFoco) < 500*time.Millisecond
	a.panel.mu.Unlock()
	switch {
	case recien:
	case abierto && hwnd != 0:
		call(pPostMessageW, hwnd, wmClose, 0, 0)
	case !abierto:
		a.openPanel()
	}
}

// runPanel crea la ventana y bombea sus mensajes hasta que se cierra. Después devuelve la memoria.
func (a *App) runPanel() {
	// WebView2 exige un hilo COM de apartamento único (STA): este hilo de la ventana lo pide acá.
	runtime.LockOSThread()
	_ = windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED)
	defer func() {
		panelSaveOK = false
		panelApp = nil
		a.panel.mu.Lock()
		a.panel.open, a.panel.wv, a.panel.hwnd = false, nil, 0
		a.panel.mu.Unlock()
		liberar()
	}()
	frameOnce.Do(setupPanelGlobals)

	// Primero se decide la geometría (con el DPI del monitor que corresponde) y el hook la pone al nacer.
	place := a.panelPlacement()
	panelCreate = place
	panelApp = a
	origWndProc = 0
	installCBT()
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus: true,
		Debug:     devtools,
		DataPath:  a.paths.WebData,
		WindowOptions: webview2.WindowOptions{
			Title:  "cupo",
			Width:  uint(place.W),
			Height: uint(place.H),
			Center: false, // la posición la fija el hook CBT
		},
	})
	uninstallCBT()
	if w == nil {
		a.logf("no pude abrir el panel: falta WebView2 (Edge) en este equipo")
		return
	}
	hwnd := uintptr(w.Window())
	subclassFrameless(hwnd) // por si el hook no llegó a correr
	setupFrameless(hwnd)
	setDarkBackground(w)
	if place.Max {
		call(pShowWindow, hwnd, swMaximize)
	}
	bringToFront(hwnd)
	panelSaveOK = true // a partir de acá, mover, cambiar el tamaño o cerrar guarda la geometría

	// Pedidos de la UI. Son no bloqueantes.
	_ = w.Bind("ready", func() vista { return a.vista() })
	_ = w.Bind("act", func(accion string) { a.act(accion) })
	_ = w.Bind("win", func(cmd string) { w.Dispatch(func() { winCommand(hwnd, cmd) }) })

	a.panel.mu.Lock()
	a.panel.wv, a.panel.hwnd = w, hwnd
	a.panel.mu.Unlock()

	// Lo que cambia se empuja a la UI por evento (la página no consulta nada).
	unsub := a.subscribe(func() {
		b, _ := json.Marshal(a.vista())
		js := "window.cupo&&cupo.onState(" + string(b) + ")"
		w.Dispatch(func() { w.Eval(js) })
	})
	defer unsub()

	w.SetHtml(uiHTML())
	w.Run()
}

// uiHTML arma la página única: CSS y JS embebidos en el HTML (la ventana no sirve archivos).
// Con CUPO_UI=<carpeta> se leen de esa carpeta en vez del ejecutable: así se retoca el diseño sin recompilar.
func uiHTML() string {
	s := strings.Replace(string(readUI("index.html")), "<!--css-->", "<style>"+string(readUI("app.css"))+"</style>", 1)
	return strings.Replace(s, "<!--js-->", "<script>"+string(readUI("app.js"))+"</script>", 1)
}

func readUI(name string) []byte {
	if dir := os.Getenv("CUPO_UI"); dir != "" {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			return b
		}
	}
	b, _ := uiFS.ReadFile("ui/" + name)
	return b
}

// winCommand traduce los botones de la barra propia a órdenes de Win32 (en el hilo de la ventana).
func winCommand(hwnd uintptr, cmd string) {
	switch {
	case cmd == "close":
		call(pPostMessageW, hwnd, wmClose, 0, 0)
	case cmd == "drag":
		call(pReleaseCapture)
		call(pSendMessageW, hwnd, wmNCLButtonDown, htCaption, 0)
	case strings.HasPrefix(cmd, "resize:"):
		if code := resizeCode(strings.TrimPrefix(cmd, "resize:")); code != 0 {
			call(pReleaseCapture)
			call(pSendMessageW, hwnd, wmNCLButtonDown, code, 0)
		}
	}
}

// resizeCode traduce el borde o esquina a un código HT (HTLEFT..HTBOTTOMRIGHT).
func resizeCode(dir string) uintptr {
	return map[string]uintptr{"w": 10, "e": 11, "n": 12, "nw": 13, "ne": 14, "s": 15, "sw": 16, "se": 17}[dir]
}

// setupFrameless deja la ventana con fondo y marco oscuros y esquinas redondeadas.
func setupFrameless(hwnd uintptr) {
	setDWM(hwnd, dwmaDarkMode, 1)
	setDWM(hwnd, dwmaCornerPref, dwmwcpRound)
	setDWM(hwnd, dwmaBorderColor, panelBg)
	call(pSetClassLongPtrW, hwnd, gclpHbrBackground, panelBrush)
	call(pSetWindowPos, hwnd, 0, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoZOrder|swpFrameChanged)
}

func setDWM(hwnd uintptr, attr uint32, v uint32) {
	call(pDwmSetWindowAttribute, hwnd, uintptr(attr), uintptr(unsafe.Pointer(&v)), unsafe.Sizeof(v))
}

// panelWndProc es la subclase del panel. Sin marco, el área cliente ocupa toda la ventana; maximizada, se recorta el
// borde invisible. El mínimo depende del DPI, WM_DPICHANGED aplica el rectángulo que propone Windows, cada cambio
// de geometría se guarda y, al perder el foco, el panel se cierra.
func panelWndProc(hwnd, msg, wp, lp uintptr) uintptr {
	switch uint32(msg) {
	case wmActivate:
		if wp&0xFFFF == waInactive && panelSaveOK && !devtools {
			if a := panelApp; a != nil {
				a.panel.mu.Lock()
				a.panel.cerradoPorFoco = time.Now()
				a.panel.mu.Unlock()
			}
			call(pPostMessageW, hwnd, wmClose, 0, 0)
		}
	case wmNCCalcSize:
		if wp != 0 {
			if call(pIsZoomed, hwnd) != 0 {
				p := (*ncCalcSizeParams)(ptrFrom(lp))
				dpi := windowDPI(hwnd)
				f := sysMetricDPI(smCXSizeFrame, dpi) + sysMetricDPI(smCXPaddedBorder, dpi)
				p.Rgrc[0].Left += f
				p.Rgrc[0].Top += f
				p.Rgrc[0].Right -= f
				p.Rgrc[0].Bottom -= f
			}
			return 0
		}
	case wmGetMinMaxInfo:
		mw, mh := minSize(windowDPI(hwnd))
		mmi := (*minMaxInfo)(ptrFrom(lp))
		mmi.PtMinTrackSize = winPoint{X: mw, Y: mh}
		return 0
	case wmDpiChanged:
		rc := (*winRect)(ptrFrom(lp))
		call(pSetWindowPos, hwnd, 0, uintptr(rc.Left), uintptr(rc.Top),
			uintptr(rc.Right-rc.Left), uintptr(rc.Bottom-rc.Top), swpNoZOrder|swpNoActivateFlag)
		saveWindowState(hwnd)
		return 0
	case wmExitSizeMove, wmClose:
		saveWindowState(hwnd)
	case wmSize:
		if wp == sizeMaximized || wp == sizeRestored {
			saveWindowState(hwnd)
		}
	}
	return call(pCallWindowProcW, origWndProc, hwnd, msg, wp, lp)
}

// bringToFront restaura (si estaba minimizada) y trae la ventana al frente.
func bringToFront(hwnd uintptr) {
	if call(pIsIconic, hwnd) != 0 {
		call(pShowWindow, hwnd, swRestore)
	} else {
		call(pShowWindow, hwnd, swShow)
	}
	call(pSetForegroundWindow, hwnd)
}

// setDarkBackground pone el fondo de WebView2 en #0A0B0D para que no haya flash blanco al abrir.
// go-webview2 no expone el controlador, así que se llega por reflexión al campo browser (igual que el vigía).
// Si la librería cambia su interno, el recover evita romper la ventana.
func setDarkBackground(w webview2.WebView) {
	defer func() { _ = recover() }()
	v := reflect.ValueOf(w)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return
	}
	f := v.Elem().FieldByName("browser")
	if !f.IsValid() || !f.CanAddr() {
		return
	}
	f = reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
	chromium, ok := f.Interface().(*edge.Chromium)
	if !ok || chromium == nil {
		return
	}
	ctrl := chromium.GetController()
	if ctrl == nil {
		return
	}
	if ctrl2 := ctrl.GetICoreWebView2Controller2(); ctrl2 != nil {
		_ = ctrl2.PutDefaultBackgroundColor(edge.COREWEBVIEW2_COLOR{A: 255, R: 0x0A, G: 0x0B, B: 0x0D})
	}
}
