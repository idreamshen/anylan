$ErrorActionPreference = "Stop"

$ProductVersion = if ($env:MSI_VERSION) { $env:MSI_VERSION } else { "0.1.0" }
if ($ProductVersion -notmatch '^\d+\.\d+\.\d+(\.\d+)?$') {
    throw "MSI_VERSION must be a numeric Windows Installer version such as 0.1.0 or 1.2.3.4; got '$ProductVersion'."
}

$Root = (Resolve-Path (Join-Path $PSScriptRoot ".."))
$OutDir = Join-Path $Root "out"
$ClientExe = Join-Path $OutDir "anylan-client-windows-amd64.exe"
$MsiPath = Join-Path $OutDir "anylan-client-windows-amd64.msi"
$TapMsm = Join-Path $OutDir "tap-windows-9.27.0-I0-amd64.msm"
$TapMsmUrl = "https://github.com/OpenVPN/tap-windows6/releases/download/9.27.0/tap-windows-9.27.0-I0-amd64.msm"
$TapMsmSha256 = "0f52d8e2b22a0b827b0d5f1e23077dc53f2b41b67839737c39503dc20c435063"
$WixSource = Join-Path $Root "cmd/client/wix/Product.wxs"

Set-Location $Root
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

npm --prefix (Join-Path $Root "webui") ci
npm --prefix (Join-Path $Root "webui") run build

$env:GOOS = "windows"
$env:GOARCH = "amd64"
go build -o $ClientExe ./cmd/client

Invoke-WebRequest -Uri $TapMsmUrl -OutFile $TapMsm
$ActualSha256 = (Get-FileHash -Algorithm SHA256 $TapMsm).Hash.ToLowerInvariant()
if ($ActualSha256 -ne $TapMsmSha256) {
    throw "Unexpected SHA256 for $TapMsm. Expected $TapMsmSha256, got $ActualSha256."
}

wix build $WixSource `
    -arch x64 `
    -d ProductVersion=$ProductVersion `
    -d ClientExe=$ClientExe `
    -d TapMsm=$TapMsm `
    -out $MsiPath
