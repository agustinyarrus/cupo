//go:build ignore

// gen-icon.go: genera cupo.ico (16 a 256 px) para el exe: un cuadrado oscuro redondeado con los dos anillos de la
// bandeja (afuera verde, adentro ámbar), como un medidor. Lo usa build.ps1 -Icono (después rsrc lo embebe en el exe).
// Uso: go run gen-icon.go
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

var (
	fondo = color.NRGBA{R: 0x12, G: 0x13, B: 0x17, A: 0xff}
	pista = color.NRGBA{R: 0x2e, G: 0x31, B: 0x3a, A: 0xff}
	verde = color.NRGBA{R: 0x84, G: 0xd4, B: 0xab, A: 0xff}
	ambar = color.NRGBA{R: 0xf0, G: 0xc6, B: 0x74, A: 0xff}
)

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// sobre mezcla c con cobertura a (0..1) encima de lo que ya hay.
func sobre(img *image.NRGBA, x, y int, c color.NRGBA, a float64) {
	if a <= 0 {
		return
	}
	d := img.NRGBAAt(x, y)
	da := float64(d.A) / 255
	oa := a + da*(1-a)
	mix := func(s, t uint8) uint8 { return uint8((float64(s)*a + float64(t)*da*(1-a)) / oa) }
	img.SetNRGBA(x, y, color.NRGBA{R: mix(c.R, d.R), G: mix(c.G, d.G), B: mix(c.B, d.B), A: uint8(oa * 255)})
}

func iconPNG(size int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	s := float64(size)
	c := s / 2
	radio := s * 0.22 // esquinas del cuadrado
	ancho := math.Max(s*0.075, 1.2)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			qx := math.Max(math.Abs(px-c)-(c-radio), 0)
			qy := math.Max(math.Abs(py-c)-(c-radio), 0)
			sobre(img, x, y, fondo, clamp01(radio-math.Hypot(qx, qy)+0.5))
			d := math.Hypot(px-c, py-c)
			ang := math.Atan2(px-c, -(py - c))
			if ang < 0 {
				ang += 2 * math.Pi
			}
			for _, a := range []struct {
				r, parte float64
				col      color.NRGBA
			}{{s * 0.32, 0.62, verde}, {s * 0.18, 0.38, ambar}} {
				cov := clamp01(ancho/2 - math.Abs(d-a.r) + 0.5)
				sobre(img, x, y, pista, cov)
				sobre(img, x, y, a.col, cov*clamp01(math.Min(a.parte*2*math.Pi-ang, ang)*d+0.5))
			}
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func main() {
	sizes := []int{16, 20, 24, 32, 40, 48, 64, 128, 256}
	var pngs [][]byte
	for _, s := range sizes {
		pngs = append(pngs, iconPNG(s))
	}
	// ICO: cabecera (6 bytes), una entrada por tamaño (16 bytes) y los PNG (Windows Vista+ acepta PNG adentro).
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, []uint16{0, 1, uint16(len(sizes))})
	off := 6 + 16*len(sizes)
	for i, s := range sizes {
		w := byte(s % 256) // 256 se escribe como 0
		b.Write([]byte{w, w, 0, 0})
		_ = binary.Write(&b, binary.LittleEndian, []uint16{1, 32})
		_ = binary.Write(&b, binary.LittleEndian, []uint32{uint32(len(pngs[i])), uint32(off)})
		off += len(pngs[i])
	}
	for _, p := range pngs {
		b.Write(p)
	}
	if err := os.WriteFile("cupo.ico", b.Bytes(), 0o644); err != nil {
		panic(err)
	}
	_ = os.WriteFile("cupo-256.png", pngs[len(pngs)-1], 0o644) // para mirarlo
}
