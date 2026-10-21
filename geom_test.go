package main

import "testing"

// Monitor 4K a 150 % (144 DPI): área de trabajo 3840×2088 (sin la barra de tareas).
var work4k = rect{L: 0, T: 0, R: 3840, B: 2088}

// Monitor de 1920×1040 a 100 %.
var work1080 = rect{L: 0, T: 0, R: 1920, B: 1040}

func TestDefaultGeomNearTrayAndScaled(t *testing.T) {
	x, y, w, h := defaultGeom(144, work4k)
	if w != 810 || h != 840 {
		t.Fatalf("a 150 %% quiero 810×840 físicos (540×560 lógicos), di %d×%d", w, h)
	}
	if x != 3840-810-18 || y != 2088-840-18 {
		t.Errorf("no nació abajo a la derecha, junto a la bandeja: x=%d y=%d", x, y)
	}
}

func TestDefaultGeomFitsSmallScreens(t *testing.T) {
	// 1024×600 a 100 %: la ventana no puede ser más grande que el 96 %×94 % del área.
	_, _, w, h := defaultGeom(96, rect{R: 1024, B: 600})
	if w > 1024*96/100 || h > 600*94/100 {
		t.Errorf("se sale de la pantalla chica: %d×%d", w, h)
	}
}

func TestFitGeomScalesWhenDPIChanges(t *testing.T) {
	// Guardada a 150 % (144) y abierta en un monitor a 100 % (96): el tamaño físico baja a dos tercios.
	g := winGeom{X: 100, Y: 100, W: 1710, H: 990, DPI: 144}
	_, _, w, h := fitGeom(g, 96, work1080)
	if w != 1140 || h != 660 {
		t.Errorf("escala de DPI mal: %d×%d, quiero 1140×660", w, h)
	}
}

func TestFitGeomKeepsSizeWhenDPISame(t *testing.T) {
	g := winGeom{X: 300, Y: 200, W: 1500, H: 900, DPI: 144}
	x, y, w, h := fitGeom(g, 144, work4k)
	if x != 300 || y != 200 || w != 1500 || h != 900 {
		t.Errorf("con el mismo DPI no debe tocar nada: %d,%d %d×%d", x, y, w, h)
	}
}

func TestFitGeomBringsOffscreenBackIn(t *testing.T) {
	// Guardada en un monitor que ya no existe: queda fuera del área de trabajo.
	g := winGeom{X: 5000, Y: -300, W: 800, H: 500, DPI: 96}
	x, y, w, h := fitGeom(g, 96, work1080)
	if x < 0 || y < 0 || x+w > 1920 || y+h > 1040 {
		t.Errorf("sigue fuera de pantalla: %d,%d %d×%d", x, y, w, h)
	}
}

func TestFitGeomClampsOversized(t *testing.T) {
	g := winGeom{X: 0, Y: 0, W: 4000, H: 3000, DPI: 96}
	_, _, w, h := fitGeom(g, 96, work1080)
	if w > 1920 || h > 1040 {
		t.Errorf("no achicó una ventana más grande que la pantalla: %d×%d", w, h)
	}
}

func TestMinSizeFollowsDPI(t *testing.T) {
	w, h := minSize(144)
	if w != 690 || h != 600 {
		t.Errorf("mínimo a 150 %% = %d×%d, quiero 690×600", w, h)
	}
	w, h = minSize(96)
	if w != 460 || h != 400 {
		t.Errorf("mínimo a 100 %% = %d×%d, quiero 460×400", w, h)
	}
}

func TestWinGeomValid(t *testing.T) {
	if (winGeom{W: 0, H: 0, DPI: 144}).valid() {
		t.Error("una geometría vacía no puede ser válida")
	}
	if (winGeom{W: 1710, H: 990, DPI: 0}).valid() {
		t.Error("sin DPI no puede ser válida")
	}
	if !(winGeom{W: 1710, H: 990, DPI: 144}).valid() {
		t.Error("una geometría normal debe ser válida")
	}
}

func TestFitGeomAgrandaHastaElMinimo(t *testing.T) {
	// guardada por la versión con las pestañas arriba (400 lógicos de ancho): no entra la lista de la izquierda
	g := winGeom{X: 3200, Y: 1200, W: 600, H: 840, DPI: 144}
	x, _, w, _ := fitGeom(g, 144, work4k)
	if w != 690 || x+w > 3840 {
		t.Errorf("quiero el mínimo de 690 px a 150 %% y adentro de la pantalla: x=%d w=%d", x, w)
	}
}
