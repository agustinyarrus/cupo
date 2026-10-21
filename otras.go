package main

// otras.go: Grok, Kimi, DeepSeek, You.com, Qwen, Mistral, Copilot y Z.ai.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// ---- Grok: cli-chat-proxy.grok.com/v1/billing?format=credits y /v1/settings (el plan) -----------

func leerGrok(l *lector) (ia, error) {
	var a map[string]struct {
		Key    string `json:"key"`
		Vence  string `json:"expires_at"`
		Correo string `json:"email"`
		Nombre string `json:"first_name"`
	}
	if leerJSON(l.ruta(`.grok\auth.json`), &a) != nil {
		return ia{}, errSinCuenta
	}
	var key, vence, cuenta string
	for _, v := range a {
		if v.Key != "" && v.Vence >= vence {
			key, vence, cuenta = v.Key, v.Vence, unir(v.Nombre, v.Correo)
		}
	}
	if key == "" {
		return ia{}, errSinCuenta
	}
	if t, err := time.Parse(time.RFC3339Nano, vence); err == nil && t.Before(l.ahora()) {
		return ia{}, errors.New("la sesión venció: abrí Grok para renovarla")
	}
	hdr := map[string]string{"Authorization": "Bearer " + key, "X-XAI-Token-Auth": "xai-grok-cli", "x-grok-client-surface": "grok-build"}
	b, err := l.pedir("GET", "https://cli-chat-proxy.grok.com/v1/billing?format=credits", "", hdr)
	if err != nil {
		return ia{}, err
	}
	x, err := parseGrok(b, l.extra("GET", "https://cli-chat-proxy.grok.com/v1/settings", "", hdr))
	x.Cuenta = cuenta
	return x, err
}

type centavos struct {
	Val *float64 `json:"val"`
}

func parseGrok(b, ajustes []byte) (ia, error) {
	var r struct {
		C *struct {
			Periodo struct {
				Tipo   string `json:"type"`
				Inicio string `json:"start"`
				Fin    string `json:"end"`
			} `json:"currentPeriod"`
			Pct      *float64  `json:"creditUsagePercent"`
			TopeDem  *centavos `json:"onDemandCap"`
			UsoDem   *centavos `json:"onDemandUsed"`
			Prepago  *centavos `json:"prepaidBalance"`
			Recarga  string    `json:"topUpMethod"`
			FinCiclo string    `json:"billingPeriodEnd"`
			Producto []struct {
				Nombre string  `json:"product"`
				Pct    float64 `json:"usagePercent"`
			} `json:"productUsage"`
		} `json:"config"`
	}
	if json.Unmarshal(b, &r) != nil || r.C == nil {
		return ia{}, errFormato
	}
	c := r.C
	pct := c.Pct
	if pct == nil && c.TopeDem != nil && c.UsoDem != nil && ptrVal(c.TopeDem.Val) > 0 {
		v := ptrVal(c.UsoDem.Val) / ptrVal(c.TopeDem.Val) * 100
		pct = &v
	}
	if pct == nil {
		return ia{}, errFormato
	}
	nombre := "período"
	for clave, n := range map[string]string{"WEEKLY": "semana", "MONTHLY": "mes", "DAILY": "día"} {
		if strings.Contains(c.Periodo.Tipo, clave) {
			nombre = n
		}
	}
	fin := c.Periodo.Fin
	if fin == "" {
		fin = c.FinCiclo
	}
	f := medidor("créditos · "+nombre, *pct, msDe(fin))
	if c.Periodo.Inicio != "" {
		f.Nota = "desde el " + fecha(c.Periodo.Inicio)
	}
	x := ia{Filas: []fila{f}}
	sort.SliceStable(c.Producto, func(i, j int) bool { return c.Producto[i].Pct > c.Producto[j].Pct })
	for _, p := range c.Producto {
		if p.Pct > 0 {
			x.agregar(dato("reparto por producto", separarPalabras(p.Nombre), numero(p.Pct)+" %", ""))
		}
	}
	const g = "créditos"
	if c.TopeDem != nil { // los montos vienen en centavos de dólar
		uso := 0.0
		if c.UsoDem != nil {
			uso = ptrVal(c.UsoDem.Val)
		}
		if tope := ptrVal(c.TopeDem.Val); tope > 0 {
			x.agregar(dato(g, "a demanda", dinero(uso/100, "US$")+" de "+dinero(tope/100, "US$"), "lo que se cobra aparte cuando se terminan los créditos"))
		} else {
			x.agregar(dato(g, "a demanda", "apagado", "cuando se terminan los créditos no se cobra extra"))
		}
	}
	if c.Prepago != nil {
		x.agregar(dato(g, "saldo prepago", dinero(ptrVal(c.Prepago.Val)/100, "US$"),
			map[string]string{"TOP_UP_METHOD_SAVED_PAYMENT_METHOD": "recarga con la tarjeta guardada"}[c.Recarga]))
	}
	var s struct {
		Plan string `json:"subscription_tier_display"`
	}
	if ajustes != nil && json.Unmarshal(ajustes, &s) == nil {
		x.Plan = s.Plan
	}
	return x, nil
}

