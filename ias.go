package main

// ias.go: el modelo de datos de cada IA, el registro de proveedores y lo que comparten sus lectores. Cada lector usa
// la credencial que su CLI ya dejó en esta PC y le pregunta sólo a su propio servidor; si la IA no tiene servidor de
// uso, lee lo que su CLI anota. Nunca renueva un token que la CLI rota al renovarlo (Claude, Codex, Grok): si venció,
// queda lo último que se supo. Sin Win32: se prueba con go test. Los lectores están en claude.go, codex.go, agy.go
// y otras.go; qué endpoint da qué está documentado en README.md.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// fila es una línea de una pestaña. Con Pct es un medidor (una tarjeta con barra); con Card, un valor destacado
// (un saldo); con Serie, un gráfico de barras por día; si no, un dato (nombre, valor y nota). Grupo es la sección.
type fila struct {
	Grupo   string    `json:"grupo,omitempty"`
	Nombre  string    `json:"nombre"`
	Pct     *float64  `json:"pct,omitempty"`     // % usado, 0..100
	Renueva int64     `json:"renueva,omitempty"` // cuándo se renueva la ventana, ms Unix
	Valor   string    `json:"valor,omitempty"`
	Nota    string    `json:"nota,omitempty"`
	Apagado bool      `json:"apagado,omitempty"` // la ventana no aplica ahora
	Cuota   bool      `json:"cuota,omitempty"`   // medidor que no es una ventana de uso: no va al icono ni al tooltip
	Card    bool      `json:"card,omitempty"`
	Serie   []float64 `json:"serie,omitempty"` // un valor por día, del más viejo al de hoy
	Desde   int64     `json:"desde,omitempty"` // ms: el día del primer valor de la serie
}

// ia es lo que se sabe de una IA.
type ia struct {
	ID       string `json:"id"`
	Nombre   string `json:"nombre"`
	Plan     string `json:"plan,omitempty"`
	Estado   string `json:"estado,omitempty"` // en prueba, suspendida, terminada…
	Cuenta   string `json:"cuenta,omitempty"` // quién: nombre · correo
	Filas    []fila `json:"filas"`
	Nota     string `json:"nota,omitempty"`
	Alerta   bool   `json:"alerta,omitempty"`   // no queda saldo, la cuenta está suspendida o se terminó el plan
	Cuando   int64  `json:"cuando,omitempty"`   // ms: de cuándo son estos datos
	Error    string `json:"error,omitempty"`    // el último pedido falló: los datos pueden ser viejos
	Cargando bool   `json:"cargando,omitempty"` // hay un pedido en camino
}

type proveedor struct {
	id, nombre string
	vigilar    []string // carpetas (relativas al perfil) que la IA escribe cuando se usa
	leer       func(*lector) (ia, error)
}

// El orden es el de las pestañas.
var proveedores = []proveedor{
	{"claude", "Claude", []string{`.claude\projects`}, leerClaude},
	{"codex", "Codex", []string{`.codex\sessions`}, leerCodex},
	{"agy", "Antigravity", []string{`.gemini\antigravity-cli\conversations`}, leerAgy},
	{"grok", "Grok", []string{`.grok\sessions`}, leerGrok},
	{"kimi", "Kimi", []string{`.kimi\sessions`}, leerKimi},
	{"deepseek", "DeepSeek", []string{`.deepcode\projects`}, leerDeepSeek},
	{"qwen", "Qwen", []string{`.qwen\usage`}, leerQwen},
	{"you", "You.com", nil, leerYou},
	{"mistral", "Mistral", nil, leerMistral},
	{"copilot", "Copilot", []string{`.copilot\session-state`}, leerCopilot},
	{"zai", "Z.ai", nil, leerZai},
}

func proveedorDe(id string) (proveedor, bool) {
	for _, p := range proveedores {
		if p.id == id {
			return p, true
		}
	}
	return proveedor{}, false
}

var (
	errSinCuenta = errors.New("no hay una cuenta en esta PC") // la pestaña no se muestra
	errEspera    = errors.New("el servidor pidió esperar unos minutos")
	errFormato   = errors.New("el servidor respondió algo que no entiendo")
)

// errHTTP es una respuesta con un código que no es 2xx.
type errHTTP int

