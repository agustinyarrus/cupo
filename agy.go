package main

// agy.go: Antigravity (Agy). Las cuotas por grupo salen de cloudcode-pa…/v1internal:retrieveUserQuotaSummary, el plan
// de loadCodeAssist y la cuota de cada modelo de fetchAvailableModels. Agy guarda su sesión de Google en el
// Administrador de credenciales y la renueva cada hora mientras corre. Google no rota el refresh token, así que acá se
// puede renovar en memoria sin molestar a Agy (no se escribe nada de vuelta).
//
// La API no publica todos los baldes que ve Agy: el semanal de Gemini 4 Argon (gemini-argon-wtu-weekly) sólo llega en
// el payload que Agy le pasa a su statusline, y sl.exe guarda ese payload en %TEMP%\sl\quota.cache cada vez que
// dibuja (lo comparte con Claude Code: queda el último que dibujó). cupo escucha esa carpeta y se queda con lo de Agy.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
)

type agyEstado struct {
	mu      sync.Mutex
	tok     string    // token renovado acá (sólo en memoria)
	vence   time.Time //
	cliente string    // secreto del cliente OAuth de Agy, sacado de agy.exe (ver secretosAgy); se guarda en el cache
	buscado bool      // ya se recorrió agy.exe en esta corrida
	sl      cuotaSL   // lo último que Agy le pasó a su statusline; se guarda en el cache
}

// cuotaSL es la parte del payload de la statusline de Agy que importa: sus baldes y de cuándo son.
type cuotaSL struct {
	Baldes map[string]baldeSL `json:"quota,omitempty"`
	Hora   int64              `json:"hora,omitempty"` // ms: cuándo los escribió Agy
}

type baldeSL struct {
	Queda   *float64 `json:"remaining_fraction"`
	Renueva string   `json:"reset_time"`
	Apagado bool     `json:"disabled"`
}

// Sin el user agent de Antigravity, el servidor contesta 403 (pide licencia de Code Assist).
const agyAgente = "antigravity/1.3.5 windows/amd64"

// dirStatusline es donde sl.exe guarda su cache (usa TEMP, después TMP y si no ~\.cache\sl).
func dirStatusline(env func(string) string, home string) string {
	for _, k := range []string{"TEMP", "TMP"} {
		if d := env(k); d != "" {
			return filepath.Join(d, "sl")
		}
	}
	return filepath.Join(home, ".cache", "sl")
}

// statuslineAgy lee el cache de la statusline; si es de Agy (trae "quota"), se queda con sus baldes. Devuelve si había
// algo nuevo de Agy. Lo de Claude Code (que escribe el mismo archivo) se ignora.
func (l *lector) statuslineAgy() bool {
	ruta := filepath.Join(dirStatusline(l.env, l.home), "quota.cache")
	info, err := os.Stat(ruta)
	if err != nil {
		return false
	}
	b, err := os.ReadFile(ruta)
	if err != nil || !bytes.Contains(b, []byte(`"quota"`)) {
		return false
	}
	var p cuotaSL
	if json.Unmarshal(b, &p) != nil || len(p.Baldes) == 0 {
		return false
	}
	p.Hora = info.ModTime().UnixMilli()
	l.agy.mu.Lock()
	defer l.agy.mu.Unlock()
	if p.Hora <= l.agy.sl.Hora {
		return false
	}
	l.agy.sl = p
	return true
}