// ---- Kimi (plataforma de Moonshot): saldo y límites de la cuenta --------------------------------

func leerKimi(l *lector) (ia, error) {
	b, _ := os.ReadFile(l.ruta(`.kimi\config.toml`))
	base, clave := claveKimi(string(b))
	if clave == "" {
		base, clave = "https://api.moonshot.ai/v1", l.env("KIMI_API_KEY")
	}
	if clave == "" {
		return ia{}, errSinCuenta
	}
	// shortcut: sólo la plataforma de Moonshot (pago por uso); con el plan Kimi Code (api.kimi.com) habría que leer /usages.
	host := hostDe(base)
	moneda := map[string]string{"api.moonshot.ai": "US$", "api.moonshot.cn": "¥"}[host]
	if moneda == "" {
		return ia{}, fmt.Errorf("no sé leer el saldo de %s", host)
	}
	hdr := map[string]string{"Authorization": "Bearer " + clave}
	r, err := l.pedir("GET", "https://"+host+"/v1/users/me/balance", "", hdr)
	if err != nil {
		return ia{}, err
	}
	return parseKimi(r, l.extra("GET", "https://"+host+"/v1/users/me", "", hdr), moneda)
}

func parseKimi(b, yo []byte, moneda string) (ia, error) {
	var r struct {
		D *struct {
			Disponible float64 `json:"available_balance"`
			Cupones    float64 `json:"voucher_balance"`
			Efectivo   float64 `json:"cash_balance"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &r) != nil || r.D == nil {
		return ia{}, errFormato
	}
	saldo := dato("", "saldo disponible", dinero(r.D.Disponible, moneda), unir("efectivo "+dinero(r.D.Efectivo, moneda), "cupones "+dinero(r.D.Cupones, moneda)))
	saldo.Card = true
	x := ia{Plan: "pago por uso", Filas: []fila{saldo}}
	if r.D.Disponible <= 0 {
		x.Alerta, x.Nota = true, "Sin saldo: Kimi no responde hasta que cargues."
		if r.D.Efectivo < 0 {
			x.Nota = "Sin saldo: debés " + dinero(-r.D.Efectivo, moneda) + " y Kimi no responde hasta que cargues."
		}
	}
	var m struct {
		D struct {
			Org struct {
				Concurrencia float64 `json:"max_concurrency"`
				RPM          float64 `json:"max_request_per_minute"`
				TPM          float64 `json:"max_token_per_minute"`
				TPD          float64 `json:"max_token_per_day"`
				Cuota        float64 `json:"max_token_quota"`
			} `json:"organization"`
			Uso struct {
				Tokens float64 `json:"cur_token_usage"`
			} `json:"organization_usage"`
			U struct {
				Estado string `json:"user_state"`
			} `json:"user"`
			Grupo string `json:"user_group_id"`
		} `json:"data"`
	}
	if yo == nil || json.Unmarshal(yo, &m) != nil {
		return x, nil
	}
	if m.D.Grupo != "" {
		x.Plan = unir(titulo(m.D.Grupo), "pago por uso")
	}
	if strings.Contains(m.D.U.Estado, "suspended") {
		x.Estado, x.Alerta = "suspendida", true
	}
	o := m.D.Org
	if o.Cuota > 0 {
		f := medidor("cuota de tokens", m.D.Uso.Tokens/o.Cuota*100, 0)
		f.Valor, f.Cuota = cantidad(m.D.Uso.Tokens)+" de "+cantidad(o.Cuota), true
		x.Filas = append(x.Filas, f)
	}
	const g = "límites de la cuenta"
	x.agregar(
		dato(g, "pedidos por minuto", cond(o.RPM > 0, numero(o.RPM)), ""),
		dato(g, "tokens por minuto", cond(o.TPM > 0, cantidad(o.TPM)), ""),
		dato(g, "tokens por día", cond(o.TPD > 0, cantidad(o.TPD)), ""),
		dato(g, "pedidos a la vez", cond(o.Concurrencia > 0, numero(o.Concurrencia)), ""),
		dato(g, "estado", map[string]string{"organization_suspended": "suspendida (falta saldo)", "normal": "normal", "active": "activa"}[m.D.U.Estado], ""),
	)
	return x, nil
}

// claveKimi saca base_url y api_key del primer proveedor de config.toml que tenga las dos.
func claveKimi(toml string) (base, clave string) {
	for _, l := range strings.Split(toml+"\n[", "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "[") {
			if base != "" && clave != "" {
				return base, clave
			}
			base, clave = "", ""
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch strings.TrimSpace(k) {
		case "base_url":
			if ok {
				base = v
			}
		case "api_key":
			if ok {
				clave = v
			}
		}
	}
	return "", ""
}

// ---- DeepSeek y You.com: saldo ------------------------------------------------------------------

func leerDeepSeek(l *lector) (ia, error) {
	var s struct {
		Env struct {
			Clave string `json:"API_KEY"`
		} `json:"env"`
	}
	_ = leerJSON(l.ruta(`.deepcode\settings.json`), &s)
	clave := s.Env.Clave
	if clave == "" {
		clave = l.env("DEEPSEEK_API_KEY")
	}
	if clave == "" {
		return ia{}, errSinCuenta
	}
	b, err := l.pedir("GET", "https://api.deepseek.com/user/balance", "", map[string]string{"Authorization": "Bearer " + clave})
	if err != nil {
		return ia{}, err
	}
	return parseDeepSeek(b)
}

func parseDeepSeek(b []byte) (ia, error) {
	var r struct {
		Disponible bool `json:"is_available"`
		Saldos     []struct {
			Moneda  string `json:"currency"`
			Total   string `json:"total_balance"`
			Regalo  string `json:"granted_balance"`
			Cargado string `json:"topped_up_balance"`
		} `json:"balance_infos"`
	}
	if json.Unmarshal(b, &r) != nil {
		return ia{}, errFormato
	}
	x := ia{Plan: "pago por uso", Nota: "El uso por día y por modelo sólo está en platform.deepseek.com (con la sesión web)."}
	for _, s := range r.Saldos {
		m := monedaDe(s.Moneda)
		f := dato("", "saldo", dinero(aNum(s.Total), m), "cargado "+dinero(aNum(s.Cargado), m)+" · regalado "+dinero(aNum(s.Regalo), m))
		f.Card = true
		x.Filas = append(x.Filas, f)
	}
	if !r.Disponible {
		x.Alerta, x.Nota = true, "Sin saldo: DeepSeek no responde hasta que cargues."
	}
	return x, nil
}

func leerYou(l *lector) (ia, error) {
	clave := l.env("YDC_API_KEY")
	if clave == "" {
		return ia{}, errSinCuenta
	}
	b, err := l.pedir("GET", "https://api.you.com/v1/billing/account_balance", "", map[string]string{"X-API-Key": clave})
	if err != nil {
		return ia{}, err
	}
	return parseYou(b)
}

func parseYou(b []byte) (ia, error) {
	var r struct {
		D struct {
			A struct {
				Centavos *float64 `json:"balance"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &r) != nil || r.D.A.Centavos == nil {
		return ia{}, errFormato
	}
	f := dato("", "saldo", dinero(*r.D.A.Centavos/100, "US$"), "créditos de la API (búsqueda, research)")
	f.Card = true
	x := ia{Plan: "créditos", Filas: []fila{f}, Nota: "El historial de uso sólo está en el panel web de you.com/platform."}
	if *r.D.A.Centavos <= 0 {
		x.Alerta = true
	}
	return x, nil
}

// ---- Qwen y Mistral: sin servidor de uso con la API key ------------------------------------------

// leerQwen suma lo que Qwen Code anota de cada pedido (DashScope no publica el saldo con la API key).
func leerQwen(l *lector) (ia, error) {
	if _, err := os.Stat(l.ruta(".qwen")); err != nil {
		return ia{}, errSinCuenta
	}
	hoy := l.ahora()
	var mes, dia sumaQwen
	porModelo := map[string]*sumaQwen{}
	porDia := map[string]float64{}
	if f, err := os.Open(l.ruta(`.qwen\usage\token-usage-` + hoy.Format("2006-01") + ".jsonl")); err == nil {
		defer f.Close()
		dHoy := hoy.Format("2006-01-02")
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			var r struct {
				Dia     string  `json:"localDate"`
				Modelo  string  `json:"model"`
				Total   float64 `json:"totalTokens"`
				Entrada float64 `json:"inputTokens"`
				Salida  float64 `json:"outputTokens"`
				Cache   float64 `json:"cachedTokens"`
				Razon   float64 `json:"thoughtsTokens"`
				Ms      float64 `json:"apiDurationMs"`
			}
			if json.Unmarshal(sc.Bytes(), &r) != nil {
				continue
			}
			s := sumaQwen{1, r.Total, r.Entrada, r.Salida, r.Cache, r.Razon, r.Ms}
			if porModelo[r.Modelo] == nil {
				porModelo[r.Modelo] = &sumaQwen{}
			}
			mes.sumar(s)
			porModelo[r.Modelo].sumar(s)
			porDia[r.Dia] += r.Total
			if r.Dia == dHoy {
				dia.sumar(s)
			}
		}
	}
	mesCard := dato("", "este mes", cantidad(mes.total)+" tokens", plural(int(mes.pedidos), "pedido", "pedidos"))
	mesCard.Card = true
	diaCard := dato("", "hoy", cantidad(dia.total)+" tokens", plural(int(dia.pedidos), "pedido", "pedidos"))
	diaCard.Card = true
	x := ia{Plan: "pago por uso", Nota: "DashScope no publica el saldo con la API key: esto es lo que anota Qwen Code en esta PC.",
		Filas: []fila{mesCard, diaCard}}
	serie, desde := serieDias(porDia, hoy, hoy.Day())
	const g = "este mes"
	x.agregar(
		fila{Grupo: g, Nombre: "tokens por día", Serie: serie, Desde: desde},
		dato(g, "entrada", cantidad(mes.entrada), cond(mes.cache > 0, "de caché "+cantidad(mes.cache))),
		dato(g, "salida", cantidad(mes.salida), cond(mes.razon > 0, "razonamiento "+cantidad(mes.razon))),
		dato(g, "tiempo de respuesta", cond(mes.pedidos > 0, numero(roundTo(mes.ms/1000/max(mes.pedidos, 1), 1))+" s en promedio"), ""),
	)
	for m, s := range porModelo {
		x.agregar(dato("por modelo · este mes", m, cantidad(s.total)+" tokens", plural(int(s.pedidos), "pedido", "pedidos")))
	}
	return x, nil
}

