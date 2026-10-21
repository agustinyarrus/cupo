package main

// claude.go: Claude. Los límites salen de api.anthropic.com/api/oauth/usage (el mismo que usa /usage), la cuenta de
// /api/oauth/profile y los tokens de lo que Claude Code anota en esta PC (~/.claude/projects/**/*.jsonl).

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

func leerClaude(l *lector) (ia, error) {
	var c struct {
		O struct {
			Token string `json:"accessToken"`
			Vence int64  `json:"expiresAt"`
			Plan  string `json:"subscriptionType"`
		} `json:"claudeAiOauth"`
	}
	if leerJSON(l.ruta(`.claude\.credentials.json`), &c) != nil || c.O.Token == "" {
		return ia{}, errSinCuenta
	}
	// Claude Code rota el token al renovarlo: renovarlo acá lo dejaría sin sesión.
	if c.O.Vence > 0 && c.O.Vence < l.ahora().UnixMilli() {
		return ia{}, errors.New("la sesión venció: Claude Code la renueva cuando lo usás")
	}
	hdr := map[string]string{"Authorization": "Bearer " + c.O.Token, "anthropic-beta": "oauth-2025-04-20"}
	b, err := l.pedir("GET", "https://api.anthropic.com/api/oauth/usage?cedar_ember=1", "", hdr)
	var eh errHTTP
	if errors.As(err, &eh) && (eh == 400 || eh == 403) { // cedar_ember (reinicios guardados) es opcional
		b, err = l.pedir("GET", "https://api.anthropic.com/api/oauth/usage", "", hdr)
	}
	if err != nil {
		return ia{}, err
	}
	x, err := parseClaude(b, l.extra("GET", "https://api.anthropic.com/api/oauth/profile", "", hdr), l.ahora())
	if err != nil {
		return ia{}, err
	}
	if x.Plan == "" {
		x.Plan = titulo(c.O.Plan)
	}
	x.Filas = append(x.Filas, l.claude.filas(l.ruta(`.claude\projects`), l.ahora())...)
	return x, nil
}

