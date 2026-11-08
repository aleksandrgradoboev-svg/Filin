# Сборка пакета распознавания Филина: filin-ocr-<N>.zip для отдельного выпуска на GitHub.
#
#   pwsh ocr-pack/pack.ps1 -Venv <site-packages окружения с onnxruntime и rapidocr> -Ori <doc_ori.onnx>
#
# Исходники — те самые файлы, на которых мерили распознавание: каждый сверяется по SHA-256,
# чужой файл в пакет не попадёт. Пакет — onnxruntime.dll, модели, словарь, лицензии и
# manifest.json с размером и суммой каждого файла. В конце печатает размер и сумму zip — их
# вписать в internal/ocrpack (pack.go), иначе Филин такой пакет не примет.
param(
    [Parameter(Mandatory)] [string] $Venv,
    [Parameter(Mandatory)] [string] $Ori,
    [int] $Version = 1,
    [string] $Out = (Join-Path $PSScriptRoot '..\dist')
)
$ErrorActionPreference = 'Stop'

$src = [ordered]@{
    'onnxruntime.dll'                         = @{ from = "$Venv\onnxruntime\capi\onnxruntime.dll"; sha = 'e45f61e50d8650885452be538a00bb14a715e46a1bc3691000ce947e89ed0306' }
    'det.onnx'                                = @{ from = "$Venv\rapidocr\models\PP-OCRv6_det_small.onnx"; sha = '090f04abcd9d9a7498bc4ebf677e4cb9bdce1fe4197ddb7e529f1ef44e1ff94f' }
    'rec.onnx'                                = @{ from = "$Venv\rapidocr\models\eslav_PP-OCRv5_rec_mobile.onnx"; sha = '08705d6721849b1347d26187f15a5e362c431963a2a62bfff4feac578c489aab' }
    'rec_dict.txt'                            = @{ from = "$PSScriptRoot\eslav_dict.txt"; sha = '3e95f1581557162870cacdba5af91a4c6be2890710d395b0c3c7578e7ee5e6eb' }
    'ori.onnx'                                = @{ from = $Ori; sha = 'af9a0a4f317ff0709ce752067807f819cb15d883f8ecad89f28df1c6ee2d9c92' }
    'NOTICE.md'                               = @{ from = "$PSScriptRoot\NOTICE.md" }
    'licenses/onnxruntime-LICENSE.txt'        = @{ from = "$Venv\onnxruntime\LICENSE"; sha = 'c250d6278f0b47a6439fb7592b08b58a55eb9f535aa49a1db63211c3f982b674' }
    'licenses/onnxruntime-ThirdPartyNotices.txt' = @{ from = "$Venv\onnxruntime\ThirdPartyNotices.txt"; sha = 'c53a76501ef60db6f865f20599f220761201ac4057683acefdab37d861b86622' }
    'licenses/Apache-2.0.txt'                 = @{ from = "$Venv\rapidocr-3.10.0.dist-info\licenses\LICENSE"; sha = '3e0af25fdd06aa9586ae97adb00ea927ebe5a3805ac77d2d3a81ce5f55693333' }
    'licenses/RapidOCR-MODEL_LICENSES.md'     = @{ from = "$Venv\rapidocr-3.10.0.dist-info\licenses\MODEL_LICENSES.md"; sha = 'c5fd0e8603d3df743355b546121e6fe0f4ef8ff455c5a85c1681fd3ae520e153' }
}

$stage = Join-Path ([IO.Path]::GetTempPath()) "filin-ocr-$Version-$PID"
if (Test-Path $stage) { Remove-Item $stage -Recurse -Force }
New-Item -ItemType Directory -Force (Join-Path $stage 'licenses') | Out-Null
$manifest = [ordered]@{ version = $Version; files = [ordered]@{} }
try {
    foreach ($name in $src.Keys) {
        $s = $src[$name]
        if (-not (Test-Path $s.from)) { throw "нет исходника $name`: $($s.from)" }
        $sum = (Get-FileHash $s.from -Algorithm SHA256).Hash.ToLower()
        if ($s.sha -and $sum -ne $s.sha) { throw "$name`: сумма $sum, ждали $($s.sha) — не тот файл" }
        $dst = Join-Path $stage $name
        Copy-Item $s.from $dst
        $manifest.files[$name] = [ordered]@{ size = (Get-Item $dst).Length; sha256 = $sum }
    }
    $manifest | ConvertTo-Json -Depth 4 | Set-Content (Join-Path $stage 'manifest.json') -Encoding utf8NoBOM
    New-Item -ItemType Directory -Force $Out | Out-Null
    $zip = Join-Path (Resolve-Path $Out) "filin-ocr-$Version.zip"
    if (Test-Path $zip) { Remove-Item $zip }
    Compress-Archive -Path (Join-Path $stage '*') -DestinationPath $zip -CompressionLevel Optimal
    $z = Get-Item $zip
    "собран: $zip"
    "размер: $($z.Length) байт ({0:N1} МБ)" -f ($z.Length / 1MB)
    "sha256: $((Get-FileHash $zip -Algorithm SHA256).Hash.ToLower())"
}
finally {
    Remove-Item $stage -Recurse -Force -ErrorAction SilentlyContinue
}