type sumaQwen struct{ pedidos, total, entrada, salida, cache, razon, ms float64 }

func (d *sumaQwen) sumar(s sumaQwen) {
	d.pedidos += s.pedidos
	d.total += s.total
	d.entrada += s.entrada
	d.salida += s.salida
	d.cache += s.cache
	d.razon += s.razon
	d.ms += s.ms
}

func leerMistral(l *lector) (ia, error) {
	var c map[string]struct {
		P struct {
			Plan string `json:"plan_name"`
			Tipo string `json:"plan_type"`
		} `json:"payload"`
		Hora int64 `json:"stored_at_timestamp"`
	}
	if leerJSON(l.ruta(`.vibe\whoami_cache.json`), &c) != nil || len(c) == 0 {
		return ia{}, errSinCuenta
	}
	x := ia{Nota: "Mistral no publica cuánto usaste con la API key: el detalle está en admin.mistral.ai (con la sesión web)."}
	for _, v := range c {
		x.Plan = titulo(v.P.Plan)
		x.agregar(
			dato("cuenta", "plan", unir(titulo(v.P.Plan), map[string]string{"api": "de la API"}[v.P.Tipo]), "según Mistral Vibe"),
			dato("cuenta", "dato de", cond(v.Hora > 0, fecha(time.Unix(v.Hora, 0).Format(time.RFC3339))), "la última vez que abriste Vibe"))
	}
	return x, nil
}

