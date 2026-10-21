//go:build windows

package main

import (
	"fmt"
	"strings"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	trayClassName = "cupoTrayWindow"
	trayTitle     = "cupo"

	msgTrayIcon  = wmApp + 1 // callback del icono de la bandeja
	msgShowPanel = wmApp + 2 // otra instancia pide abrir el panel
	msgQuit      = wmApp + 3 // cerrar cupo (menú, panel u otra instancia con --quit)
	msgRefresh   = wmApp + 4 // repintar icono y tooltip

	cmdOpenPanel  = 101
	cmdActualizar = 102
	cmdQuit       = 103

	nimAdd    = 0x0
	nimModify = 0x1
	nimDelete = 0x2

	nifMessage = 0x01
	nifIcon    = 0x02
	nifTip     = 0x04
)

// notifyIconData es NOTIFYICONDATAW en su versión Vista+ (976 bytes en x64). Los campos "_" son la alineación.
type notifyIconData struct {
	CbSize          uint32
	_               uint32
	Wnd             uintptr
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	_               uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Timeout         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GuidItem        [16]byte
	BalloonIcon     uintptr
}

// Si el tamaño no es 976, este archivo no compila (evita un icono que Windows no entiende).
var _ [0]int = [unsafe.Sizeof(notifyIconData{}) - 976]int{}

type trayUI struct {
	app     *App
	hwnd    uintptr
	nid     notifyIconData
	icono   uintptr
	clave   string // los dos % del icono actual: si no cambian, no se rehace
	taskbar uint32
}

var (
	trayInst   *trayUI
	trayProcCB = windows.NewCallback(trayWndProc)
	trayHwnd   atomic.Uintptr // ventana de la bandeja: los otros hilos le piden cosas con PostMessage
)

// salir cierra cupo; es seguro desde cualquier hilo.
func (a *App) salir() {
	if h := trayHwnd.Load(); h != 0 {
		call(pPostMessageW, h, msgQuit, 0, 0)
	}
}

// trayWndProc atiende los mensajes de la ventana oculta de la bandeja (corre en el hilo principal).
func trayWndProc(hwnd, msg, wp, lp uintptr) uintptr {
	t := trayInst
	if t == nil {
		return call(pDefWindowProcW, hwnd, msg, wp, lp)
	}
	m := uint32(msg)
	switch {
	case m == msgTrayIcon:
		switch uint32(lp & 0xFFFF) {
		case wmLButtonUp:
			t.app.alternarPanel()
		case wmRButtonUp, wmContextMenu:
			t.showMenu()
		case wmMouseMove:
			t.app.alPasar()
		}
		return 0
	case m == msgShowPanel:
		t.app.openPanel()
		return 0
	case m == msgQuit:
		t.removeIcon()
		call(pPostQuitMessage, 0)
		return 0
	case m == msgRefresh:
		t.repaint()
		return 0
	case m == t.taskbar && t.taskbar != 0:
		t.addIcon() // Explorer se reinició y perdió el icono
		return 0
	case m == wmDestroy:
		call(pPostQuitMessage, 0)
		return 0
	}
	return call(pDefWindowProcW, hwnd, msg, wp, lp)
}

// runTray crea la ventana oculta y el icono, abre el panel y bombea mensajes hasta que cupo se cierra.
func runTray(app *App) error {
	t := &trayUI{app: app}
	trayInst = t
	t.taskbar = uint32(call(pRegisterWindowMessageW, uintptr(unsafe.Pointer(utf16Ptr("TaskbarCreated")))))

	var hinst windows.Handle
	if err := windows.GetModuleHandleEx(0, nil, &hinst); err != nil {
		return err
	}
	cls := utf16Ptr(trayClassName)
	wc := wndClassEx{Size: uint32(unsafe.Sizeof(wndClassEx{})), WndProc: trayProcCB, Instance: uintptr(hinst), ClassName: cls}
	if call(pRegisterClassExW, uintptr(unsafe.Pointer(&wc))) == 0 {
		return fmt.Errorf("RegisterClassExW falló")
	}
	// Ventana de nivel superior pero oculta: las de sólo mensajes no reciben TaskbarCreated al reiniciar Explorer.
	t.hwnd = call(pCreateWindowExW, 0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(utf16Ptr(trayTitle))),
		0, 0, 0, 0, 0, 0, 0, uintptr(hinst), 0)
	if t.hwnd == 0 {
		return fmt.Errorf("no pude crear la ventana de la bandeja")
	}
	trayHwnd.Store(t.hwnd)

	t.nid = notifyIconData{CbSize: uint32(unsafe.Sizeof(notifyIconData{})), Wnd: t.hwnd, ID: 1, CallbackMessage: msgTrayIcon}
	t.addIcon()
	app.subscribe(func() { call(pPostMessageW, t.hwnd, msgRefresh, 0, 0) })
	app.openPanel()

	var m winMsg
	for {
		if r := int32(call(pGetMessageW, uintptr(unsafe.Pointer(&m)), 0, 0, 0)); r <= 0 {
			break
		}
		call(pTranslateMessage, uintptr(unsafe.Pointer(&m)))
		call(pDispatchMessageW, uintptr(unsafe.Pointer(&m)))
	}
	t.removeIcon()
	return nil
}