func parseClaude(b, perfil []byte, ahora time.Time) (ia, error) {
	type ventana struct {
		Pct    *float64 `json:"utilization"`
		Renova string   `json:"resets_at"`
	}
	var r struct {
		Sesion  *ventana `json:"five_hour"`
		Semana  *ventana `json:"seven_day"`
		Opus    *ventana `json:"seven_day_opus"`
		Sonnet  *ventana `json:"seven_day_sonnet"`
		Apps    *ventana `json:"seven_day_oauth_apps"`
		Rutinas *ventana `json:"seven_day_routines"`
		Cowork  *ventana `json:"seven_day_cowork"`
		Limites []struct {
			Tipo    string          `json:"kind"`
			Pct     *float64        `json:"percent"`
			Renova  string          `json:"resets_at"`
			Alcance json.RawMessage `json:"scope"`
			Activo  bool            `json:"is_active"`
		} `json:"limits"`
		Extra *struct {
			Prendido bool     `json:"is_enabled"`
			Tope     *float64 `json:"monthly_limit"`
			Usado    *float64 `json:"used_credits"`
			Pct      *float64 `json:"utilization"`
			Moneda   string   `json:"currency"`
			Decim    *int     `json:"decimal_places"`
			AlTope   bool     `json:"spend_limit_reached"`
		} `json:"extra_usage"`
		Nube *struct {
			Tope   *float64 `json:"limit_dollars"`
			Usado  *float64 `json:"used_dollars"`
			Queda  *float64 `json:"remaining_dollars"`
			Vence  string   `json:"resets_at"`
			Motivo *string  `json:"locked_reason"`
		} `json:"iguana_necktie"`
		Reinicios *struct {
			Puede  bool `json:"eligible"`
			Grants []struct {
				Quedan *int   `json:"resets_left"`
				Pausa  bool   `json:"paused"`
				Desde  string `json:"starts_at"`
				Hasta  string `json:"ends_at"`
			} `json:"grants"`
		} `json:"cedar_ember"`
		Parte *struct {
			Filas []struct {
				Nombre string  `json:"display_name"`
				Pct    float64 `json:"percent"`
			} `json:"rows"`
		} `json:"seven_day_breakdown"`
	}
	if json.Unmarshal(b, &r) != nil {
		return ia{}, errFormato
	}
	var x ia
	// El límite que más aprieta ahora (is_active) se marca en su tarjeta.
	activo := map[string]bool{}
	for _, lim := range r.Limites {
		if lim.Activo {
			activo[map[string]string{"session": "sesion", "weekly_all": "semana"}[lim.Tipo]] = true
		}
	}
	for _, v := range []struct {
		clave, nombre string
		w             *ventana
	}{{"sesion", "sesión · 5 h", r.Sesion}, {"semana", "semana", r.Semana},
		{"", "semana · Opus", r.Opus}, {"", "semana · Sonnet", r.Sonnet}, {"", "semana · apps conectadas", r.Apps},
		{"", "rutinas diarias", r.Rutinas}, {"", "semana · Cowork", r.Cowork}} {
		if v.w != nil && v.w.Pct != nil {
			f := medidor(v.nombre, *v.w.Pct, msDe(v.w.Renova))
			if activo[v.clave] && v.clave != "" {
				f.Nota = "el que más aprieta ahora"
			}
			x.Filas = append(x.Filas, f)
		}
	}
	// Límites semanales por modelo nuevos (weekly_scoped): el nombre del modelo viene en scope.
	for _, lim := range r.Limites {
		if lim.Tipo == "session" || lim.Tipo == "weekly_all" || lim.Pct == nil {
			continue
		}
		f := medidor("semana · "+alcance(lim.Alcance, lim.Tipo), *lim.Pct, msDe(lim.Renova))
		if lim.Activo {
			f.Nota = "el que más aprieta ahora"
		}
		x.Filas = append(x.Filas, f)
	}
	if len(x.Filas) == 0 {
		return ia{}, errFormato
	}
	if e := r.Extra; e != nil && e.Prendido {
		div := math.Pow(10, float64(2)) // shortcut: sin muestra de cómo viene prendido; se asume en centavos si no dice
		if e.Decim != nil {
			div = math.Pow(10, float64(*e.Decim))
		}
		m := monedaDe(strings.ToUpper(e.Moneda))
		if m == "" {
			m = "US$"
		}
		f := medidor("créditos extra · mes", ptrVal(e.Pct), 0)
		f.Grupo, f.Cuota = "créditos", true
		if e.Usado != nil && e.Tope != nil {
			f.Valor = dinero(*e.Usado/div, m) + " de " + dinero(*e.Tope/div, m)
		}
		if e.AlTope {
			f.Nota = "llegaste al tope de gasto"
		}
		x.Filas = append(x.Filas, f)
	}
	if n := r.Nube; n != nil && (n.Tope != nil || n.Queda != nil) {
		queda := ptrVal(n.Queda)
		if n.Queda == nil {
			queda = ptrVal(n.Tope) - ptrVal(n.Usado)
		}
		x.Filas = append(x.Filas, dato("créditos", "créditos de nube", dinero(queda, "US$")+cond(n.Tope != nil, " de "+dinero(ptrVal(n.Tope), "US$")),
			unir(cond(n.Vence != "", "vencen "+fecha(n.Vence)), cond(n.Motivo != nil && *n.Motivo != "", "bloqueados"))))
	}
	if g := r.Reinicios; g != nil && g.Puede {
		quedan, prox := 0, ""
		for _, k := range g.Grants {
			ini, fin := msDe(k.Desde), msDe(k.Hasta)
			if k.Pausa || k.Quedan == nil || ini > ahora.UnixMilli() || (fin > 0 && fin < ahora.UnixMilli()) {
				continue
			}
			quedan += *k.Quedan
			if k.Hasta != "" && (prox == "" || k.Hasta < prox) {
				prox = k.Hasta
			}
		}
		x.Filas = append(x.Filas, dato("créditos", "reinicios guardados", plural(quedan, "disponible", "disponibles"),
			cond(prox != "", "el primero vence el "+fecha(prox))))
	}
	if r.Parte != nil {
		for _, f := range r.Parte.Filas {
			if f.Pct > 0 {
				x.Filas = append(x.Filas, dato("reparto de la semana", f.Nombre, numero(f.Pct)+" %", ""))
			}
		}
	}
	perfilClaude(&x, perfil)
	return x, nil
}