// ---- Copilot y Z.ai ----------------------------------------------------------------------------

func leerCopilot(l *lector) (ia, error) {
	var c struct {
		U *struct{ Host, Login string } `json:"lastLoggedInUser"`
	}
	b, err := os.ReadFile(l.ruta(`.copilot\config.json`))
	if err != nil || json.Unmarshal(sinComentarios(b), &c) != nil || c.U == nil || c.U.Login == "" {
		return ia{}, errSinCuenta
	}
	// shortcut: sólo github.com; con GitHub Enterprise el servidor es otro.
	if c.U.Host != "https://github.com" {
		return ia{}, errSinCuenta
	}
	var tok string
	for _, t := range []string{c.U.Host + ":" + c.U.Login + ".copilot-cli", c.U.Host + ":" + c.U.Login + ":github.copilot-cli"} {
		if s, err := l.cred(t); err == nil && s != "" {
			tok = s
			break
		}
	}
	if tok == "" {
		return ia{}, errSinCuenta
	}
	r, err := l.pedir("GET", "https://api.github.com/copilot_internal/user", "", map[string]string{
		"Authorization": "token " + tok, "X-GitHub-Api-Version": "2025-04-01"})
	if err != nil {
		return ia{}, err
	}
	x, err := parseCopilot(r)
	x.Cuenta = c.U.Login
	return x, err
}