// repaint refleja el estado en el icono (dos anillos: sesión y semana) y en el tooltip.
func (t *trayUI) repaint() {
	afuera, adentro, tip := t.app.bandeja()
	viejo := uintptr(0)
	if clave := fmt.Sprintf("%.0f/%.0f", afuera, adentro); clave != t.clave || t.icono == 0 {
		n := int(sysMetric(smCXSmIcon))
		viejo, t.icono, t.clave = t.icono, makeIcon(anilloPNG(afuera, adentro, n), n), clave
	}
	t.nid.Icon = t.icono
	t.nid.Flags = nifIcon | nifTip
	setUTF16(t.nid.Tip[:], tip)
	call(pShellNotifyIconW, nimModify, uintptr(unsafe.Pointer(&t.nid)))
	if viejo != 0 {
		call(pDestroyIcon, viejo)
	}
}

func (t *trayUI) addIcon() {
	t.nid.Flags = nifMessage | nifIcon | nifTip
	if call(pShellNotifyIconW, nimAdd, uintptr(unsafe.Pointer(&t.nid))) == 0 {
		t.app.logf("no pude agregar el icono a la bandeja todavía; se agrega cuando Explorer esté listo")
	}
	t.repaint()
}

func (t *trayUI) removeIcon() {
	call(pShellNotifyIconW, nimDelete, uintptr(unsafe.Pointer(&t.nid)))
}

// showMenu arma y muestra el menú contextual: el resumen (gris) y las acciones.
func (t *trayUI) showMenu() {
	_, _, tip := t.app.bandeja()
	menu := call(pCreatePopupMenu)
	if menu == 0 {
		return
	}
	defer call(pDestroyMenu, menu)
	if lineas := strings.Split(tip, "\n")[1:]; len(lineas) > 0 {
		for _, l := range lineas {
			appendMenu(menu, mfString|mfGrayed, 0, l)
		}
		appendMenu(menu, mfSeparator, 0, "")
	}
	appendMenu(menu, mfString, cmdOpenPanel, "Abrir panel")
	appendMenu(menu, mfString, cmdActualizar, "Actualizar ahora")
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, cmdQuit, "Salir")
	call(pSetMenuDefaultItem, menu, cmdOpenPanel, 0)

	var pt winPoint
	call(pGetCursorPos, uintptr(unsafe.Pointer(&pt)))
	call(pSetForegroundWindow, t.hwnd) // sin esto el menú no se cierra solo al hacer clic afuera
	cmd := call(pTrackPopupMenu, menu, tpmRightButton|tpmReturnCmd|tpmNonotify|tpmBottomAlign,
		uintptr(pt.X), uintptr(pt.Y), 0, t.hwnd, 0)
	call(pPostMessageW, t.hwnd, wmNull, 0, 0)

	switch cmd {
	case cmdOpenPanel:
		t.app.openPanel()
	case cmdActualizar:
		t.app.actualizar(0)
	case cmdQuit:
		t.app.salir()
	}
}

func appendMenu(menu uintptr, flags uintptr, id uintptr, text string) {
	var p uintptr
	if text != "" {
		p = uintptr(unsafe.Pointer(utf16Ptr(text)))
	}
	call(pAppendMenuW, menu, flags, id, p)
}

// setUTF16 copia s a dst como texto terminado en cero, truncado si no entra.
func setUTF16(dst []uint16, s string) {
	u, _ := windows.UTF16FromString(s)
	if len(u) > len(dst) {
		u = u[:len(dst)]
		u[len(dst)-1] = 0
	}
	copy(dst, u)
}

// makeIcon convierte un PNG en HICON (Windows Vista+ acepta PNG en CreateIconFromResourceEx).
func makeIcon(data []byte, size int) uintptr {
	if len(data) == 0 {
		return 0
	}
	return call(pCreateIconFromResourceEx, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)),
		1, 0x00030000, uintptr(size), uintptr(size), 0)
}

// findTray devuelve la ventana de la bandeja de la instancia que ya está corriendo (0 si no hay).
func findTray() uintptr {
	return call(pFindWindowW, uintptr(unsafe.Pointer(utf16Ptr(trayClassName))), 0)
}
