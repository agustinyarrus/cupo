package main

// geom.go: geometría de la ventana del panel. Pura (sin Win32) para probarla con go test. Todo va en
// píxeles FÍSICOS; los tamaños "lógicos" se escalan con el DPI del monitor donde cae la ventana.

// winGeom es la ventana guardada. DPI es el de la ventana cuando se guardó. Si Max está activo, X/Y/W/H
// son la posición normal (la de antes de maximizar), no el tamaño maximizado.
type winGeom struct {
	X   int32 `json:"x"`
	Y   int32 `json:"y"`
	W   int32 `json:"w"`
	H   int32 `json:"h"`
	DPI int32 `json:"dpi"`
	Max bool  `json:"max"`
}

// rect es un rectángulo plano: izquierda, arriba, derecha y abajo.
type rect struct{ L, T, R, B int32 }

// Tamaños lógicos (a 100 %, 96 DPI) del panel.
const (
	panelWantW = 540 // tamaño inicial: la lista de pestañas (152) y una columna de 388, como los paneles de la bandeja
	panelWantH = 560
	panelMinW  = 460 // mínimo: por debajo, el diseño no entra
	panelMinH  = 400
	panelMarg  = 12 // separación del borde del área de trabajo al nacer junto a la bandeja
)

// valid dice si una geometría guardada sirve. Descarta restos rotos (tamaños de cero, DPI absurdo).
func (g winGeom) valid() bool {
	return g.W >= 300 && g.H >= 200 && g.DPI >= 48 && g.DPI <= 480
}

// scaled convierte un tamaño lógico (a 96 DPI) en píxeles para un DPI dado.
func scaled(v, dpi int32) int32 {
	return int32(int64(v) * int64(dpi) / 96)
}

// minSize es el mínimo de la ventana para un DPI dado.
func minSize(dpi int32) (int32, int32) {
	return scaled(panelMinW, dpi), scaled(panelMinH, dpi)
}

// defaultGeom es el tamaño inicial: abajo a la derecha del área de trabajo, junto a la bandeja, y nunca más del
// 96 % x 94 % de ella.
func defaultGeom(dpi int32, work rect) (x, y, w, h int32) {
	w = scaled(panelWantW, dpi)
	h = scaled(panelWantH, dpi)
	aw, ah := work.R-work.L, work.B-work.T
	if mw := aw * 96 / 100; w > mw {
		w = mw
	}
	if mh := ah * 94 / 100; h > mh {
		h = mh
	}
	m := scaled(panelMarg, dpi)
	x = max(work.R-w-m, work.L)
	y = max(work.B-h-m, work.T)
	return x, y, w, h
}

// fitGeom reubica una geometría guardada en un monitor con DPI monitorDPI y área de trabajo work. Si el DPI
// cambió (la ventana se guardó en otro monitor), escala el tamaño. La agranda hasta el mínimo (una guardada por una
// versión con otro diseño puede ser más chica), la achica si no entra y la mete entera dentro del área de trabajo,
// para que nunca quede tapada por la barra ni fuera de la pantalla.
func fitGeom(g winGeom, monitorDPI int32, work rect) (x, y, w, h int32) {
	w, h = g.W, g.H
	if g.DPI > 0 && monitorDPI > 0 && monitorDPI != g.DPI {
		w = int32(int64(g.W) * int64(monitorDPI) / int64(g.DPI))
		h = int32(int64(g.H) * int64(monitorDPI) / int64(g.DPI))
	}
	if monitorDPI > 0 {
		mw, mh := minSize(monitorDPI)
		w, h = max(w, mw), max(h, mh)
	}
	if aw := work.R - work.L; w > aw {
		w = aw
	}
	if ah := work.B - work.T; h > ah {
		h = ah
	}
	x, y = g.X, g.Y
	if x < work.L {
		x = work.L
	}
	if y < work.T {
		y = work.T
	}
	if x+w > work.R {
		x = work.R - w
	}
	if y+h > work.B {
		y = work.B - h
	}
	return x, y, w, h
}
