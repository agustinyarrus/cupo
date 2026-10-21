//go:build windows

// cupo: cuánto te queda de cada IA, en la bandeja de Windows y en un panel oscuro con una pestaña por IA.
// Hecho para no gastar: nada consulta en segundo plano. Los datos se piden al abrir el panel, al pasar el mouse por
// el icono, un rato después de que una IA se usa (avisa el sistema de archivos) y cuando vence una ventana de uso.
// El panel se cierra al perder el foco y libera toda su memoria.
//
// Uso:
//
//	cupo.exe          abre la bandeja y el panel
//	cupo.exe --quit   cierra la instancia que está corriendo
//
// Una sola instancia: si ya corre, la segunda sólo le pide que abra el panel o que se cierre.
package main

import (
	"flag"
	"runtime"

	"golang.org/x/sys/windows"
)

const singletonName = `Local\cupo-bandeja`

func main() {
	runtime.LockOSThread() // la bandeja vive en el hilo principal: ahí corre su bucle de mensajes
	quit := flag.Bool("quit", false, "cierra la instancia que está corriendo")
	flag.Parse()

	mutex, err := windows.CreateMutex(nil, false, utf16Ptr(singletonName))
	if err == windows.ERROR_ALREADY_EXISTS {
		if hwnd := findTray(); hwnd != 0 {
			msg := uintptr(msgShowPanel)
			if *quit {
				msg = msgQuit
			}
			call(pAllowSetForegroundWindow, asfwAny) // que la instancia que corre pueda traer el panel al frente
			call(pPostMessageW, hwnd, msg, 0, 0)
		}
		return
	}
	if mutex != 0 {
		defer windows.CloseHandle(mutex)
	}
	if *quit {
		return // no hay nada que cerrar
	}

	setDPIAware()
	app := newApp(resolvePaths())
	app.start()
	if err := runTray(app); err != nil {
		app.logf("la bandeja no arrancó: %v", err)
	}
}
