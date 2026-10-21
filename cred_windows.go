//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	advapi32   = windows.NewLazySystemDLL("advapi32.dll")
	pCredReadW = advapi32.NewProc("CredReadW")
	pCredFree  = advapi32.NewProc("CredFree")
)

// credential es CREDENTIALW (x64).
type credential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

// leerCred devuelve el secreto de una credencial genérica del Administrador de credenciales de Windows (ahí dejan
// su sesión Copilot y Agy). Sólo lectura.
func leerCred(target string) (string, error) {
	var c *credential
	if r, _, err := pCredReadW.Call(uintptr(unsafe.Pointer(utf16Ptr(target))), 1 /* CRED_TYPE_GENERIC */, 0,
		uintptr(unsafe.Pointer(&c))); r == 0 {
		return "", err
	}
	defer pCredFree.Call(uintptr(unsafe.Pointer(c)))
	b := unsafe.Slice(c.CredentialBlob, c.CredentialBlobSize)
	if len(b) >= 2 && b[1] == 0 { // UTF-16 (keytar y Go guardan UTF-8)
		return windows.UTF16ToString(unsafe.Slice((*uint16)(unsafe.Pointer(c.CredentialBlob)), len(b)/2)), nil
	}
	return string(b), nil
}
