package main

// Pruebas de los lectores con respuestas reales de cada servidor (2026-10-20, sin ids ni correos).

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var ahoraPrueba = time.Date(2026, 10, 20, 12, 0, 0, 0, time.Local)

func pct(t *testing.T, f fila) float64 {
	t.Helper()
	if f.Pct == nil {
		t.Fatalf("%q no es un medidor", f.Nombre)
	}
	return *f.Pct
}

// buscar devuelve la fila con ese nombre (y grupo, si se pasa).
func buscar(t *testing.T, x ia, nombre string, grupo ...string) fila {
	t.Helper()
	for _, f := range x.Filas {
		if f.Nombre == nombre && (len(grupo) == 0 || f.Grupo == grupo[0]) {
			return f
		}
	}
	t.Fatalf("no está la fila %q %v en %+v", nombre, grupo, x.Filas)
	return fila{}
}

func TestParseClaude(t *testing.T) {
	uso := []byte(`{"five_hour":{"utilization":45.0,"resets_at":"2026-10-20T17:09:59.687984-03:00"},
		"seven_day":{"utilization":20.0,"resets_at":"2026-10-26T19:59:59.688009-03:00"},"seven_day_opus":null,"seven_day_sonnet":null,
		"cedar_ember":{"eligible":false,"ineligible_reason":"surface","grants":[]},
		"extra_usage":{"is_enabled":false,"monthly_limit":null,"used_credits":null},
		"limits":[{"kind":"session","group":"session","percent":45,"resets_at":"2026-10-20T17:09:59.687984-03:00","scope":null,"is_active":true},
		{"kind":"weekly_all","group":"weekly","percent":20,"resets_at":"2026-10-26T19:59:59.688009-03:00","scope":null,"is_active":false}],
		"seven_day_breakdown":{"rows":[{"key":"claude_code","display_name":"Claude Code","percent":98},
		{"key":"chat","display_name":"Chats","percent":0},{"key":"cowork","display_name":"Cowork","percent":2}]}}`)
	perfil := []byte(`{"account":{"full_name":"ana","display_name":"ana","email":"a@b.c","has_claude_pro":true,"created_at":"2026-10-18T19:57:50.573343Z"},
		"organization":{"organization_type":"claude_pro","billing_type":"stripe_subscription","rate_limit_tier":"default_claude_ai",
		"has_extra_usage_enabled":false,"subscription_status":"trialing","subscription_created_at":"2026-10-19T03:30:55.074337Z","claude_code_trial_ends_at":null}}`)
	x, err := parseClaude(uso, perfil, ahoraPrueba)
	if err != nil {
		t.Fatal(err)
	}
	s := buscar(t, x, "sesión · 5 h")
	if pct(t, s) != 45 || s.Nota != "el que más aprieta ahora" || pct(t, buscar(t, x, "semana")) != 20 {
		t.Errorf("ventanas: %+v", x.Filas)
	}
	if want := time.Date(2026, 10, 20, 20, 9, 59, 687984000, time.UTC).UnixMilli(); s.Renueva != want {
		t.Errorf("renueva %d, quiero %d", s.Renueva, want)
	}
	if f := buscar(t, x, "Claude Code", "reparto de la semana"); f.Valor != "98 %" {
		t.Errorf("reparto: %+v", f)
	}
	for _, f := range x.Filas {
		if f.Nombre == "Chats" || f.Nombre == "reinicios guardados" {
			t.Errorf("no va: %+v (0 %% o reinicios sin derecho)", f)
		}
	}
	if x.Plan != "Pro" || x.Estado != "en prueba" || x.Cuenta != "ana · a@b.c" {
		t.Errorf("cuenta: plan %q estado %q cuenta %q", x.Plan, x.Estado, x.Cuenta)
	}
	if f := buscar(t, x, "suscripción", "cuenta"); !strings.HasPrefix(f.Valor, "en prueba · desde el 19 oct 2026") {
		t.Errorf("suscripción: %q", f.Valor)
	}
	if _, err := parseClaude([]byte(`{"error":{"type":"x"}}`), nil, ahoraPrueba); err == nil {
		t.Error("una respuesta sin ventanas tiene que ser un error, no una pestaña vacía")
	}
	// sin perfil (falló ese pedido) la pestaña sale igual
	if y, err := parseClaude(uso, nil, ahoraPrueba); err != nil || y.Cuenta != "" || len(y.Filas) == 0 {
		t.Errorf("sin perfil: %+v %v", y, err)
	}
}

func TestParseClaudeExtrasYPorModelo(t *testing.T) {
	x, err := parseClaude([]byte(`{"five_hour":{"utilization":10,"resets_at":"2026-10-20T17:00:00Z"},
		"limits":[{"kind":"weekly_scoped","percent":30,"resets_at":"2026-10-26T00:00:00Z","scope":{"display_name":"Fable"},"is_active":true}],
		"extra_usage":{"is_enabled":true,"monthly_limit":10000,"used_credits":2500,"utilization":25,"currency":"USD","decimal_places":2},
		"iguana_necktie":{"limit_dollars":20,"used_dollars":5,"remaining_dollars":15,"resets_at":"2026-11-30T00:00:00Z"},
		"cedar_ember":{"eligible":true,"grants":[{"resets_left":2,"paused":false,"starts_at":"2026-10-01T00:00:00Z","ends_at":"2026-11-01T00:00:00Z"},
		{"resets_left":5,"paused":true,"starts_at":"2026-10-01T00:00:00Z","ends_at":"2026-11-01T00:00:00Z"}]}}`), nil, ahoraPrueba)
	if err != nil {
		t.Fatal(err)
	}
	if f := buscar(t, x, "semana · Fable"); pct(t, f) != 30 || f.Nota == "" {
		t.Errorf("por modelo: %+v", f)
	}
	if f := buscar(t, x, "créditos extra · mes"); pct(t, f) != 25 || !f.Cuota || f.Valor != "US$ 25,00 de US$ 100,00" {
		t.Errorf("créditos extra: %+v", f)
	}
	if f := buscar(t, x, "créditos de nube"); f.Valor != "US$ 15,00 de US$ 20,00" {
		t.Errorf("nube: %+v", f)
	}
	if f := buscar(t, x, "reinicios guardados"); f.Valor != "2 disponibles" {
		t.Errorf("reinicios (los pausados no cuentan): %+v", f)
	}
}

func TestParseCodex(t *testing.T) {
	uso := []byte(`{"email":"a@b.c","plan_type":"free","rate_limit":{"allowed":true,"limit_reached":false,
		"primary_window":{"used_percent":71,"limit_window_seconds":2592000,"reset_after_seconds":2449927,"reset_at":1794955788},
		"secondary_window":null},"code_review_rate_limit":null,
		"additional_rate_limits":[{"limit_name":"GPT-5.3-Codex-Spark","metered_feature":"codex_spark","rate_limit":{
		"primary_window":{"used_percent":12,"limit_window_seconds":18000,"reset_at":1794000000},"secondary_window":{"used_percent":4,"limit_window_seconds":604800,"reset_at":1794500000}}}],
		"credits":{"has_credits":false,"unlimited":false,"balance":null}}`)
	reinicios := []byte(`{"credits":[],"available_count":0,"total_earned_count":0,"immediate_reset_purchase_eligible":false}`)
	perfil := []byte(`{"profile":{"username":"ana","display_name":"Ana"},"stats":{"lifetime_tokens":991346189,"peak_daily_tokens":73573715,
		"current_streak_days":6,"longest_streak_days":13,"total_threads":406,"longest_running_turn_sec":5205,"fast_mode_usage_percentage":45.17,
		"total_skills_used":55,"unique_skills_used":13,"most_used_reasoning_effort":"max","most_used_reasoning_effort_percentage":62.04,
		"daily_usage_buckets":[{"start_date":"2026-10-19","tokens":1000000},{"start_date":"2026-10-20","tokens":2500000},{"start_date":"2026-08-01","tokens":9}]}}`)
	x, err := parseCodex(uso, reinicios, perfil, ahoraPrueba)
	if err != nil {
		t.Fatal(err)
	}
	if x.Plan != "Free" || x.Cuenta != "Ana · a@b.c" {
		t.Errorf("cuenta: %q %q", x.Plan, x.Cuenta)
	}
	if f := buscar(t, x, "30 días"); pct(t, f) != 71 || f.Renueva != 1794955788000 {
		t.Errorf("ventana: %+v", f)
	}
	if pct(t, buscar(t, x, "GPT-5.3-Codex-Spark · 5 h", "por modelo")) != 12 || pct(t, buscar(t, x, "GPT-5.3-Codex-Spark · semana", "por modelo")) != 4 {
		t.Error("límites por modelo")
	}
	if f := buscar(t, x, "reinicios guardados"); f.Valor != "0 disponibles" {
		t.Errorf("reinicios: %+v", f)
	}
	g := buscar(t, x, "tokens por día · 30 días")
	if len(g.Serie) != 30 || g.Serie[29] != 2500000 || g.Serie[28] != 1000000 || g.Valor != "3,5 M tokens" {
		t.Errorf("serie: %d valores, últimos %v %v, %q", len(g.Serie), g.Serie[28], g.Serie[29], g.Valor)
	}
	if f := buscar(t, x, "tokens de por vida"); f.Valor != "991 M" || f.Nota != "pico de un día: 74 M" {
		t.Errorf("por vida: %+v", f)
	}
	if f := buscar(t, x, "hilos"); f.Nota != "el turno más largo duró 1 h 26 min" {
		t.Errorf("hilos: %+v", f)
	}
}

func TestParseAgy(t *testing.T) {
	resumen := []byte(`{"groups":[{"buckets":[{"bucketId":"gemini-weekly","window":"weekly","resetTime":"2026-10-21T05:11:37Z","remainingFraction":0.6188875},
		{"bucketId":"gemini-5h","window":"5h","resetTime":"2026-10-20T19:20:57Z","remainingFraction":1}],"displayName":"Gemini Models"},
		{"buckets":[{"bucketId":"3p-weekly","window":"weekly","resetTime":"2026-10-20T17:56:00Z","remainingFraction":0},
		{"bucketId":"3p-5h","window":"5h","resetTime":"2026-10-20T19:20:57Z","disabled":true,"remainingFraction":1}],"displayName":"Claude and GPT models"}]}`)
	nivel := []byte(`{"currentTier":{"id":"free-tier","name":"Antigravity","upgradeSubscriptionText":"Upgrade to get 1,500 model requests per day with Google AI Pro."}}`)
	modelos := []byte(`{"models":{
		"gemini-3.1-pro-high":{"displayName":"Gemini 3.1 Pro (High)","quotaInfo":{"remainingFraction":1,"resetTime":"2026-10-20T20:27:38Z"},"tagTitle":"Leaving Soon"},
		"gemini-pro-agent":{"displayName":"Gemini 3.1 Pro (High)","quotaInfo":{"remainingFraction":1,"resetTime":"2026-10-20T20:27:38Z"}},
		"claude-opus-5-5-high":{"displayName":"Claude Opus 5.5 (High)","quotaInfo":{"resetTime":"2026-10-20T17:56:00Z"},"tagTitle":"New"},
		"tab_flash_lite_preview":{"quotaInfo":{"remainingFraction":1}}}}`)
	x, err := parseAgy(resumen, nivel, modelos, cuotaSL{})
	if err != nil {
		t.Fatal(err)
	}
	if g := x.Filas[0]; g.Grupo != "Gemini Flash y Pro" || g.Nombre != "semana" || int(pct(t, g)*10) != 381 {
		t.Errorf("gemini semanal: %+v %.2f", g, pct(t, g))
	}
	if f := x.Filas[2]; f.Grupo != "Claude y GPT" || pct(t, f) != 100 {
		t.Errorf("3p semanal agotado: %+v", f)
	}
	if f := x.Filas[3]; !f.Apagado || f.Nombre != "5 h" {
		t.Errorf("el de 5 h apagado: %+v", f)
	}
	if x.Plan != "Free" {
		t.Errorf("plan %q", x.Plan)
	}
	if f := buscar(t, x, "Claude · 1 modelo", "modelos"); f.Valor != "agotado" || f.Nota != "Opus 5.5 (High) (nuevo)" || f.Renueva == 0 {
		t.Errorf("modelo agotado: %+v", f)
	}
	// el mismo nombre va una vez y los ids internos no van
	if f := buscar(t, x, "Gemini · 1 modelo", "modelos"); f.Valor != "queda 100 %" || f.Renueva != 0 {
		t.Errorf("gemini: %+v", f)
	}
	// protobuf omite remainingFraction cuando es 0: sin campo es que no queda nada
	y, _ := parseAgy([]byte(`{"groups":[{"displayName":"Gemini Models","buckets":[{"window":"weekly","resetTime":"2026-10-21T05:11:37Z"}]}]}`), nil, nil, cuotaSL{})
	if pct(t, y.Filas[0]) != 100 {
		t.Errorf("sin remainingFraction tiene que ser 100 %% usado, dio %v", pct(t, y.Filas[0]))
	}
}

// Los baldes que la API no publica (Gemini 4 Argon) salen de la statusline de Agy; los que sí publica, de la API.
func TestAgyBaldesDeLaStatusline(t *testing.T) {
	cero, mitad := 0.0, 0.5
	sl := cuotaSL{Hora: ahoraPrueba.UnixMilli(), Baldes: map[string]baldeSL{
		"gemini-argon-wtu-weekly": {Queda: &cero, Renueva: "2026-10-23T00:46:39Z"},
		"gemini-weekly":           {Queda: &mitad, Renueva: "2026-10-22T23:39:30Z"}, // la API ya lo trae: gana la API
	}}
	x, err := parseAgy([]byte(`{"groups":[{"displayName":"Gemini Models","buckets":[{"bucketId":"gemini-weekly","window":"weekly","remainingFraction":0.9}]}]}`), nil, nil, sl)
	if err != nil {
		t.Fatal(err)
	}
	if len(x.Filas) != 2 || math.Abs(pct(t, x.Filas[0])-10) > 0.01 {
		t.Fatalf("filas: %+v", x.Filas)
	}
	if a := x.Filas[1]; a.Grupo != "Gemini 4 Argon" || a.Nombre != "semana" || pct(t, a) != 100 || !strings.HasPrefix(a.Nota, "según Agy") || a.Renueva == 0 {
		t.Errorf("argon: %+v", a)
	}
}

// El cache de la statusline lo escriben Claude Code y Agy: sólo cuenta lo de Agy.
func TestStatuslineAgy(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "sl"), 0o755)
	ruta := filepath.Join(tmp, "sl", "quota.cache")
	l := &lector{home: t.TempDir(), env: func(k string) string { return map[string]string{"TEMP": tmp}[k] }}
	os.WriteFile(ruta, []byte(`{"model":{"id":"claude-opus-5-5"},"rate_limits":{"seven_day":{"used_percentage":69}}}`), 0o644)
	if l.statuslineAgy() {
		t.Error("un payload de Claude Code no es de Agy")
	}
	os.WriteFile(ruta, []byte(`{"product":"antigravity","quota":{"gemini-argon-wtu-weekly":{"remaining_fraction":0,"reset_time":"2026-10-23T00:46:39Z"}},"plan_tier":"x"}`), 0o644)
	if !l.statuslineAgy() || l.agy.sl.Baldes["gemini-argon-wtu-weekly"].Renueva == "" || l.agy.sl.Hora == 0 {
		t.Errorf("no tomó el payload de Agy: %+v", l.agy.sl)
	}
	if l.statuslineAgy() {
		t.Error("el mismo archivo otra vez no es nuevo")
	}
}

// Codex en esta PC: el total de cada sesión es el último acumulado; sólo se lee lo nuevo de cada archivo.
func TestCodexUsoLocal(t *testing.T) {
	raiz := t.TempDir()
	dir := filepath.Join(raiz, ahoraPrueba.Format("2006"), ahoraPrueba.Format("01"), ahoraPrueba.Format("02"))
	os.MkdirAll(dir, 0o755)
	tc := func(in, cache, out, razon int) string {
		return `{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":` + itoa(in) + `,"cached_input_tokens":` + itoa(cache) +
			`,"output_tokens":` + itoa(out) + `,"reasoning_output_tokens":` + itoa(razon) + `,"total_tokens":` + itoa(in+out) + `}}}}` + "\n"
	}
	f := filepath.Join(dir, "rollout-1.jsonl")
	os.WriteFile(f, []byte(`{"type":"turn_context","payload":{"cwd":"x","model":"gpt-6-luna"}}`+"\n"+
		`{"type":"event_msg","payload":{"type":"token_count","info":null}}`+"\n"+
		tc(1000, 800, 100, 50)+tc(3000, 2500, 300, 200)), 0o644)
	var u codexUso
	fs := u.filas(raiz, ahoraPrueba)
	if len(fs) < 4 || fs[0].Valor != "3,3 k tokens" || !strings.HasPrefix(fs[0].Nota, "2 pedidos") || fs[3].Nombre != "gpt-6-luna" {
		t.Fatalf("%+v", fs)
	}
	fh, _ := os.OpenFile(f, os.O_APPEND|os.O_WRONLY, 0o644)
	fh.WriteString(tc(5000, 4000, 500, 300))
	fh.Close()
	if fs = u.filas(raiz, ahoraPrueba); fs[0].Valor != "5,5 k tokens" || !strings.HasPrefix(fs[0].Nota, "3 pedidos") {
		t.Errorf("después de agregar: %+v", fs[0])
	}
}

func TestParseGrok(t *testing.T) {
	x, err := parseGrok([]byte(`{"config":{"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","start":"2026-10-15T03:25:37.382806-03:00",
		"end":"2026-10-22T03:25:37.382806-03:00"},"creditUsagePercent":32.0,"onDemandCap":{"val":0},"onDemandUsed":{"val":0},
		"productUsage":[{"product":"GrokChat","usagePercent":11.0},{"product":"GrokBuild","usagePercent":21.0}],"prepaidBalance":{"val":1446},
		"topUpMethod":"TOP_UP_METHOD_SAVED_PAYMENT_METHOD"}}`), []byte(`{"subscription_tier_display":"X Premium","leader_mode":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if f := x.Filas[0]; f.Nombre != "créditos · semana" || pct(t, f) != 32 || f.Nota != "desde el 15 oct 2026" {
		t.Fatalf("%+v", f)
	}
	if x.Filas[1].Nombre != "Grok Build" || x.Filas[2].Nombre != "Grok Chat" {
		t.Errorf("reparto ordenado por uso: %+v", x.Filas[1:3])
	}
	if f := buscar(t, x, "saldo prepago"); f.Valor != "US$ 14,46" {
		t.Errorf("el prepago viene en centavos: %+v", f)
	}
	if buscar(t, x, "a demanda").Valor != "apagado" || x.Plan != "X Premium" {
		t.Errorf("a demanda / plan: %+v %q", x.Filas, x.Plan)
	}
}

func TestParseKimi(t *testing.T) {
	x, _ := parseKimi([]byte(`{"code":0,"data":{"available_balance":0,"voucher_balance":0,"cash_balance":-0.19399},"status":true}`),
		[]byte(`{"code":0,"data":{"organization":{"max_concurrency":1,"max_request_per_minute":3,"max_token_per_minute":500000,"max_token_per_day":1500000,
		"max_token_quota":525300},"organization_usage":{"cur_token_usage":68421},"user":{"user_state":"organization_suspended"},"user_group_id":"free"}}`), "US$")
	if !x.Alerta || x.Estado != "suspendida" || x.Plan != "Free · pago por uso" || !strings.Contains(x.Nota, "debés US$ 0,19") {
		t.Errorf("kimi: %+v", x)
	}
	if f := buscar(t, x, "saldo disponible"); f.Valor != "US$ 0,00" || !f.Card {
		t.Errorf("saldo: %+v", f)
	}
	if f := buscar(t, x, "cuota de tokens"); int(pct(t, f)) != 13 || !f.Cuota || f.Valor != "68 k de 525 k" {
		t.Errorf("cuota: %+v", f)
	}
	if buscar(t, x, "tokens por día").Valor != "1,5 M" || buscar(t, x, "pedidos por minuto").Valor != "3" {
		t.Error("límites de la cuenta")
	}
	// sin /users/me (falló ese pedido) queda el saldo
	y, _ := parseKimi([]byte(`{"data":{"available_balance":5,"voucher_balance":0,"cash_balance":5}}`), nil, "¥")
	if y.Alerta || y.Filas[0].Valor != "¥ 5,00" {
		t.Errorf("sin users/me: %+v", y)
	}
}

func TestSaldos(t *testing.T) {
	d, _ := parseDeepSeek([]byte(`{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":"1.43","granted_balance":"0.07","topped_up_balance":"1.36"}]}`))
	if d.Alerta || d.Filas[0].Valor != "US$ 1,43" || d.Filas[0].Nota != "cargado US$ 1,36 · regalado US$ 0,07" {
		t.Errorf("deepseek: %+v", d)
	}
	y, _ := parseYou([]byte(`{"data":{"type":"account","id":"x","attributes":{"balance":19480.0}}}`))
	if y.Filas[0].Valor != "US$ 194,80" {
		t.Errorf("you: %+v", y)
	}
}

func TestParseCopilot(t *testing.T) {
	x, err := parseCopilot([]byte(`{"login":"x","access_type_sku":"subscription_ended","can_signup_for_limited":true,"chat_enabled":false,"cli_enabled":false,"copilot_plan":"individual"}`))
	if err != nil || !x.Alerta || x.Estado != "terminada" || x.Nota != "La suscripción terminó. Podés pasarte a Copilot Free." {
		t.Fatalf("terminada: %+v %v", x, err)
	}
	if buscar(t, x, "chat", "acceso").Valor != "no" {
		t.Error("acceso")
	}
	y, _ := parseCopilot([]byte(`{"copilot_plan":"individual","quota_reset_date":"2026-11-01","quota_snapshots":{
		"chat":{"entitlement":0,"remaining":0,"percent_remaining":100,"unlimited":true},
		"premium_interactions":{"entitlement":300,"remaining":120,"percent_remaining":40,"unlimited":false,"overage_permitted":true}}}`))
	if f := buscar(t, y, "pedidos premium"); pct(t, f) != 60 || f.Valor != "quedan 120 de 300" {
		t.Errorf("con cuota: %+v", f)
	}
	if buscar(t, y, "chat", "cuotas").Valor != "sin límite" || !strings.HasPrefix(buscar(t, y, "excedentes").Valor, "permitidos") {
		t.Errorf("cuotas: %+v", y.Filas)
	}
}

func TestParseZai(t *testing.T) {
	x, err := parseZai([]byte(`{"code":500,"msg":"当前用户不存在coding plan","success":false}`))
	if err != nil || !strings.HasPrefix(x.Nota, "Sin Coding Plan") {
		t.Fatalf("%+v %v", x, err)
	}
	y, _ := parseZai([]byte(`{"success":true,"data":{"planName":"Pro","limits":[{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":12,"usage":1000,"currentValue":120,"nextResetTime":1794000000000}]}}`))
	if f := y.Filas[0]; f.Nombre != "tokens · 5 h" || pct(t, f) != 12 || f.Valor != "120 de 1.000" || y.Plan != "Pro" {
		t.Errorf("con plan: %+v", y)
	}
}

func TestClaveKimi(t *testing.T) {
	toml := "default_model = \"x\"\n[models.\"a\"]\nprovider = \"managed:moonshot-ai\"\n[providers.\"managed:moonshot-ai\"]\ntype = \"kimi\"\n" +
		"base_url = \"https://api.moonshot.ai/v1\"\napi_key = \"sk-prueba\"\n[loop_control]\nmax_steps_per_turn = 1000\n"
	if b, k := claveKimi(toml); b != "https://api.moonshot.ai/v1" || k != "sk-prueba" {
		t.Errorf("dio %q %q", b, k)
	}
	if b, k := claveKimi("[providers.x]\nbase_url = \"https://a\"\n"); b != "" || k != "" {
		t.Errorf("sin api_key no hay clave: %q %q", b, k)
	}
}

func TestQwenSumaElMes(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".qwen", "usage"), 0o755)
	os.WriteFile(filepath.Join(home, ".qwen", "usage", "token-usage-2026-10.jsonl"), []byte(
		`{"localDate":"2026-10-01","model":"qwen3.8-max","totalTokens":21912,"inputTokens":21190,"outputTokens":722,"apiDurationMs":2000}`+"\n"+
			`{"localDate":"2026-10-20","model":"qwen3.8-max","totalTokens":268753,"inputTokens":268337,"outputTokens":416,"cachedTokens":267794,"apiDurationMs":4000}`+"\n"+
			`roto`+"\n"), 0o644)
	x, err := leerQwen(&lector{home: home, ahora: func() time.Time { return ahoraPrueba }})
	if err != nil {
		t.Fatal(err)
	}
	if f := buscar(t, x, "este mes"); f.Valor != "291 k tokens" || f.Nota != "2 pedidos" || !f.Card {
		t.Errorf("mes: %+v", f)
	}
	if buscar(t, x, "hoy").Valor != "269 k tokens" || buscar(t, x, "tiempo de respuesta").Valor != "3 s en promedio" {
		t.Errorf("%+v", x.Filas)
	}
	if g := buscar(t, x, "tokens por día"); len(g.Serie) != 20 || g.Serie[0] != 21912 || g.Serie[19] != 268753 {
		t.Errorf("serie del mes: %v", g.Serie)
	}
	if buscar(t, x, "qwen3.8-max", "por modelo · este mes").Valor != "291 k tokens" {
		t.Error("por modelo")
	}
	if _, err := leerQwen(&lector{home: t.TempDir(), ahora: time.Now}); err != errSinCuenta {
		t.Error("sin ~/.qwen no hay pestaña")
	}
}

// El uso local de Claude Code: cuenta cada respuesta una vez (Claude Code escribe una línea por bloque, todas con el
// mismo uso), sólo la última semana, y en la vuelta siguiente lee sólo lo que se agregó.
func TestClaudeUsoLocal(t *testing.T) {
	raiz := t.TempDir()
	dir := filepath.Join(raiz, "proyecto", "sesion", "subagents")
	os.MkdirAll(dir, 0o755)
	linea := func(id, req, hora, modelo string, in, out, cr int) string {
		return `{"type":"assistant","timestamp":"` + hora + `","requestId":"` + req + `","message":{"id":"` + id + `","model":"` + modelo +
			`","content":[{"type":"text","text":"hola"}],"usage":{"input_tokens":` + itoa(in) + `,"output_tokens":` + itoa(out) +
			`,"cache_read_input_tokens":` + itoa(cr) + `,"cache_creation_input_tokens":0,"output_tokens_details":{"thinking_tokens":5}}}}` + "\n"
	}
	hoy := ahoraPrueba.UTC().Format(time.RFC3339)
	viejo := ahoraPrueba.AddDate(0, 0, -9).UTC().Format(time.RFC3339)
	f := filepath.Join(dir, "agent-1.jsonl")
	os.WriteFile(f, []byte(linea("m1", "r1", hoy, "claude-opus-5-5", 10, 100, 1000)+
		linea("m1", "r1", hoy, "claude-opus-5-5", 10, 100, 1000)+ // el mismo mensaje, otro bloque
		`{"type":"user","message":{"content":"x"}}`+"\n"+
		linea("m0", "r0", viejo, "claude-opus-5-5", 1, 1, 1)+ // hace 9 días: no entra
		linea("m2", "r2", hoy, "claude-sonnet-4-5-20250929", 1, 2, 3)+
		`{"type":"assistant","timestamp":"`+hoy+`","message":{"id":"m3","model":"claude-opus-5-5","usage":{"output_tokens":7`), 0o644) // a medio escribir
	var u claudeUso
	fs := u.filas(raiz, ahoraPrueba)
	h := fs[0]
	if h.Nombre != "hoy" || h.Valor != "1,1 k tokens" || !strings.HasPrefix(h.Nota, "2 respuestas") {
		t.Fatalf("hoy: %+v", h)
	}
	if m := fs[3]; m.Nombre != "Opus 5.5" || m.Valor != "1,1 k tokens" || fs[4].Nombre != "Sonnet 4.5" {
		t.Errorf("modelos: %+v", fs[3:])
	}
	if g := fs[2]; len(g.Serie) != 7 || g.Serie[6] != 1116 {
		t.Errorf("serie: %+v", g)
	}
	// se termina de escribir la línea cortada y llega otra respuesta: sólo se lee lo nuevo
	fh, _ := os.OpenFile(f, os.O_APPEND|os.O_WRONLY, 0o644)
	fh.WriteString(`}}}` + "\n" + linea("m4", "r4", hoy, "claude-opus-5-5", 0, 50, 0))
	fh.Close()
	fs = u.filas(raiz, ahoraPrueba)
	if !strings.HasPrefix(fs[0].Nota, "4 respuestas") || fs[0].Valor != "1,2 k tokens" {
		t.Errorf("después de agregar: %+v", fs[0])
	}
}

func itoa(n int) string { return strings.TrimSpace(strings.Replace(numero(float64(n)), ".", "", -1)) }

// Sin credenciales, cada lector dice "sin cuenta" (la pestaña no se muestra) y no sale a la red.
func TestSinCuentaNoSaleALaRed(t *testing.T) {
	l := &lector{home: t.TempDir(), ahora: time.Now, env: func(string) string { return "" },
		cred: func(string) (string, error) { return "", os.ErrNotExist }}
	for _, p := range proveedores {
		if _, err := p.leer(l); err != errSinCuenta {
			t.Errorf("%s sin cuenta dio %v", p.id, err)
		}
	}
}

// Un token vencido de una CLI que rota tokens no se renueva ni se usa: error claro, sin red.
func TestSesionVencidaNoSaleALaRed(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"t","expiresAt":1000}}`), 0o600)
	os.MkdirAll(filepath.Join(home, ".grok"), 0o755)
	os.WriteFile(filepath.Join(home, ".grok", "auth.json"), []byte(`{"a":{"key":"k","expires_at":"2020-01-01T00:00:00Z"}}`), 0o600)
	l := &lector{home: home, ahora: time.Now}
	for _, leer := range []func(*lector) (ia, error){leerClaude, leerGrok} {
		if _, err := leer(l); err == nil || !strings.Contains(err.Error(), "venció") {
			t.Errorf("quiero 'la sesión venció', dio %v", err)
		}
	}
}

func TestSecretosAgy(t *testing.T) {
	// el secreto cae justo en el borde de un bloque de lectura (1 MiB) y va pegado a otro texto, como en un binario de Go
	sec := "GOCSPX-" + strings.Repeat("aB3_", 7)
	b := make([]byte, 1<<20-10)
	b = append(b, []byte(sec+"seguido-de-otra-cosa"+sec)...)
	ruta := filepath.Join(t.TempDir(), "agy.exe")
	os.WriteFile(ruta, b, 0o644)
	if got := secretosAgy(ruta); len(got) != 1 || got[0] != sec {
		t.Errorf("dio %q", got)
	}
}

func TestFormatos(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{dinero(1234.5, "US$"), "US$ 1.234,50"},
		{dinero(-0.004, "US$"), "US$ 0,00"},
		{cantidad(950), "950"},
		{cantidad(2300), "2,3 k"},
		{cantidad(1.4e6), "1,4 M"},
		{ventanaNombre(18000), "5 h"},
		{ventanaNombre(604800), "semana"},
		{ventanaNombre(2592000), "30 días"},
		{separarPalabras("GrokBuild"), "Grok Build"},
		{numero(2.5), "2,5"},
		{duracion(5205), "1 h 26 min"},
		{duracion(3 * 86400), "3 d"},
		{fecha("2026-10-19T03:30:55Z"), "19 oct 2026"},
		{modeloLindo("claude-opus-5-5"), "Opus 5.5"},
	} {
		if c.got != c.want {
			t.Errorf("dio %q, quiero %q", c.got, c.want)
		}
	}
	var exp struct {
		Exp int64 `json:"exp"`
	}
	if !jwtCampos("x.eyJleHAiOjE3OTUwMDAwMDB9.y", &exp) || exp.Exp != 1795000000 {
		t.Errorf("jwt: %+v", exp)
	}
}

func TestAnillo(t *testing.T) {
	if nivel(59.9) != 0 || nivel(60) != 1 || nivel(85) != 2 || nivel(100) != 3 {
		t.Error("cortes de nivel")
	}
	if b := anilloPNG(58, 71, 24); len(b) < 100 || string(b[1:4]) != "PNG" {
		t.Error("no es un PNG")
	}
}
