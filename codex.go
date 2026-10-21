package main

// codex.go: Codex con la cuenta de ChatGPT. Límites de chatgpt.com/backend-api/wham/usage, reinicios guardados de
// wham/rate-limit-reset-credits y las estadísticas de la cuenta (tokens por día, rachas, hilos) de wham/profiles/me.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

func leerCodex(l *lector) (ia, error) {
	var a struct {
		T *struct {
			Token  string `json:"access_token"`
			Cuenta string `json:"account_id"`
		} `json:"tokens"`
	}
	// Con una API key en vez de la cuenta de ChatGPT no hay cupo que mirar.
	if leerJSON(l.ruta(`.codex\auth.json`), &a) != nil || a.T == nil || a.T.Token == "" {
		return ia{}, errSinCuenta
	}
	var jwt struct {
		Exp int64 `json:"exp"`
	}
	if jwtCampos(a.T.Token, &jwt) && jwt.Exp > 0 && jwt.Exp < l.ahora().Unix() {
		return ia{}, errors.New("la sesión venció: abrí Codex para renovarla")
	}
	hdr := map[string]string{"Authorization": "Bearer " + a.T.Token, "ChatGPT-Account-Id": a.T.Cuenta, "User-Agent": "codex_cli_rs"}
	const base = "https://chatgpt.com/backend-api/wham/"
	b, err := l.pedir("GET", base+"usage", "", hdr)
	if err != nil {
		return ia{}, err
	}
	x, err := parseCodex(b, l.extra("GET", base+"rate-limit-reset-credits", "", hdr), l.extra("GET", base+"profiles/me", "", hdr), l.ahora())
	x.Filas = append(x.Filas, l.codex.filas(l.ruta(`.codex\sessions`), l.ahora())...)
	return x, err
}

// ---- lo que Codex anotó en esta PC --------------------------------------------------------------

// codexUso lee las sesiones de Codex de los últimos 7 días (~/.codex/sessions/AAAA/MM/DD/*.jsonl, de la CLI y de la
// app). Cada sesión anota su total acumulado de tokens; de cada archivo se lee sólo lo que se agregó.
type codexUso struct {
	mu       sync.Mutex
	archivos map[string]*sesionCodex
}

type sesionCodex struct {
	off     int64
	dia     string // shortcut: la sesión entera cuenta el día en que empezó (su carpeta); repartirla por día pide sumar cada pedido
	modelo  string
	t       tokensCodex
	pedidos int
}

type tokensCodex struct {
	Entrada int64 `json:"input_tokens"` // incluye la caché
	Cache   int64 `json:"cached_input_tokens"`
	Salida  int64 `json:"output_tokens"` // incluye el razonamiento
	Razon   int64 `json:"reasoning_output_tokens"`
	Total   int64 `json:"total_tokens"`
}

func (t *tokensCodex) sumar(o tokensCodex) {
	t.Entrada += o.Entrada
	t.Cache += o.Cache
	t.Salida += o.Salida
	t.Razon += o.Razon
	t.Total += o.Total
}

func (u *codexUso) filas(raiz string, ahora time.Time) []fila {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.archivos == nil {
		u.archivos = map[string]*sesionCodex{}
	}
	primer := ahora.AddDate(0, 0, -(diasLocales - 1)).Format("2006-01-02")
	for i := 0; i < diasLocales; i++ {
		d := ahora.AddDate(0, 0, -i)
		dir := filepath.Join(raiz, d.Format("2006"), d.Format("01"), d.Format("02"))
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			p := filepath.Join(dir, e.Name())
			s := u.archivos[p]
			if s == nil {
				s = &sesionCodex{dia: d.Format("2006-01-02")}
				u.archivos[p] = s
			}
			if info, err := e.Info(); err == nil && info.Size() > s.off {
				s.leer(p)
			}
		}
	}
	for p, s := range u.archivos {
		if s.dia < primer {
			delete(u.archivos, p)
		}
	}
	var hoy, semana tokensCodex
	pedHoy, pedSemana := 0, 0
	porDia := map[string]float64{}
	porModelo := map[string]*tokensCodex{}
	pedModelo := map[string]int{}
	for _, s := range u.archivos {
		semana.sumar(s.t)
		pedSemana += s.pedidos
		porDia[s.dia] += float64(s.t.Total)
		if s.dia == ahora.Format("2006-01-02") {
			hoy.sumar(s.t)
			pedHoy += s.pedidos
		}
		m := s.modelo
		if m == "" {
			m = "sin modelo"
		}
		if porModelo[m] == nil {
			porModelo[m] = &tokensCodex{}
		}
		porModelo[m].sumar(s.t)
		pedModelo[m] += s.pedidos
	}
	if pedSemana == 0 {
		return nil
	}
	detalle := func(t tokensCodex, n int) string {
		return unir(plural(n, "pedido", "pedidos"), "entrada "+cantidad(float64(t.Entrada))+cond(t.Cache > 0, " (caché "+cantidad(float64(t.Cache))+")"),
			"salida "+cantidad(float64(t.Salida))+cond(t.Razon > 0, " (razonamiento "+cantidad(float64(t.Razon))+")"))
	}
	const g = "Codex en esta PC"
	serie, desde := serieDias(porDia, ahora, diasLocales)
	out := []fila{
		{Grupo: g, Nombre: "hoy", Valor: cantidad(float64(hoy.Total)) + " tokens", Nota: detalle(hoy, pedHoy)},
		{Grupo: g, Nombre: "7 días", Valor: cantidad(float64(semana.Total)) + " tokens", Nota: detalle(semana, pedSemana)},
		{Grupo: g, Nombre: "tokens por día", Serie: serie, Desde: desde, Nota: "cada sesión cuenta el día en que empezó"},
	}
	modelos := make([]string, 0, len(porModelo))
	for m := range porModelo {
		modelos = append(modelos, m)
	}
	sort.Slice(modelos, func(i, j int) bool { return porModelo[modelos[i]].Total > porModelo[modelos[j]].Total })
	for _, m := range modelos {
		out = append(out, dato("por modelo · 7 días", m, cantidad(float64(porModelo[m].Total))+" tokens",
			unir(plural(pedModelo[m], "pedido", "pedidos"), "salida "+cantidad(float64(porModelo[m].Salida)))))
	}
	return out
}