func leerAgy(l *lector) (ia, error) {
	s, err := l.cred("gemini:antigravity")
	if err != nil {
		return ia{}, errSinCuenta
	}
	var c struct {
		T struct {
			Acceso  string `json:"access_token"`
			Renovar string `json:"refresh_token"`
			Vence   string `json:"expiry"`
		} `json:"token"`
		ID string `json:"id_token"`
	}
	if json.Unmarshal([]byte(s), &c) != nil || c.T.Acceso == "" && c.T.Renovar == "" {
		return ia{}, errSinCuenta
	}
	var jwt struct {
		Cliente string `json:"azp"`
		Correo  string `json:"email"`
	}
	jwtCampos(c.ID, &jwt)
	tok := c.T.Acceso
	if v, _ := time.Parse(time.RFC3339Nano, c.T.Vence); v.Before(l.ahora().Add(time.Minute)) {
		if tok, err = l.renovarAgy(c.T.Renovar, jwt.Cliente); err != nil {
			return ia{}, err
		}
	}
	hdr := map[string]string{"Authorization": "Bearer " + tok, "User-Agent": agyAgente}
	const base = "https://cloudcode-pa.googleapis.com/v1internal:"
	b, err := l.pedir("POST", base+"retrieveUserQuotaSummary", "{}", hdr)
	if err != nil {
		return ia{}, err
	}
	nivel := l.extra("POST", base+"loadCodeAssist", `{"metadata":{"ideType":"ANTIGRAVITY","platform":"PLATFORM_UNSPECIFIED","pluginType":"GEMINI"}}`, hdr)
	modelos := l.extra("POST", base+"fetchAvailableModels", "{}", hdr)
	l.statuslineAgy() // por si el cache tiene ahora un payload de Agy que todavía no se vio
	l.agy.mu.Lock()
	sl := l.agy.sl
	l.agy.mu.Unlock()
	x, err := parseAgy(b, nivel, modelos, sl)
	x.Cuenta = jwt.Correo
	return x, err
}

