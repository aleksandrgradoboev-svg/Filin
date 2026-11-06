# Выкладка новой версии приложения на сервер Филина.
#
#   pwsh android/publish.ps1 -Data <папка библиотеки, где filin.db>   например D:\Филин
#
# Собирает подписанный выпуск и кладёт в <Data>\app два файла: filin.apk и app.json
# с номером версии. Сервер отдаёт их по /app.apk и /api/app; приложение на телефоне
# при следующем запуске предложит обновиться.
# Перед выкладкой поднять versionCode (и versionName) в app/build.gradle.kts —
# телефон обновляется, только если номер на сервере больше установленного.
param(
    [Parameter(Mandatory)] [string] $Data
)
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

& .\gradlew.bat assembleRelease --no-daemon -q
if ($LASTEXITCODE -ne 0) { throw 'сборка выпуска не удалась' }

$out = 'app\build\outputs\apk\release'
$meta = Get-Content "$out\output-metadata.json" -Raw | ConvertFrom-Json
$el = $meta.elements[0]
$apk = Join-Path $out $el.outputFile
if ($el.outputFile -match 'unsigned') {
    throw 'выпуск собран без подписи: в android/local.properties нужна строка filin.signing=<путь к .properties ключа>'
}

# подпись проверяется до выкладки: неподписанный или чужой APK телефон не поставит.
# apksigner — из того же Android SDK, которым собирали: sdk.dir в local.properties
# (в нём «:» и «\» экранированы), иначе ANDROID_HOME / ANDROID_SDK_ROOT; build-tools — новейшие.
$sdk = $env:ANDROID_HOME
if (-not $sdk) { $sdk = $env:ANDROID_SDK_ROOT }
$lp = Get-Content local.properties -ErrorAction SilentlyContinue | Where-Object { $_ -match '^sdk\.dir=' } | Select-Object -First 1
if ($lp) { $sdk = ($lp -replace '^sdk\.dir=', '') -replace '\\:', ':' -replace '\\\\', '\' }
if (-not $sdk -or -not (Test-Path $sdk)) { throw 'не найден Android SDK: sdk.dir в android/local.properties или ANDROID_HOME' }
$apksigner = Get-ChildItem (Join-Path $sdk 'build-tools') -Directory |
    Sort-Object { [version]($_.Name -replace '-.*$', '') } -Descending |
    ForEach-Object { Join-Path $_.FullName 'apksigner.bat' } | Where-Object { Test-Path $_ } | Select-Object -First 1
if (-not $apksigner) { throw "в $sdk\build-tools нет apksigner" }
& $apksigner verify $apk
if ($LASTEXITCODE -ne 0) { throw "APK не прошёл проверку подписи: $apk" }

$dir = Join-Path $Data 'app'
New-Item -ItemType Directory -Force $dir | Out-Null
$old = Join-Path $dir 'app.json'
if (Test-Path $old) {
    $was = (Get-Content $old -Raw | ConvertFrom-Json).version_code
    if ($el.versionCode -le $was) { throw "versionCode $($el.versionCode) не больше выложенного $was — телефон не увидит обновление" }
}
# сначала APK, потом сведения: пока app.json старый, телефон не полезет за новым файлом
Copy-Item $apk (Join-Path $dir 'filin.apk') -Force
@{ version_code = $el.versionCode; version_name = $el.versionName } | ConvertTo-Json | Set-Content -Encoding utf8 $old
"выложено: Филин $($el.versionName) (версия $($el.versionCode)) → $dir"