func (s *sesionCodex) leer(p string) {
	f, err := os.Open(p)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.Seek(s.off, io.SeekStart); err != nil {
		return
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 256<<10), 64<<20)
	sc.Split(lineasEnteras)
	for sc.Scan() {
		l := sc.Bytes()
		s.off += int64(len(l)) + 1
		switch {
		case bytes.Contains(l, []byte(`"token_count"`)):
			var r struct {
				P struct {
					Info *struct {
						T *tokensCodex `json:"total_token_usage"`
					} `json:"info"`
				} `json:"payload"`
			}
			if json.Unmarshal(l, &r) == nil && r.P.Info != nil && r.P.Info.T != nil {
				s.t = *r.P.Info.T // es el acumulado de la sesión
				s.pedidos++
			}
		case bytes.Contains(l, []byte(`"turn_context"`)):
			var r struct {
				P struct {
					Modelo string `json:"model"`
				} `json:"payload"`
			}
			if json.Unmarshal(l, &r) == nil && r.P.Modelo != "" {
				s.modelo = r.P.Modelo
			}
		}
	}
	if errors.Is(sc.Err(), bufio.ErrTooLong) { // una línea de más de 64 MB: se saltea el resto por ahora
		if info, err := f.Stat(); err == nil {
			s.off = info.Size()
		}
	}
}

type ventanaCodex struct {
	Pct    float64 `json:"used_percent"`
	Largo  int64   `json:"limit_window_seconds"`
	Renova int64   `json:"reset_at"`
}

type limiteCodex struct {
	Uno     *ventanaCodex `json:"primary_window"`
	Dos     *ventanaCodex `json:"secondary_window"`
	AlTope  bool          `json:"limit_reached"`
	Permite *bool         `json:"allowed"`
}

func parseCodex(b, reinicios, perfil []byte, ahora time.Time) (ia, error) {
	var r struct {
		Correo   string       `json:"email"`
		Plan     string       `json:"plan_type"`
		Limite   *limiteCodex `json:"rate_limit"`
		Revision *limiteCodex `json:"code_review_rate_limit"`
		Otros    []struct {
			Nombre  string       `json:"limit_name"`
			Funcion string       `json:"metered_feature"`
			Limite  *limiteCodex `json:"rate_limit"`
		} `json:"additional_rate_limits"`
		Creditos *struct {
			Tiene    bool `json:"has_credits"`
			Libre    bool `json:"unlimited"`
			AlTope   bool `json:"overage_limit_reached"`
			Saldo    any  `json:"balance"`
			MsjLocal any  `json:"approx_local_messages"`
			MsjNube  any  `json:"approx_cloud_messages"`
		} `json:"credits"`
		Gasto *struct {
			AlTope bool `json:"reached"`
		} `json:"spend_control"`
		TopeTipo *string `json:"rate_limit_reached_type"`
	}
	if json.Unmarshal(b, &r) != nil || r.Limite == nil {
		return ia{}, errFormato
	}
	x := ia{Plan: titulo(r.Plan), Cuenta: r.Correo}
	ventanas := func(grupo, pre string, lim *limiteCodex) {
		if lim == nil {
			return
		}
		for _, v := range []*ventanaCodex{lim.Uno, lim.Dos} {
			if v != nil {
				f := medidor(pre+ventanaNombre(v.Largo), v.Pct, v.Renova*1000)
				f.Grupo = grupo
				x.Filas = append(x.Filas, f)
			}
		}
	}
	ventanas("", "", r.Limite)
	for _, o := range r.Otros {
		nombre := o.Nombre
		if nombre == "" {
			nombre = o.Funcion
		}
		ventanas("por modelo", nombre+" · ", o.Limite)
	}
	ventanas("revisión de código", "revisiones · ", r.Revision)
	if r.Limite.AlTope || (r.TopeTipo != nil && *r.TopeTipo != "") {
		x.Alerta, x.Nota = true, "Llegaste al tope: Codex no responde hasta que se renueve la ventana."
	}
	if c := r.Creditos; c != nil {
		const g = "créditos"
		switch {
		case c.Libre:
			x.agregar(dato(g, "créditos", "ilimitados", ""))
		case c.Tiene:
			x.agregar(dato(g, "saldo", texto(c.Saldo), unir(cond(texto(c.MsjLocal) != "", "alcanza para ~"+texto(c.MsjLocal)+" mensajes locales"),
				cond(texto(c.MsjNube) != "", "~"+texto(c.MsjNube)+" en la nube"))))
		default:
			x.agregar(dato(g, "créditos", "sin créditos", "el plan no tiene créditos comprados"))
		}
		if c.AlTope || (r.Gasto != nil && r.Gasto.AlTope) {
			x.agregar(dato(g, "tope de gasto", "alcanzado", ""))
		}
	}
	var rc struct {
		Disponibles *int `json:"available_count"`
		Ganados     *int `json:"total_earned_count"`
	}
	if reinicios != nil && json.Unmarshal(reinicios, &rc) == nil && rc.Disponibles != nil {
		x.agregar(dato("créditos", "reinicios guardados", plural(*rc.Disponibles, "disponible", "disponibles"),
			cond(rc.Ganados != nil, fmt.Sprintf("%d ganados en total", ptrInt(rc.Ganados)))))
	}
	perfilCodex(&x, perfil, ahora)
	return x, nil
}