// parseAgy arma la pestaña: los grupos de la API y, después, los baldes que sólo vio la statusline de Agy.
func parseAgy(b, nivel, modelos []byte, sl cuotaSL) (ia, error) {
	var r struct {
		Grupos []struct {
			Nombre string `json:"displayName"`
			Baldes []struct {
				ID      string   `json:"bucketId"`
				Ventana string   `json:"window"`
				Renova  string   `json:"resetTime"`
				Queda   *float64 `json:"remainingFraction"` // protobuf omite el 0: sin campo = no queda nada
				Apagado bool     `json:"disabled"`
			} `json:"buckets"`
		} `json:"groups"`
	}
	if json.Unmarshal(b, &r) != nil || len(r.Grupos) == 0 {
		return ia{}, errFormato
	}
	var x ia
	deLaAPI := map[string]bool{}
	for _, g := range r.Grupos {
		for _, k := range g.Baldes {
			deLaAPI[k.ID] = true
			queda := 0.0
			if k.Queda != nil {
				queda = *k.Queda
			}
			nombre := map[string]string{"5h": "5 h", "weekly": "semana", "daily": "día"}[k.Ventana]
			if nombre == "" {
				nombre = k.Ventana
			}
			f := medidor(nombre, (1-queda)*100, msDe(k.Renova))
			f.Grupo, f.Apagado = grupoAgy(g.Nombre), k.Apagado
			if k.Apagado {
				f.Nota = "no aplica ahora"
			}
			x.Filas = append(x.Filas, f)
		}
	}
	ids := make([]string, 0, len(sl.Baldes))
	for id := range sl.Baldes {
		if !deLaAPI[id] {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	if sl.Hora == 0 {
		x.agregar(dato("Gemini 4 Argon", "semana", "sin dato todavía", "aparece cuando uses Agy: la API no lo publica y sale de su statusline"))
	}
	for _, id := range ids {
		k := sl.Baldes[id]
		queda := 0.0
		if k.Queda != nil {
			queda = *k.Queda
		}
		grupo, ventana := nombreBaldeSL(id)
		f := medidor(ventana, (1-queda)*100, msDe(k.Renueva))
		f.Grupo, f.Apagado = grupo, k.Apagado
		f.Nota = "según Agy, " + horaCorta(sl.Hora) // la API no lo publica: es lo que Agy mostró por última vez
		x.Filas = append(x.Filas, f)
	}
	var n struct {
		Actual *struct {
			ID     string `json:"id"`
			Nombre string `json:"name"`
			Mejora string `json:"upgradeSubscriptionText"`
		} `json:"currentTier"`
		Pago *struct {
			Nombre string `json:"name"`
		} `json:"paidTier"`
	}
	if nivel != nil && json.Unmarshal(nivel, &n) == nil {
		switch {
		case n.Pago != nil && n.Pago.Nombre != "":
			x.Plan = n.Pago.Nombre
		case n.Actual != nil:
			x.Plan = map[string]string{"free-tier": "Free", "standard-tier": "Standard"}[n.Actual.ID]
			if x.Plan == "" {
				x.Plan = n.Actual.Nombre
			}
			if n.Actual.Mejora != "" {
				x.agregar(dato("cuenta", "con un plan pago", strings.TrimSuffix(n.Actual.Mejora, "."), ""))
			}
		}
	}
	x.Filas = append(x.Filas, modelosAgy(modelos)...)
	return x, nil
}

// modelosAgy: los modelos de cada familia y cuánto les queda. Los modelos de una familia comparten la cuota, así que
// va una fila por familia y estado, con los modelos en la nota. Los ids internos (sin nombre) no van; el mismo nombre
// va una vez.
func modelosAgy(b []byte) []fila {
	var r struct {
		M map[string]struct {
			Nombre string `json:"displayName"`
			Cuota  *struct {
				Queda  *float64 `json:"remainingFraction"`
				Renova string   `json:"resetTime"`
			} `json:"quotaInfo"`
			Etiqueta string `json:"tagTitle"`
		} `json:"models"`
	}
	if b == nil || json.Unmarshal(b, &r) != nil {
		return nil
	}
	type mod struct {
		nombre, familia, etiqueta string
		queda                     float64
		renova                    int64
	}
	var ms []mod
	vistos := map[string]bool{}
	ids := make([]string, 0, len(r.M))
	for id := range r.M {
		ids = append(ids, id)
	}
	slices.Sort(ids) // el mismo nombre puede venir con varios ids: siempre gana el mismo
	for _, id := range ids {
		m := r.M[id]
		if m.Nombre == "" || m.Cuota == nil || vistos[m.Nombre] {
			continue
		}
		vistos[m.Nombre] = true
		q := 0.0
		if m.Cuota.Queda != nil {
			q = *m.Cuota.Queda
		}
		fam := strings.Fields(m.Nombre)[0]
		ms = append(ms, mod{m.Nombre, fam, m.Etiqueta, q, msDe(m.Cuota.Renova)})
	}
	orden := map[string]int{"Gemini": 0, "Claude": 1, "GPT-OSS": 2}
	slices.SortFunc(ms, func(a, b mod) int {
		if oa, ob := orden[a.familia], orden[b.familia]; oa != ob {
			return oa - ob
		}
		return strings.Compare(a.nombre, b.nombre)
	})
	var out []fila
	var cuantos []int
	idx := map[string]int{} // familia + estado -> fila
	for _, m := range ms {
		estado := "queda " + numero(roundTo(m.queda*100, 0)) + " %"
		if m.queda <= 0 {
			estado = "agotado"
		}
		nombre := strings.TrimPrefix(m.nombre, m.familia+" ")
		if m.etiqueta == "Leaving Soon" {
			nombre += " (se va)"
		} else if m.etiqueta == "New" {
			nombre += " (nuevo)"
		}
		k := m.familia + "|" + estado
		i, ok := idx[k]
		if !ok {
			i = len(out)
			idx[k] = i
			f := dato("modelos", m.familia, estado, "")
			if m.queda <= 0 {
				f.Renueva = m.renova // la hora en que vuelve; con cuota, la ventana que rueda no dice nada
			}
			out, cuantos = append(out, f), append(cuantos, 0)
		}
		out[i].Nota = unirComa(out[i].Nota, nombre)
		cuantos[i]++
	}
	for i := range out {
		out[i].Nombre += " · " + plural(cuantos[i], "modelo", "modelos")
	}
	return out
}

func unirComa(a, b string) string {
	if a == "" {
		return b
	}
	return a + ", " + b
}

func grupoAgy(s string) string {
	switch {
	case strings.Contains(s, "Gemini"):
		return "Gemini Flash y Pro"
	case strings.Contains(s, "Claude"):
		return "Claude y GPT"
	}
	return s
}

// nombreBaldeSL: "gemini-argon-wtu-weekly" -> ("Gemini 4 Argon", "semana"); "3p-5h" -> ("Claude y GPT", "5 h").
func nombreBaldeSL(id string) (grupo, ventana string) {
	switch {
	case strings.Contains(id, "argon"):
		grupo = "Gemini 4 Argon"
	case strings.HasPrefix(id, "gemini"):
		grupo = "Gemini Flash y Pro"
	case strings.HasPrefix(id, "3p"):
		grupo = "Claude y GPT"
	default:
		grupo = id
	}
	ventana = id
	for suf, v := range map[string]string{"weekly": "semana", "5h": "5 h", "daily": "día"} {
		if strings.HasSuffix(id, suf) {
			ventana = v
		}
	}
	return grupo, ventana
}

// horaCorta: "a las 20:54" (hoy) o "el 19 oct" (otro día).
func horaCorta(ms int64) string {
	t := time.UnixMilli(ms)
	if t.Format("2006-01-02") == time.Now().Format("2006-01-02") {
		return "a las " + t.Format("15:04")
	}
	return "el " + strings.TrimSuffix(fecha(t.Format(time.RFC3339)), " "+t.Format("2006"))
}

// renovarAgy pide un token nuevo a Google con el refresh token de Agy y el cliente OAuth de Agy. Queda en memoria
// hasta que vence.
func (l *lector) renovarAgy(refresh, cliente string) (string, error) {
	a := &l.agy
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tok != "" && l.ahora().Before(a.vence) {
		return a.tok, nil
	}
	if refresh == "" || cliente == "" {
		return "", errors.New("la sesión venció: abrí Agy para renovarla")
	}
	candidatos := []string{a.cliente}
	if a.cliente == "" {
		if a.buscado { // agy.exe ya se recorrió sin éxito: no se vuelve a leer (son 200 MB)
			return "", errors.New("la sesión venció: abrí Agy para renovarla")
		}
		a.buscado = true
		candidatos = secretosAgy(filepath.Join(l.env("LOCALAPPDATA"), `agy\bin\agy.exe`))
	}
	for _, sec := range candidatos {
		form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {cliente}, "client_secret": {sec}}
		req, _ := http.NewRequest("POST", "https://oauth2.googleapis.com/token", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res, err := l.http.Do(req)
		if err != nil {
			return "", errors.New("sin conexión")
		}
		var t struct {
			Token string `json:"access_token"`
			Dura  int64  `json:"expires_in"`
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&t)
		res.Body.Close()
		if res.StatusCode == 200 && t.Token != "" {
			a.tok, a.vence, a.cliente = t.Token, l.ahora().Add(time.Duration(t.Dura-60)*time.Second), sec
			return t.Token, nil
		}
		if t.Error != "invalid_client" { // la sesión misma no vale (por ejemplo, cerraste sesión en Agy)
			return "", errors.New("la sesión de Agy no se pudo renovar: abrí Agy")
		}
	}
	a.cliente = "" // Agy cambió de cliente: la próxima vez se lo vuelve a buscar en agy.exe
	return "", errors.New("la sesión venció y no la pude renovar: abrí Agy")
}

// secretosAgy busca en agy.exe los secretos de cliente OAuth ("GOCSPX-" y 28 caracteres). En una app instalada no son
// secretos (Google lo documenta así), pero no se copian al código: se buscan una vez y se guardan en el cache.
func secretosAgy(exe string) []string {
	f, err := os.Open(exe)
	if err != nil {
		return nil
	}
	defer f.Close()
	const pre, largo = "GOCSPX-", 35
	var res []string
	buf := make([]byte, 1<<20)
	resto := 0
	for {
		n, err := f.Read(buf[resto:])
		datos := buf[:resto+n]
		for i := 0; ; i++ {
			j := bytes.Index(datos[i:], []byte(pre))
			if j < 0 {
				break
			}
			i += j
			if i+largo > len(datos) {
				break // queda cortado: entra en el resto que pasa a la vuelta siguiente
			}
			if s := string(datos[i : i+largo]); esSecreto(s[len(pre):]) && !slices.Contains(res, s) {
				res = append(res, s)
			}
		}
		if err != nil {
			return res
		}
		resto = min(largo-1, len(datos))
		copy(buf, datos[len(datos)-resto:])
	}
}

func esSecreto(s string) bool {
	for _, r := range s {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_') || r > unicode.MaxASCII {
			return false
		}
	}
	return true
}
