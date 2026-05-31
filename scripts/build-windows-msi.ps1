$ErrorActionPreference = "Stop"

$ProductVersion = if ($env:MSI_VERSION) { $env:MSI_VERSION } else { "0.1.0" }
if ($ProductVersion -notmatch '^\d+\.\d+\.\d+(\.\d+)?$') {
    throw "MSI_VERSION must be a numeric Windows Installer version such as 0.1.0 or 1.2.3.4; got '$ProductVersion'."
}

$Root = (Resolve-Path (Join-Path $PSScriptRoot ".."))
$OutDir = Join-Path $Root "out"
$ClientExe = Join-Path $OutDir "anylan-client-windows-amd64.exe"
$TapSetupExe = Join-Path $OutDir "anylan-tapsetup-windows-amd64.exe"
$MsiPath = Join-Path $OutDir "anylan-client-windows-amd64.msi"
$TapMsm = Join-Path $OutDir "tap-windows-9.27.0-I0-amd64.msm"
$TapMsmUrl = "https://github.com/OpenVPN/tap-windows6/releases/download/9.27.0/tap-windows-9.27.0-I0-amd64.msm"
$TapMsmSha256 = "0f52d8e2b22a0b827b0d5f1e23077dc53f2b41b67839737c39503dc20c435063"
$TapDistZip = Join-Path $OutDir "dist.win10.zip"
$TapDistUrl = "https://github.com/OpenVPN/tap-windows6/releases/download/9.27.0/dist.win10.zip"
$TapDistSha256 = "36e2609b7ceefedcb978ce5c48caf9e0e5af83423717c4e2e3c1d7ebca8f62a5"
$TapDistDir = Join-Path $OutDir "tap-windows6-dist"
$TapDriverDir = Join-Path $TapDistDir "dist.win10\amd64"
$WixSource = Join-Path $Root "cmd/client/wix/Product.wxs"
$IconFile = Join-Path $Root "cmd/client/wix/assets/anylan.ico"
$LicenseFile = Join-Path $Root "cmd/client/wix/assets/license.rtf"
$ManifestFile = Join-Path $Root "cmd/client/windows/anylan-client.exe.manifest"
$ResourceFile = Join-Path $Root "cmd/client/rsrc_windows_amd64.syso"

Set-Location $Root
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

npm --prefix (Join-Path $Root "webui") ci
npm --prefix (Join-Path $Root "webui") run build

go install github.com/akavel/rsrc@v0.10.2
$GoBin = if ($env:GOBIN) { $env:GOBIN } else { Join-Path (go env GOPATH) "bin" }
$env:PATH = "$GoBin;$env:PATH"
rsrc -arch amd64 -manifest $ManifestFile -ico $IconFile -o $ResourceFile

$env:GOOS = "windows"
$env:GOARCH = "amd64"
try {
    go build -o $ClientExe ./cmd/client
    go build -o $TapSetupExe ./cmd/tapsetup
} finally {
    Remove-Item -Force $ResourceFile -ErrorAction SilentlyContinue
}

Invoke-WebRequest -Uri $TapMsmUrl -OutFile $TapMsm
$ActualSha256 = (Get-FileHash -Algorithm SHA256 $TapMsm).Hash.ToLowerInvariant()
if ($ActualSha256 -ne $TapMsmSha256) {
    throw "Unexpected SHA256 for $TapMsm. Expected $TapMsmSha256, got $ActualSha256."
}

Invoke-WebRequest -Uri $TapDistUrl -OutFile $TapDistZip
$ActualTapDistSha256 = (Get-FileHash -Algorithm SHA256 $TapDistZip).Hash.ToLowerInvariant()
if ($ActualTapDistSha256 -ne $TapDistSha256) {
    throw "Unexpected SHA256 for $TapDistZip. Expected $TapDistSha256, got $ActualTapDistSha256."
}
Remove-Item -Recurse -Force $TapDistDir -ErrorAction SilentlyContinue
Expand-Archive -Path $TapDistZip -DestinationPath $TapDistDir

wix extension add WixToolset.UI.wixext/5.0.2 --global

wix build $WixSource `
    -arch x64 `
    -ext WixToolset.UI.wixext `
    -d ProductVersion=$ProductVersion `
    -d ClientExe=$ClientExe `
    -d TapSetupExe=$TapSetupExe `
    -d TapMsm=$TapMsm `
    -d TapDriverDir=$TapDriverDir `
    -d IconFile=$IconFile `
    -d LicenseFile=$LicenseFile `
    -out $MsiPath