// perfilCodex suma las estadísticas de la cuenta (todas las superficies: CLI, app y web) y los tokens por día.
func perfilCodex(x *ia, b []byte, ahora time.Time) {
	var p struct {
		P struct {
			Usuario string `json:"username"`
			Nombre  string `json:"display_name"`
		} `json:"profile"`
		S *struct {
			Total     float64 `json:"lifetime_tokens"`
			Pico      float64 `json:"peak_daily_tokens"`
			Racha     int     `json:"current_streak_days"`
			RachaMax  int     `json:"longest_streak_days"`
			Hilos     int     `json:"total_threads"`
			TurnoMax  int64   `json:"longest_running_turn_sec"`
			Rapido    float64 `json:"fast_mode_usage_percentage"`
			Skills    int     `json:"total_skills_used"`
			SkillsUni int     `json:"unique_skills_used"`
			Effort    string  `json:"most_used_reasoning_effort"`
			EffortPct float64 `json:"most_used_reasoning_effort_percentage"`
			Dias      []struct {
				Dia    string  `json:"start_date"`
				Tokens float64 `json:"tokens"`
			} `json:"daily_usage_buckets"`
		} `json:"stats"`
	}
	if b == nil || json.Unmarshal(b, &p) != nil {
		return
	}
	if p.P.Nombre != "" {
		x.Cuenta = unir(p.P.Nombre, x.Cuenta)
	}
	s := p.S
	if s == nil {
		return
	}
	porDia := map[string]float64{}
	for _, d := range s.Dias {
		porDia[d.Dia] = d.Tokens
	}
	serie, desde := serieDias(porDia, ahora, 30)
	mes := 0.0
	for _, v := range serie {
		mes += v
	}
	const g = "estadísticas de la cuenta"
	x.agregar(
		fila{Grupo: g, Nombre: "tokens por día · 30 días", Serie: serie, Desde: desde, Valor: cantidad(mes) + " tokens", Nota: "en todas las superficies (CLI, app y web), según OpenAI"},
		dato(g, "tokens de por vida", cantidad(s.Total), cond(s.Pico > 0, "pico de un día: "+cantidad(s.Pico))),
		dato(g, "racha", plural(s.Racha, "día seguido", "días seguidos"), "la más larga: "+plural(s.RachaMax, "día", "días")),
		dato(g, "hilos", miles(int64(s.Hilos)), cond(s.TurnoMax > 0, "el turno más largo duró "+duracion(s.TurnoMax))),
		dato(g, "modo rápido", numero(roundTo(s.Rapido, 1))+" % del uso", ""),
		dato(g, "razonamiento más usado", s.Effort, cond(s.EffortPct > 0, numero(roundTo(s.EffortPct, 0))+" % de los turnos")),
		dato(g, "skills", plural(s.Skills, "uso", "usos"), cond(s.SkillsUni > 0, plural(s.SkillsUni, "skill distinta", "skills distintas"))),
	)
}

// texto muestra un número o texto que vino como JSON (los créditos llegan como número o como texto).
func texto(v any) string {
	switch t := v.(type) {
	case float64:
		return numero(t)
	case string:
		if f := aNum(t); f != 0 || t == "0" {
			return numero(f)
		}
		return strings.TrimSpace(t)
	}
	return ""
}

func ptrInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func roundTo(v float64, dec int) float64 {
	m := 1.0
	for i := 0; i < dec; i++ {
		m *= 10
	}
	return float64(int64(v*m+0.5)) / m
}