// alcance saca el nombre del modelo de un límite con scope (puede venir como texto o como objeto).
func alcance(raw json.RawMessage, tipo string) string {
	var s string
	if json.Unmarshal(raw, &s) == nil && s != "" {
		return s
	}
	var o map[string]any
	if json.Unmarshal(raw, &o) == nil {
		for _, k := range []string{"display_name", "name", "model", "model_name"} {
			if v, ok := o[k].(string); ok && v != "" {
				return v
			}
		}
	}
	return strings.ReplaceAll(tipo, "_", " ")
}

// perfilClaude suma la cuenta: quién, plan, estado de la suscripción y desde cuándo.
func perfilClaude(x *ia, b []byte) {
	var p struct {
		Cuenta struct {
			Nombre string `json:"display_name"`
			Correo string `json:"email"`
			Creada string `json:"created_at"`
		} `json:"account"`
		Org struct {
			Tipo     string  `json:"organization_type"`
			Factura  string  `json:"billing_type"`
			Nivel    string  `json:"rate_limit_tier"`
			Estado   string  `json:"subscription_status"`
			Desde    string  `json:"subscription_created_at"`
			Extra    bool    `json:"has_extra_usage_enabled"`
			PruebaCC *string `json:"claude_code_trial_ends_at"`
		} `json:"organization"`
	}
	if b == nil || json.Unmarshal(b, &p) != nil {
		return
	}
	x.Cuenta = unir(p.Cuenta.Nombre, p.Cuenta.Correo)
	plan := map[string]string{"claude_pro": "Pro", "claude_max": "Max", "claude_team": "Team", "claude_enterprise": "Enterprise", "claude_free": "Free"}[p.Org.Tipo]
	if plan == "Max" {
		for _, m := range []string{"20x", "5x"} {
			if strings.Contains(p.Org.Nivel, m) {
				plan += " " + m
			}
		}
	}
	x.Plan = plan
	estados := map[string]string{"active": "activa", "trialing": "en prueba", "past_due": "pago atrasado", "canceled": "cancelada", "unpaid": "impaga", "paused": "pausada"}
	if p.Org.Estado != "active" {
		x.Estado = estados[p.Org.Estado]
	}
	x.Alerta = x.Alerta || p.Org.Estado == "past_due" || p.Org.Estado == "unpaid"
	factura := map[string]string{"stripe_subscription": "suscripción con tarjeta", "apple_subscription": "App Store", "google_play_subscription": "Google Play"}[p.Org.Factura]
	if factura == "" {
		factura = p.Org.Factura
	}
	extra := "apagados"
	if p.Org.Extra {
		extra = "prendidos"
	}
	prueba := ""
	if p.Org.PruebaCC != nil && *p.Org.PruebaCC != "" {
		prueba = "hasta el " + fecha(*p.Org.PruebaCC)
	}
	nivel := map[string]string{"default_claude_ai": "estándar", "default_claude_max_5x": "Max 5x", "default_claude_max_20x": "Max 20x"}[p.Org.Nivel]
	if nivel == "" {
		nivel = p.Org.Nivel
	}
	const g = "cuenta"
	x.agregar(
		dato(g, "suscripción", unir(estados[p.Org.Estado], cond(p.Org.Desde != "", "desde el "+fecha(p.Org.Desde))), ""),
		dato(g, "facturación", factura, ""),
		dato(g, "nivel de límites", nivel, ""),
		dato(g, "créditos extra", extra, ""),
		dato(g, "prueba de Claude Code", prueba, ""),
		dato(g, "cuenta creada", fecha(p.Cuenta.Creada), ""))
}