func parseCopilot(b []byte) (ia, error) {
	type cuota struct {
		Total     float64 `json:"entitlement"`
		Queda     float64 `json:"remaining"`
		PctQueda  float64 `json:"percent_remaining"`
		Libre     bool    `json:"unlimited"`
		Excedente bool    `json:"overage_permitted"`
		Exceso    float64 `json:"overage_count"`
	}
	var r struct {
		SKU     string            `json:"access_type_sku"`
		Plan    string            `json:"copilot_plan"`
		Renueva string            `json:"quota_reset_date"`
		Cuotas  map[string]*cuota `json:"quota_snapshots"`
		Free    bool              `json:"can_signup_for_limited"`
		Chat    bool              `json:"chat_enabled"`
		CLI     bool              `json:"cli_enabled"`
		MCP     bool              `json:"is_mcp_enabled"`
	}
	if json.Unmarshal(b, &r) != nil {
		return ia{}, errFormato
	}
	x := ia{Plan: titulo(r.Plan)}
	renueva := int64(0)
	if t, err := time.Parse("2006-01-02", r.Renueva); err == nil {
		renueva = t.UnixMilli()
	}
	for _, k := range []struct{ clave, nombre string }{{"premium_interactions", "pedidos premium"}, {"chat", "chat"}, {"completions", "autocompletado"}} {
		c := r.Cuotas[k.clave]
		switch {
		case c == nil:
		case c.Libre:
			x.agregar(dato("cuotas", k.nombre, "sin límite", ""))
		default:
			f := medidor(k.nombre, 100-c.PctQueda, renueva)
			f.Valor = "quedan " + numero(c.Queda) + " de " + numero(c.Total)
			if c.Exceso > 0 {
				f.Nota = numero(c.Exceso) + " de excedente"
			}
			x.Filas = append(x.Filas, f)
			if k.clave == "premium_interactions" {
				x.agregar(dato("cuotas", "excedentes", cond(c.Excedente, "permitidos (se cobran aparte)")+cond(!c.Excedente, "no permitidos"), ""))
			}
		}
	}
	if r.SKU == "subscription_ended" {
		x.Estado, x.Alerta, x.Nota = "terminada", true, "La suscripción terminó."
		if r.Free {
			x.Nota += " Podés pasarte a Copilot Free."
		}
	}
	const g = "acceso"
	x.agregar(
		dato(g, "chat", siNo(r.Chat), ""),
		dato(g, "Copilot CLI", siNo(r.CLI), ""),
		dato(g, "MCP", siNo(r.MCP), ""),
		dato(g, "tipo de acceso", strings.ReplaceAll(r.SKU, "_", " "), ""),
	)
	return x, nil
}

