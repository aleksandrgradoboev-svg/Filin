# Пакет распознавания Филина — откуда что

Филин распознаёт фото титулов и каталожных карточек моделями PaddleOCR через ONNX Runtime.
Этот пакет (`filin-ocr-<N>.zip`) Филин скачивает сам при первом запуске и кладёт рядом с
библиотекой. Файлы в нём не изменены — взяты как есть из источников ниже.

| Файл | Что это | Источник | Лицензия |
|---|---|---|---|
| `onnxruntime.dll` | ONNX Runtime 1.31.1 для Windows x64 (CPU) | Microsoft, пакет `onnxruntime` 1.31.1 с PyPI | MIT — `licenses/onnxruntime-LICENSE.txt`, `licenses/onnxruntime-ThirdPartyNotices.txt` |
| `det.onnx` | поиск строк, PP-OCRv6 small det (`PP-OCRv6_det_small.onnx`) | PaddleOCR (Baidu), раздаёт RapidOCR 3.10.0 | Apache-2.0 — `licenses/Apache-2.0.txt`, `licenses/RapidOCR-MODEL_LICENSES.md` |
| `rec.onnx` | чтение строк, восточнославянский алфавит, PP-OCRv5 mobile rec (`eslav_PP-OCRv5_rec_mobile.onnx`) | PaddleOCR (Baidu), раздаёт RapidOCR 3.10.0 (ModelScope), карточка модели — huggingface.co/PaddlePaddle/eslav_PP-OCRv5_mobile_rec | Apache-2.0 |
| `rec_dict.txt` | словарь знаков модели чтения — выписан из её метаданных (`character`) | там же | Apache-2.0 |
| `ori.onnx` | ориентация страницы, PP-LCNet_x1_0_doc_ori (`inference.onnx`) | PaddlePaddle, huggingface.co/PaddlePaddle/PP-LCNet_x1_0_doc_ori_onnx | Apache-2.0 |
| `manifest.json` | опись: размер и SHA-256 каждого файла | Филин | — |

Copyright модели: Baidu и правообладатели PaddleOCR (github.com/PaddlePaddle/PaddleOCR).
ONNX Runtime: Copyright (c) Microsoft Corporation.
