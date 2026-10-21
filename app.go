//go:build windows

package main

// App: lo que se sabe de cada IA y cuándo se pide. Reglas:
//   - Nada consulta en segundo plano. Se pide al arrancar, al abrir el panel, al pasar el mouse por el icono, un rato
//     después de que una IA escribe en su carpeta (se está usando: avisa el sistema, sin consultar nada) y cuando
//     vence una de sus ventanas de uso (un reloj de un solo disparo por IA).
//   - Cada IA se pide en paralelo y aparece apenas responde. Si falla, queda lo último que se supo.
//   - Después de cada tanda se devuelve la memoria: en reposo el proceso queda en un par de MB.
//   - Nunca se avisa a los suscriptores con el mutex tomado: pueden volver a leer el estado.

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

const (
	edadPanel   = time.Minute      // al abrir el panel se pide lo que tenga más de esto
	edadMouse   = 2 * time.Minute  // al pasar el mouse por el icono
	trasUso     = 2 * time.Minute  // mientras una IA se usa, se la pide a lo sumo así de seguido
	minTrasUso  = 20 * time.Second // y nunca enseguida: que termine lo que está haciendo
	espera429   = 5 * time.Minute  // si el servidor pide esperar
	trasRenovar = 15 * time.Second // después de que vence una ventana, para que el servidor ya la dé renovada
)

type agenda struct {
	cuando time.Time
	t      *time.Timer
}

type App struct {
	paths paths
	lect  *lector

	mu       sync.Mutex
	set      settings
	ias      map[string]*ia
	ocultas  map[string]bool // sin cuenta en esta PC
	enCamino map[string]bool
	intento  map[string]time.Time // último pedido de cada IA
	noAntes  map[string]time.Time // el servidor pidió esperar
	reloj    map[string]*agenda   // próximo pedido agendado de cada IA
	enCurso  int
	ultima   int64 // ms: cuándo terminó la última tanda
	subs     map[int]func()
	nextSub  int

	logMu sync.Mutex
	panel panelState
}

func newApp(p paths) *App {
	a := &App{paths: p, ias: map[string]*ia{}, ocultas: map[string]bool{}, enCamino: map[string]bool{},
		intento: map[string]time.Time{}, noAntes: map[string]time.Time{}, reloj: map[string]*agenda{}, subs: map[int]func(){}}
	cargar(p.Settings, &a.set)
	var c cache
	cargar(p.Cache, &c)
	for id, x := range c.IAs {
		if _, ok := proveedorDe(id); ok && x != nil {
			a.ias[id] = x
		}
	}
	a.lect = nuevoLector(leerCred)
	a.lect.agy.cliente, a.lect.agy.sl = c.AgyCliente, c.AgySL
	return a
}

// start empieza a escuchar las carpetas de las IAs y pide todo una vez.
func (a *App) start() {
	a.logf("cupo arrancó")
	a.vigilar()
	a.actualizar(0)
}

// ---- pedidos -----------------------------------------------------------------------------------

// actualizar pide las IAs indicadas (todas si no se indica ninguna) cuyo último pedido tenga más de edad. Las que
// ya están en camino o tienen que esperar (429) se saltean.
func (a *App) actualizar(edad time.Duration, ids ...string) {
	if len(ids) == 0 {
		for _, p := range proveedores {
			ids = append(ids, p.id)
		}
	}
	ahora := time.Now()
	var van []proveedor
	a.mu.Lock()
	for _, id := range ids {
		p, ok := proveedorDe(id)
		if !ok || a.enCamino[id] || ahora.Sub(a.intento[id]) < edad || ahora.Before(a.noAntes[id]) {
			continue
		}
		a.enCamino[id], a.intento[id] = true, ahora
		van = append(van, p)
	}
	a.enCurso += len(van)
	a.mu.Unlock()
	if len(van) == 0 {
		return
	}
	a.emit()
	for _, p := range van {
		go a.leerUna(p)
	}
}

// leerUna pide una IA y guarda lo que llegó. Al terminar la tanda, guarda el cache y devuelve la memoria.
func (a *App) leerUna(p proveedor) {
	r, err := p.leer(a.lect)
	ahora := time.Now()
	a.mu.Lock()
	delete(a.enCamino, p.id)
	a.enCurso--
	fin := a.enCurso == 0
	switch {
	case err == nil:
		r.ID, r.Nombre, r.Cuando = p.id, p.nombre, ahora.UnixMilli()
		a.ias[p.id] = &r
		delete(a.ocultas, p.id)
	case errors.Is(err, errSinCuenta):
		delete(a.ias, p.id)
		a.ocultas[p.id] = true
	default:
		x := a.ias[p.id]
		if x == nil {
			x = &ia{ID: p.id, Nombre: p.nombre}
			a.ias[p.id] = x
		}
		x.Error = err.Error()
		if errors.Is(err, errEspera) {
			a.noAntes[p.id] = ahora.Add(espera429)
		}
	}
	if fin {
		a.ultima = ahora.UnixMilli()
	}
	a.mu.Unlock()
	if err == nil {
		a.agendarRenovacion(p.id, r.Filas)
	} else if !errors.Is(err, errSinCuenta) {
		a.logf("%s: %v", p.nombre, err)
	}
	a.emit()
	if fin {
		a.guardarCache()
		if !a.panelAbierto() {
			liberar()
		}
	}
}

