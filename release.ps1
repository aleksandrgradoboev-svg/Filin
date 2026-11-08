# Сборка выпуска Филина: файлы для страницы выпуска на GitHub.
#
#   pwsh release.ps1 -Version v0.5.0 [-Apk data\app\filin.apk]
#
# Кладёт в dist\ три файла выпуска:
#   filin.exe — программа без окна консоли, с версией;
#   filin.apk и app.json — приложение для телефона и его номер версии (выложенные
#   android/publish.ps1). Внутрь filin.exe приложение не вшивается: оно меняется реже
#   программы и весило две трети каждого обновления. Филин сам докачивает его из выпуска,
#   когда там оно новее (update.SyncApp), — поэтому в выпуск идут ОБА файла, и app.json тоже.
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
$cgo = $env:CGO_ENABLED
$env:CGO_ENABLED = '0' # чистый Go: один файл без DLL
try {
    go build -trimpath -ldflags "-H=windowsgui -s -w -X main.version=$Version" -o dist\filin.exe .\cmd\filin
} finally {
    $env:CGO_ENABLED = $cgo # сессию не портим: go test -race без CGO не работает
}
if ($LASTEXITCODE -ne 0) { throw 'сборка filin.exe не удалась' }
# через конвейер: filin.exe собран без консоли, и без него PowerShell не ждёт программу и
# не забирает её вывод. Строка, а не массив: «@() -notmatch …» фильтрует, а не сравнивает,
# и пустой вывод проходил проверку молча (так собирался v0.5.0)
$got = (& .\dist\filin.exe version | Out-String).Trim()
if (-not $got.Contains($Version)) { throw "в filin.exe не та версия: «$got»" }

Copy-Item $Apk dist\filin.apk -Force
Copy-Item $appJson dist\app.json -Force

Get-ChildItem dist | Select-Object Name, Length | Format-Table -AutoSize
"готово: $got"
