<#
  build.ps1  -  Compila cupo (app GUI, sin consola) en dist\cupo.exe.

  .\build.ps1          compila
  .\build.ps1 -Test    además go vet + pruebas (no tocan la red ni tus credenciales)
  .\build.ps1 -Icono   regenera cupo.ico (gen-icon.go) y su recurso rsrc_windows_amd64.syso antes de compilar

  Nada se instala ni se publica.
#>
param([switch]$Test, [switch]$Icono)
$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath $PSScriptRoot

if ($Icono) {
    Write-Host '== icono' -ForegroundColor Cyan
    & go run gen-icon.go
    if ($LASTEXITCODE -ne 0) { throw 'gen-icon falló' }
    Remove-Item cupo-256.png -ErrorAction SilentlyContinue
    & go run github.com/akavel/rsrc@v0.10.2 -ico cupo.ico -arch amd64 -o rsrc_windows_amd64.syso
    if ($LASTEXITCODE -ne 0) { throw 'rsrc falló' }
}

if ($Test) {
    Write-Host '== go vet' -ForegroundColor Cyan
    & go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'go vet falló' }
    Write-Host '== pruebas' -ForegroundColor Cyan
    & go test -count=1 ./...
    if ($LASTEXITCODE -ne 0) { throw 'las pruebas fallaron' }
}

Write-Host '== compilando dist\cupo.exe' -ForegroundColor Cyan
New-Item -ItemType Directory -Force -Path (Join-Path $PSScriptRoot 'dist') | Out-Null
& go build -trimpath -ldflags '-H windowsgui -s -w' -o (Join-Path $PSScriptRoot 'dist\cupo.exe') .
if ($LASTEXITCODE -ne 0) { throw 'go build falló' }
$exe = Get-Item (Join-Path $PSScriptRoot 'dist\cupo.exe')
Write-Host ("OK  {0}  ({1:N0} KB)" -f $exe.FullName, ($exe.Length / 1KB)) -ForegroundColor Green