// agendar pide una IA dentro de d. Si ya hay un pedido agendado antes, queda ése.
func (a *App) agendar(id string, d time.Duration) {
	cuando := time.Now().Add(d)
	a.mu.Lock()
	defer a.mu.Unlock()
	if g, ok := a.reloj[id]; ok {
		if !g.cuando.After(cuando) {
			return
		}
		g.t.Stop()
	}
	g := &agenda{cuando: cuando}
	a.reloj[id] = g
	g.t = time.AfterFunc(d, func() {
		a.mu.Lock()
		vale := a.reloj[id] == g
		if vale {
			delete(a.reloj, id)
		}
		a.mu.Unlock()
		if vale {
			a.actualizar(0, id)
		}
	})
}

// actividad: una IA escribió en su carpeta, o sea que se está usando. Se la pide un rato después y, mientras se
// siga usando, a lo sumo cada trasUso.
func (a *App) actividad(id string) {
	a.mu.Lock()
	d := trasUso - time.Since(a.intento[id])
	a.mu.Unlock()
	a.agendar(id, max(d, minTrasUso))
}

// agendarRenovacion: cuando vence la primera ventana con uso, se vuelve a pedir (el icono y el tooltip bajan solos).
// Una ventana sin uso no se agenda: renovarse no cambia nada.
func (a *App) agendarRenovacion(id string, filas []fila) {
	ahora := time.Now().UnixMilli()
	var prox int64
	for _, f := range filas {
		if f.Pct != nil && *f.Pct > 0 && !f.Apagado && f.Renueva > ahora && (prox == 0 || f.Renueva < prox) {
			prox = f.Renueva
		}
	}
	if prox > 0 {
		a.agendar(id, time.Duration(prox-ahora)*time.Millisecond+trasRenovar)
	}
}

// alPasar: el mouse pasó por el icono; si los datos tienen unos minutos, se piden de nuevo.
func (a *App) alPasar() { a.actualizar(edadMouse) }

// vigilar: el sistema avisa cuando una IA escribe en su carpeta. El hilo duerme en el kernel hasta que pasa algo.
func (a *App) vigilar() {
	var hs []windows.Handle
	var ids []string
	mirar := func(dir string, sub bool, id string) {
		h, err := windows.FindFirstChangeNotification(dir, sub, windows.FILE_NOTIFY_CHANGE_FILE_NAME|windows.FILE_NOTIFY_CHANGE_LAST_WRITE)
		if err == nil { // si la carpeta no existe, esa IA no se usa en esta PC
			hs, ids = append(hs, h), append(ids, id)
		}
	}
	for _, p := range proveedores {
		for _, rel := range p.vigilar {
			mirar(a.lect.ruta(rel), true, p.id)
		}
	}
	// El cache de la statusline (Claude Code y Agy lo reescriben al dibujar): de ahí sale el balde de Argon.
	mirar(dirStatusline(a.lect.env, a.lect.home), false, "statusline")
	go func() {
		for len(hs) > 0 {
			ev, err := windows.WaitForMultipleObjects(hs, false, windows.INFINITE)
			i := int(ev - windows.WAIT_OBJECT_0)
			if err != nil || i < 0 || i >= len(hs) {
				a.logf("dejé de escuchar las carpetas de las IAs: %v", err)
				return
			}
			switch ids[i] {
			case "statusline": // casi siempre es Claude Code; sólo cuenta si lo escribió Agy
				if a.lect.statuslineAgy() {
					a.actividad("agy")
				}
			default:
				a.actividad(ids[i])
			}
			if windows.FindNextChangeNotification(hs[i]) != nil { // la carpeta desapareció: se deja de mirar
				windows.FindCloseChangeNotification(hs[i])
				hs, ids = append(hs[:i], hs[i+1:]...), append(ids[:i], ids[i+1:]...)
			}
		}
	}()
}

// ---- estado para la bandeja y el panel ---------------------------------------------------------

type vista struct {
	IAs    []ia  `json:"ias"`
	Ultima int64 `json:"ultima"` // ms: cuándo terminó la última tanda
	Faltan int   `json:"faltan"` // pedidos en camino
}

func (a *App) vista() vista {
	a.mu.Lock()
	defer a.mu.Unlock()
	v := vista{IAs: []ia{}, Ultima: a.ultima, Faltan: a.enCurso}
	for _, p := range proveedores {
		switch x := a.ias[p.id]; {
		case x != nil:
			c := *x // las filas no se modifican nunca en el lugar: se pueden compartir
			c.Cargando = a.enCamino[p.id]
			v.IAs = append(v.IAs, c)
		case a.enCamino[p.id] && !a.ocultas[p.id]:
			v.IAs = append(v.IAs, ia{ID: p.id, Nombre: p.nombre, Cargando: true})
		}
	}
	return v
}

