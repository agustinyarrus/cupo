package main

// icons.go: el icono de la bandeja. Dos anillos finos que se llenan en sentido horario desde las 12: el de afuera es
// la sesión (o el primer medidor) y el de adentro la semana. El color dice cuánto queda, con los tonos del panel.
// Puro (sin Win32): se prueba con go test.

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

var (
	colorPista = color.NRGBA{R: 0x5a, G: 0x5f, B: 0x6b, A: 0xc0}
	colorNivel = []color.NRGBA{
		{R: 0x84, G: 0xd4, B: 0xab, A: 0xff}, // bajo: menos de 60 %
		{R: 0xf0, G: 0xc6, B: 0x74, A: 0xff}, // medio: menos de 85 %
		{R: 0xff, G: 0x8f, B: 0x7a, A: 0xff}, // alto: menos de 100 %
		{R: 0xff, G: 0x6b, B: 0x6b, A: 0xff}, // lleno
	}
)

// nivel: 0 bajo, 1 medio, 2 alto, 3 lleno. Los mismos cortes que el panel (app.js).
func nivel(pct float64) int {
	switch {
	case pct < 60:
		return 0
	case pct < 85:
		return 1
	case pct < 100:
		return 2
	}
	return 3
}

// anilloPNG dibuja el icono de n×n píxeles. Un % negativo es "sin dato": queda sólo la pista.
func anilloPNG(afuera, adentro float64, n int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	s := float64(n)
	ancho := math.Max(s*0.13, 1.6)
	for _, a := range []struct{ radio, pct float64 }{{s * 0.40, afuera}, {s * 0.19, adentro}} {
		arco(img, a.radio, ancho, a.pct)
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// arco pinta la pista de un anillo y, encima, la parte usada, con bordes suavizados.
func arco(img *image.NRGBA, radio, ancho, pct float64) {
	n := img.Bounds().Dx()
	c := float64(n) / 2
	lleno := math.Min(math.Max(pct, 0), 100) / 100 * 2 * math.Pi
	col := colorNivel[nivel(pct)]
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			dx, dy := float64(x)+0.5-c, float64(y)+0.5-c
			d := math.Hypot(dx, dy)
			radial := clamp01(ancho/2 - math.Abs(d-radio) + 0.5)
			if radial == 0 {
				continue
			}
			sobre(img, x, y, colorPista, radial)
			if pct <= 0 {
				continue
			}
			ang := math.Atan2(dx, -dy) // desde las 12, en sentido horario
			if ang < 0 {
				ang += 2 * math.Pi
			}
			parte := 1.0
			if pct < 100 {
				parte = clamp01(math.Min(lleno-ang, ang)*d + 0.5) // el borde del arco, suavizado en píxeles
			}
			sobre(img, x, y, col, radial*parte)
		}
	}
}

// sobre mezcla c con cobertura a (0..1) encima de lo que ya hay.
func sobre(img *image.NRGBA, x, y int, c color.NRGBA, a float64) {
	a *= float64(c.A) / 255
	if a <= 0 {
		return
	}
	d := img.NRGBAAt(x, y)
	da := float64(d.A) / 255
	oa := a + da*(1-a)
	mix := func(s, t uint8) uint8 { return uint8((float64(s)*a + float64(t)*da*(1-a)) / oa) }
	img.SetNRGBA(x, y, color.NRGBA{R: mix(c.R, d.R), G: mix(c.G, d.G), B: mix(c.B, d.B), A: uint8(oa*255 + 0.5)})
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }
