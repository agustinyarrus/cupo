package main

// Rutas y lo que se guarda. Regla del usuario: todo vive dentro de Box. cupo está en Box\cupo (el exe en dist\) y
// sus datos en Box\cupo\data.

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type paths struct {
	Raiz     string // Box\cupo
	WebData  string // data\webview2: perfil del panel
	Settings string // data\settings.json: dónde quedó el panel
	Cache    string // data\cache.json: lo último que se supo de cada IA
	Log      string // data\cupo.log
}

// resolvePaths: la carpeta del exe, o la de arriba si el exe está en dist\. CUPO_RAIZ la fija a mano (desarrollo).
func resolvePaths() paths {
	raiz := os.Getenv("CUPO_RAIZ")
	if raiz == "" {
		exe, _ := os.Executable()
		raiz = filepath.Dir(exe)
		if filepath.Base(raiz) == "dist" {
			raiz = filepath.Dir(raiz)
		}
	}
	data := filepath.Join(raiz, "data")
	return paths{
		Raiz:     raiz,
		WebData:  filepath.Join(data, "webview2"),
		Settings: filepath.Join(data, "settings.json"),
		Cache:    filepath.Join(data, "cache.json"),
		Log:      filepath.Join(data, "cupo.log"),
	}
}

// settings es lo que cupo recuerda entre sesiones: dónde estaba el panel.
type settings struct {
	Window *winGeom `json:"window,omitempty"`
}

// cache: lo último que se supo de cada IA (para mostrar algo al abrir y cuando una sesión vence), el cliente OAuth de
// Agy y lo último que Agy le pasó a su statusline (el balde de Argon, que la API no publica).
type cache struct {
	IAs        map[string]*ia `json:"ias"`
	AgyCliente string         `json:"agyCliente,omitempty"`
	AgySL      cuotaSL        `json:"agyStatusline,omitempty"`
}

// cargar lee un JSON; si no existe o está roto, deja v como estaba.
func cargar(path string, v any) {
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, v)
	}
}

// guardar escribe a un temporal y lo renombra: un corte a mitad de escritura no deja JSON roto.
func guardar(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