// vigente: el % usado de un medidor, contando con que la ventana ya se haya renovado.
func vigente(f fila, ahora int64) float64 {
	if f.Renueva > 0 && f.Renueva <= ahora {
		return 0
	}
	return *f.Pct
}

// bandeja: los dos medidores del icono (las dos primeras ventanas de uso de Claude o, si no hay, de la primera IA que
// tenga; -1 si no hay) y el tooltip: la ventana más usada de cada IA. Las cuotas (créditos, tokens) no cuentan.
func (a *App) bandeja() (afuera, adentro float64, tip string) {
	afuera, adentro = -1, -1
	ahora := time.Now().UnixMilli()
	lineas := []string{"cupo"}
	for _, x := range a.vista().IAs {
		var pcts []float64
		for _, f := range x.Filas {
			if f.Pct != nil && !f.Apagado && !f.Cuota {
				pcts = append(pcts, vigente(f, ahora))
			}
		}
		if len(pcts) == 0 {
			continue
		}
		if afuera < 0 {
			afuera = pcts[0]
			if len(pcts) > 1 {
				adentro = pcts[1]
			}
		}
		lineas = append(lineas, fmt.Sprintf("%s %s %%", x.Nombre, numero(math.Round(maxDe(pcts)))))
	}
	return afuera, adentro, strings.Join(lineas, "\n")
}

func maxDe(v []float64) float64 {
	m := v[0]
	for _, x := range v[1:] {
		m = max(m, x)
	}
	return m
}

// ---- pedidos del panel -------------------------------------------------------------------------

func (a *App) act(accion string) {
	switch accion {
	case "actualizar":
		a.actualizar(0)
	case "salir":
		a.salir()
	}
}

// ---- eventos -----------------------------------------------------------------------------------

// subscribe registra un suscriptor (no debe bloquear). Devuelve la función para darse de baja.
func (a *App) subscribe(fn func()) func() {
	a.mu.Lock()
	id := a.nextSub
	a.nextSub++
	a.subs[id] = fn
	a.mu.Unlock()
	return func() {
		a.mu.Lock()
		delete(a.subs, id)
		a.mu.Unlock()
	}
}

func (a *App) emit() {
	a.mu.Lock()
	fns := make([]func(), 0, len(a.subs))
	for _, f := range a.subs {
		fns = append(fns, f)
	}
	a.mu.Unlock()
	for _, f := range fns {
		f()
	}
}

// ---- disco y memoria ---------------------------------------------------------------------------

// guardarCache escribe lo último que llegó bien de cada IA (sin los errores del momento) y lo de Agy.
func (a *App) guardarCache() {
	a.mu.Lock()
	c := cache{IAs: map[string]*ia{}}
	for id, x := range a.ias {
		if x.Cuando > 0 {
			y := *x
			y.Error, y.Cargando = "", false
			c.IAs[id] = &y
		}
	}
	a.mu.Unlock()
	a.lect.agy.mu.Lock() // nunca con a.mu tomado: renovarAgy toma este primero
	c.AgyCliente, c.AgySL = a.lect.agy.cliente, a.lect.agy.sl
	a.lect.agy.mu.Unlock()
	if err := guardar(a.paths.Cache, c); err != nil {
		a.logf("no pude guardar el cache: %v", err)
	}
}

// liberar devuelve al sistema la memoria de una tanda de trabajo y saca del working set las páginas que no se usan:
// en reposo, el proceso queda en un par de MB.
func liberar() {
	debug.FreeOSMemory()
	_ = windows.SetProcessWorkingSetSizeEx(windows.CurrentProcess(), ^uintptr(0), ^uintptr(0), 0)
}

// logf agrega una línea a cupo.log. Si pasa de 1 MB, lo rota a cupo.log.1. Nunca lleva tokens ni claves.
func (a *App) logf(format string, args ...any) {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(a.paths.Log), 0o755); err != nil {
		return
	}
	if fi, err := os.Stat(a.paths.Log); err == nil && fi.Size() > 1<<20 {
		_ = os.Rename(a.paths.Log, a.paths.Log+".1")
	}
	f, err := os.OpenFile(a.paths.Log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s  %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
}

// savedWindow devuelve una copia de la geometría guardada del panel (nil si todavía no hay ninguna).
func (a *App) savedWindow() *winGeom {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.set.Window == nil {
		return nil
	}
	g := *a.set.Window
	return &g
}

// saveWindow guarda la geometría del panel. Se llama al mover, cambiar de tamaño o cerrar la ventana.
func (a *App) saveWindow(g winGeom) {
	a.mu.Lock()
	a.set.Window = &g
	s := a.set
	a.mu.Unlock()
	if err := guardar(a.paths.Settings, s); err != nil {
		a.logf("no pude guardar la ventana: %v", err)
	}
}