func (e errHTTP) Error() string {
	if e == http.StatusUnauthorized || e == http.StatusForbidden {
		return fmt.Sprintf("el servidor rechazó la sesión (%d)", int(e))
	}
	return fmt.Sprintf("el servidor respondió %d", int(e))
}

// lector junta lo que necesitan los lectores; los campos de función se reemplazan en las pruebas.
type lector struct {
	http  *http.Client
	home  string
	ahora func() time.Time
	env   func(string) string
	cred  func(target string) (string, error)

	agy    agyEstado // token de Antigravity renovado acá, su cliente OAuth y lo que Agy le pasó a su statusline (agy.go)
	claude claudeUso // lo que Claude Code anotó en esta PC, leído de a poco (claude.go)
	codex  codexUso  // lo mismo para Codex (codex.go)
}

func nuevoLector(cred func(string) (string, error)) *lector {
	home, _ := os.UserHomeDir()
	return &lector{
		// Sin conexiones abiertas entre pedidos: cada IA es otro servidor y en reposo no queda nada vivo.
		http: &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment, DisableKeepAlives: true,
			TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second,
		}},
		home:  home,
		ahora: time.Now,
		env:   os.Getenv,
		cred:  cred,
	}
}

func (l *lector) ruta(rel string) string { return filepath.Join(l.home, rel) }

// pedir hace un pedido HTTPS y devuelve el cuerpo. Los errores no llevan la URL ni encabezados (ahí van claves).
func (l *lector) pedir(metodo, u string, cuerpo string, hdr map[string]string) ([]byte, error) {
	var body io.Reader
	if cuerpo != "" {
		body = strings.NewReader(cuerpo)
	}
	req, err := http.NewRequest(metodo, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "cupo/1")
	req.Header.Set("Accept", "application/json")
	if cuerpo != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := l.http.Do(req)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return nil, errors.New("el servidor no respondió a tiempo")
		}
		return nil, errors.New("sin conexión")
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	switch {
	case res.StatusCode == http.StatusTooManyRequests:
		return nil, errEspera
	case res.StatusCode/100 != 2:
		return nil, errHTTP(res.StatusCode)
	}
	return b, nil
}

// extra pide un dato que suma detalle pero no es imprescindible: si falla, la pestaña sale igual sin él.
func (l *lector) extra(metodo, u, cuerpo string, hdr map[string]string) []byte {
	b, err := l.pedir(metodo, u, cuerpo, hdr)
	if err != nil {
		return nil
	}
	return b
}

// ---- armado de filas ---------------------------------------------------------------------------

func medidor(nombre string, pct float64, renueva int64) fila {
	p := math.Max(0, math.Min(100, pct))
	return fila{Nombre: nombre, Pct: &p, Renueva: renueva}
}

func dato(grupo, nombre, valor, nota string) fila {
	return fila{Grupo: grupo, Nombre: nombre, Valor: valor, Nota: nota}
}

// agregar suma filas a la pestaña; un dato sin valor no se muestra.
func (x *ia) agregar(fs ...fila) {
	for _, f := range fs {
		if f.Pct != nil || f.Serie != nil || f.Valor != "" {
			x.Filas = append(x.Filas, f)
		}
	}
}

// serieDias arma los últimos n días hasta hoy (inclusive) con los valores de por (clave "2006-01-02").
func serieDias(por map[string]float64, hoy time.Time, n int) (serie []float64, desde int64) {
	d0 := time.Date(hoy.Year(), hoy.Month(), hoy.Day(), 0, 0, 0, 0, hoy.Location()).AddDate(0, 0, -(n - 1))
	for i := 0; i < n; i++ {
		serie = append(serie, por[d0.AddDate(0, 0, i).Format("2006-01-02")])
	}
	return serie, d0.UnixMilli()
}

// ---- ayudas ------------------------------------------------------------------------------------

func leerJSON(ruta string, v any) error {
	b, err := os.ReadFile(ruta)
	if err != nil {
		return err
	}
	return json.Unmarshal(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), v)
}

// sinComentarios saca las líneas "// ..." (Copilot las pone arriba de su config.json).
func sinComentarios(b []byte) []byte {
	var out [][]byte
	for _, l := range bytes.Split(b, []byte("\n")) {
		if !bytes.HasPrefix(bytes.TrimSpace(l), []byte("//")) {
			out = append(out, l)
		}
	}
	return bytes.Join(out, []byte("\n"))
}