func leerZai(l *lector) (ia, error) {
	clave := l.env("Z_AI_API_KEY")
	if clave == "" {
		clave = l.env("ZAI_API_KEY")
	}
	if clave == "" {
		return ia{}, errSinCuenta
	}
	b, err := l.pedir("GET", "https://api.z.ai/api/monitor/usage/quota/limit", "", map[string]string{"Authorization": clave, "Accept-Language": "en-US,en"})
	if err != nil {
		return ia{}, err
	}
	return parseZai(b)
}

func parseZai(b []byte) (ia, error) {
	var r struct {
		OK  bool   `json:"success"`
		Msg string `json:"msg"`
		D   *struct {
			Plan    string `json:"planName"`
			Limites []struct {
				Tipo    string  `json:"type"`
				Unidad  int     `json:"unit"`
				Numero  int     `json:"number"`
				Pct     float64 `json:"percentage"`
				Total   float64 `json:"usage"`
				Usado   float64 `json:"currentValue"`
				Renueva int64   `json:"nextResetTime"`
			} `json:"limits"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &r) != nil {
		return ia{}, errFormato
	}
	if !r.OK {
		if strings.Contains(strings.ToLower(r.Msg), "coding plan") {
			return ia{Plan: "pago por uso", Nota: "Sin Coding Plan: la key es de pago por uso y Z.ai no publica su saldo ni su uso por la API."}, nil
		}
		return ia{}, fmt.Errorf("Z.ai: %s", r.Msg)
	}
	// shortcut: sin una respuesta real de un Coding Plan para comparar; las unidades (3 = horas, 5 = meses) salen de
	// la documentación de CodexBar. Ajustar con la primera respuesta real.
	var x ia
	if r.D != nil {
		x.Plan = r.D.Plan
		for _, lim := range r.D.Limites {
			nombre := map[string]string{"TOKENS_LIMIT": "tokens", "CREDIT_LIMIT": "créditos", "TIME_LIMIT": "herramientas MCP"}[lim.Tipo]
			if nombre == "" {
				nombre = strings.ToLower(lim.Tipo)
			}
			if u := map[int]string{1: "min", 3: "h", 4: "días", 5: "mes"}[lim.Unidad]; u != "" && lim.Numero > 0 {
				nombre += fmt.Sprintf(" · %d %s", lim.Numero, u)
			}
			f := medidor(nombre, lim.Pct, lim.Renueva)
			if lim.Total > 0 {
				f.Valor = numero(lim.Usado) + " de " + numero(lim.Total)
			}
			x.Filas = append(x.Filas, f)
		}
	}
	return x, nil
}
