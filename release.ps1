# Сборка выпуска Филина: файлы для страницы выпуска на GitHub.
#
#   pwsh release.ps1 -Version v0.5.0 [-Apk data\app\filin.apk]
#
# Кладёт в dist\: filin.exe — без окна консоли, с версией и приложением для телефона внутри,
# и filin.apk — то же приложение отдельным файлом (выложенное android/publish.ps1).
# Тег и сам выпуск делаются после влития в main: собирать выпуск надо из main.
param(
    [Parameter(Mandatory)] [ValidatePattern('^v\d+\.\d+\.\d+$')] [string] $Version,
    [string] $Apk = (Join-Path $PSScriptRoot 'data\app\filin.apk')
)
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

$branch = git rev-parse --abbrev-ref HEAD
if ($branch -ne 'main') { throw "выпуск собирается из main, а сейчас $branch" }
if (git status --porcelain --untracked-files=no) { throw 'в рабочей копии есть незакоммиченные правки' }

go test ./...
if ($LASTEXITCODE -ne 0) { throw 'тесты не прошли' }

if (-not (Test-Path $Apk)) { throw "нет APK: $Apk — сначала android/publish.ps1" }
$appJson = Join-Path (Split-Path $Apk) 'app.json'
if (-not (Test-Path $appJson)) { throw "нет $appJson рядом с APK — его пишет android/publish.ps1" }

New-Item -ItemType Directory -Force dist | Out-Null
# приложение для телефона вшивается в filin.exe (пакет internal/bundled): человек скачивает
# один файл, и QR «поставить приложение» сразу ведёт к APK. Лежит там только на время сборки.
$bundle = 'internal\bundled\app'
$cgo = $env:CGO_ENABLED
$env:CGO_ENABLED = '0' # чистый Go: один файл без DLL
try {
    Copy-Item $Apk (Join-Path $bundle 'filin.apk') -Force
    Copy-Item $appJson (Join-Path $bundle 'app.json') -Force
    go build -trimpath -ldflags "-H=windowsgui -s -w -X main.version=$Version" -o dist\filin.exe .\cmd\filin
} finally {
    $env:CGO_ENABLED = $cgo # сессию не портим: go test -race без CGO не работает
    Remove-Item (Join-Path $bundle 'filin.apk'), (Join-Path $bundle 'app.json') -ErrorAction SilentlyContinue
}
if ($LASTEXITCODE -ne 0) { throw 'сборка filin.exe не удалась' }
if ((Get-Item dist\filin.exe).Length -lt (Get-Item $Apk).Length) { throw 'приложение не вшилось: filin.exe меньше APK' }
# через конвейер: filin.exe собран без консоли, и без него PowerShell не ждёт программу и
# не забирает её вывод. Строка, а не массив: «@() -notmatch …» фильтрует, а не сравнивает,
# и пустой вывод проходил проверку молча (так собирался v0.5.0)
$got = (& .\dist\filin.exe version | Out-String).Trim()
if (-not $got.Contains($Version)) { throw "в filin.exe не та версия: «$got»" }

Copy-Item $Apk dist\filin.apk -Force # и отдельным файлом — для тех, кто ставит приложение сам

Get-ChildItem dist | Select-Object Name, Length | Format-Table -AutoSize
"готово: $got"