// jwtCampos lee el contenido de un JWT sin validarlo: sólo para saber cuándo vence, quién es o con qué cliente se
// emitió.
func jwtCampos(t string, v any) bool {
	p := strings.Split(t, ".")
	if len(p) != 3 {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(p[1], "="))
	return err == nil && json.Unmarshal(b, v) == nil
}

func msDe(s string) int64 {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UnixMilli()
	}
	return 0
}

// fecha: "20 oct 2026" (para datos de cuenta, no para relojes).
func fecha(s string) string {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		if t, err = time.Parse("2006-01-02", s); err != nil {
			return ""
		}
	}
	meses := []string{"ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sep", "oct", "nov", "dic"}
	t = t.Local()
	return fmt.Sprintf("%d %s %d", t.Day(), meses[t.Month()-1], t.Year())
}

func hostDe(u string) string {
	if p, err := url.Parse(u); err == nil {
		return p.Host
	}
	return ""
}

func titulo(s string) string {
	if s == "" {
		return ""
	}
	r := []rune(strings.ToLower(s))
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// separarPalabras: "GrokBuild" -> "Grok Build".
func separarPalabras(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && unicode.IsUpper(r) {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func unir(partes ...string) string {
	var p []string
	for _, s := range partes {
		if s != "" {
			p = append(p, s)
		}
	}
	return strings.Join(p, " · ")
}

func siNo(b bool) string {
	if b {
		return "sí"
	}
	return "no"
}

func aNum(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func monedaDe(c string) string {
	switch c {
	case "USD":
		return "US$"
	case "CNY":
		return "¥"
	}
	return c
}

// numero: 98 · 2,5 · 1.234 (enteros sin decimales, si no uno).
func numero(v float64) string {
	if v == math.Trunc(v) {
		return miles(int64(v))
	}
	return strings.Replace(strconv.FormatFloat(v, 'f', 1, 64), ".", ",", 1)
}

// dinero en formato argentino, con dos decimales: US$ 1,43 · US$ -0,19 · US$ 1.234,50.
func dinero(v float64, moneda string) string {
	c := int64(math.Round(math.Abs(v) * 100))
	s := fmt.Sprintf("%s,%02d", miles(c/100), c%100)
	if v < 0 && c != 0 {
		s = "-" + s
	}
	return moneda + " " + s
}

func miles(n int64) string {
	if n < 0 {
		return "-" + miles(-n)
	}
	s := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// cantidad: 950 · 2,3 k · 268 k · 1,4 M · 991 M.
func cantidad(n float64) string {
	for _, u := range []struct {
		div float64
		suf string
	}{{1e9, " G"}, {1e6, " M"}, {1e3, " k"}} {
		if n >= u.div {
			v := n / u.div
			if v < 10 {
				return strings.Replace(strconv.FormatFloat(v, 'f', 1, 64), ".", ",", 1) + u.suf
			}
			return strconv.FormatFloat(v, 'f', 0, 64) + u.suf
		}
	}
	return strconv.FormatFloat(n, 'f', 0, 64)
}

func plural(n int, uno, varios string) string {
	if n == 1 {
		return "1 " + uno
	}
	return miles(int64(n)) + " " + varios
}

// duracion: "1 h 27 min" · "45 min" · "3 d 4 h".
func duracion(seg int64) string {
	d, h, m := seg/86400, seg%86400/3600, seg%3600/60
	switch {
	case d > 0:
		return unirEsp(fmt.Sprintf("%d d", d), cond(h > 0, fmt.Sprintf("%d h", h)))
	case h > 0:
		return unirEsp(fmt.Sprintf("%d h", h), cond(m > 0, fmt.Sprintf("%d min", m)))
	}
	return fmt.Sprintf("%d min", max(m, 1))
}

func cond(ok bool, s string) string {
	if ok {
		return s
	}
	return ""
}

func unirEsp(a, b string) string { return strings.TrimSpace(a + " " + b) }

// ventanaNombre: 5 h · día · semana · 30 días · 12 h
func ventanaNombre(seg int64) string {
	switch {
	case seg == 7*86400:
		return "semana"
	case seg == 86400:
		return "día"
	case seg > 0 && seg%86400 == 0:
		return strconv.FormatInt(seg/86400, 10) + " días"
	case seg > 0 && seg%3600 == 0:
		return strconv.FormatInt(seg/3600, 10) + " h"
	}
	return "ventana"
}