func ptrVal(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// ---- lo que Claude Code anotó en esta PC --------------------------------------------------------

// claudeUso lee las transcripciones de Claude Code de los últimos 7 días. De cada archivo se recuerda hasta dónde se
// leyó: la primera vez se recorre todo (unos segundos con cientos de MB) y después sólo lo que se agregó.
type claudeUso struct {
	mu       sync.Mutex
	archivos map[string]*marca
	dias     map[string]map[string]*tokens // "2006-01-02" -> modelo -> tokens
}

type marca struct {
	off int64
	ult string // el último mensaje contado: Claude Code escribe una línea por bloque, todas con el mismo uso
}

type tokens struct {
	Respuestas, Entrada, Salida, Razon, CacheL, CacheE, Busquedas int64
}

func (t *tokens) total() int64 { return t.Entrada + t.Salida + t.CacheL + t.CacheE }

func (t *tokens) sumar(o *tokens) {
	t.Respuestas += o.Respuestas
	t.Entrada += o.Entrada
	t.Salida += o.Salida
	t.Razon += o.Razon
	t.CacheL += o.CacheL
	t.CacheE += o.CacheE
	t.Busquedas += o.Busquedas
}

const diasLocales = 7

// leer recorre la carpeta y suma lo nuevo de cada archivo tocado en la última semana.
func (u *claudeUso) leer(raiz string, ahora time.Time) {
	if u.archivos == nil {
		u.archivos, u.dias = map[string]*marca{}, map[string]map[string]*tokens{}
	}
	desde := ahora.AddDate(0, 0, -(diasLocales - 1))
	primerDia := time.Date(desde.Year(), desde.Month(), desde.Day(), 0, 0, 0, 0, desde.Location())
	_ = filepath.WalkDir(raiz, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.ModTime().Before(primerDia) {
			return nil
		}
		m := u.archivos[p]
		if m == nil {
			m = &marca{}
			u.archivos[p] = m
		}
		if info.Size() < m.off { // se reescribió (no pasa con Claude Code, que sólo agrega): se lee de nuevo
			*m = marca{}
		}
		if info.Size() > m.off {
			u.leerArchivo(p, m, primerDia.Format("2006-01-02"))
		}
		return nil
	})
	for dia := range u.dias {
		if dia < primerDia.Format("2006-01-02") {
			delete(u.dias, dia)
		}
	}
}

func (u *claudeUso) leerArchivo(p string, m *marca, primerDia string) {
	f, err := os.Open(p)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.Seek(m.off, io.SeekStart); err != nil {
		return
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 256<<10), 64<<20)
	sc.Split(lineasEnteras)
	for sc.Scan() {
		linea := sc.Bytes()
		m.off += int64(len(linea)) + 1
		// filtro barato antes de decodificar: la mayoría de las líneas (resultados de herramientas) no son respuestas
		if !bytes.Contains(linea, []byte(`"output_tokens"`)) || !bytes.Contains(linea, []byte(`"type":"assistant"`)) {
			continue
		}
		var r struct {
			Tipo   string    `json:"type"`
			Hora   time.Time `json:"timestamp"`
			Pedido string    `json:"requestId"`
			M      struct {
				ID     string `json:"id"`
				Modelo string `json:"model"`
				U      *struct {
					Entrada int64 `json:"input_tokens"`
					Salida  int64 `json:"output_tokens"`
					CacheE  int64 `json:"cache_creation_input_tokens"`
					CacheL  int64 `json:"cache_read_input_tokens"`
					Det     struct {
						Razon int64 `json:"thinking_tokens"`
					} `json:"output_tokens_details"`
					Srv struct {
						Web int64 `json:"web_search_requests"`
					} `json:"server_tool_use"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(linea, &r) != nil || r.Tipo != "assistant" || r.M.U == nil || r.M.Modelo == "<synthetic>" {
			continue
		}
		if clave := r.M.ID + "|" + r.Pedido; clave == m.ult {
			continue
		} else {
			m.ult = clave
		}
		dia := r.Hora.Local().Format("2006-01-02")
		if dia < primerDia {
			continue
		}
		if u.dias[dia] == nil {
			u.dias[dia] = map[string]*tokens{}
		}
		t := u.dias[dia][r.M.Modelo]
		if t == nil {
			t = &tokens{}
			u.dias[dia][r.M.Modelo] = t
		}
		x := r.M.U
		t.sumar(&tokens{Respuestas: 1, Entrada: x.Entrada, Salida: x.Salida, Razon: x.Det.Razon, CacheL: x.CacheL, CacheE: x.CacheE, Busquedas: x.Srv.Web})
	}
	if errors.Is(sc.Err(), bufio.ErrTooLong) { // una línea de más de 64 MB: se saltea el resto de ese archivo por ahora
		if info, err := f.Stat(); err == nil {
			m.off = info.Size()
		}
	}
}

// lineasEnteras corta en "\n" y deja sin leer una última línea a medio escribir (se lee en la vuelta siguiente).
func lineasEnteras(data []byte, _ bool) (int, []byte, error) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, data[:i], nil
	}
	return 0, nil, nil
}

// filas lee lo nuevo y arma la sección "Claude Code en esta PC": hoy, la semana, un gráfico por día y los modelos.
func (u *claudeUso) filas(raiz string, ahora time.Time) []fila {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.leer(raiz, ahora)
	if len(u.dias) == 0 {
		return nil
	}
	const g = "Claude Code en esta PC"
	var hoy, semana tokens
	porDia := map[string]float64{}
	porModelo := map[string]*tokens{}
	for dia, ms := range u.dias {
		for modelo, t := range ms {
			semana.sumar(t)
			porDia[dia] += float64(t.total())
			if dia == ahora.Format("2006-01-02") {
				hoy.sumar(t)
			}
			if porModelo[modelo] == nil {
				porModelo[modelo] = &tokens{}
			}
			porModelo[modelo].sumar(t)
		}
	}
	detalle := func(t tokens) string {
		return unir(plural(int(t.Respuestas), "respuesta", "respuestas"), "entrada "+cantidad(float64(t.Entrada)),
			"salida "+cantidad(float64(t.Salida))+cond(t.Razon > 0, " (razonamiento "+cantidad(float64(t.Razon))+")"),
			"caché leída "+cantidad(float64(t.CacheL)), "caché escrita "+cantidad(float64(t.CacheE)),
			cond(t.Busquedas > 0, plural(int(t.Busquedas), "búsqueda web", "búsquedas web")))
	}
	serie, desde := serieDias(porDia, ahora, diasLocales)
	out := []fila{
		{Grupo: g, Nombre: "hoy", Valor: cantidad(float64(hoy.total())) + " tokens", Nota: detalle(hoy)},
		{Grupo: g, Nombre: "7 días", Valor: cantidad(float64(semana.total())) + " tokens", Nota: detalle(semana)},
		{Grupo: g, Nombre: "tokens por día", Serie: serie, Desde: desde},
	}
	modelos := make([]string, 0, len(porModelo))
	for m := range porModelo {
		modelos = append(modelos, m)
	}
	sort.Slice(modelos, func(i, j int) bool { return porModelo[modelos[i]].total() > porModelo[modelos[j]].total() })
	for _, m := range modelos {
		t := porModelo[m]
		out = append(out, dato("por modelo · 7 días", modeloLindo(m), cantidad(float64(t.total()))+" tokens",
			unir(plural(int(t.Respuestas), "respuesta", "respuestas"), "salida "+cantidad(float64(t.Salida)))))
	}
	return out
}

var reVersion = regexp.MustCompile(`(\d+)-(\d+)`)

// modeloLindo: "claude-opus-5-5" -> "Opus 5.5"; "claude-sonnet-4-5-20250929" -> "Sonnet 4.5".
func modeloLindo(m string) string {
	m = strings.TrimPrefix(m, "claude-")
	if i := strings.LastIndexByte(m, '-'); i > 0 && len(m)-i == 9 {
		m = m[:i] // fecha al final
	}
	m = reVersion.ReplaceAllString(m, "$1.$2")
	return titulo(strings.ReplaceAll(m, "-", " "))
}
