// Сканер ISBN: камера телефона + встроенный в Chrome BarcodeDetector.
// Камеру браузер даёт только «безопасной» странице — localhost или HTTPS; по USB телефон
// открывает приложение как localhost через проброс порта (chrome://inspect).
(async () => {
  const help = document.getElementById('scan-help');
  const status = document.getElementById('scan-status');
  const video = document.getElementById('video');
  // сканировать нечем — рамку и подсказку про камеру убираем, остаётся ввод руками
  const say = (html) => {
    help.innerHTML = html;
    help.hidden = false;
    document.querySelector('.scanner').hidden = true;
    status.hidden = true;
  };

  if (!window.isSecureContext) {
    say('Камера открывается только на <b>localhost</b> или по HTTPS. С телефона по USB: на компьютере ' +
      'откройте <code>chrome://inspect</code> → Port forwarding → <code>8080</code> → <code>localhost:8080</code>, ' +
      'на телефоне — <code>http://localhost:8080/scan</code>.');
    return;
  }
  if (!('BarcodeDetector' in window)) {
    say('В этом браузере нет распознавания штрихкодов (BarcodeDetector). Введите ISBN руками.');
    return;
  }
  const formats = await BarcodeDetector.getSupportedFormats();
  if (!formats.includes('ean_13')) {
    say('Браузер не распознаёт EAN-13 — формат книжного штрихкода. Введите ISBN руками.');
    return;
  }
  const detector = new BarcodeDetector({ formats: ['ean_13'] });

  let stream;
  try {
    stream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: { ideal: 'environment' } }, audio: false });
  } catch (e) {
    say('Нет доступа к камере.');
    status.textContent = String((e && e.message) || e);
    return;
  }
  video.srcObject = stream;
  try {
    await video.play();
  } catch (e) {
    stream.getTracks().forEach((t) => t.stop());
    say('Камера не запустилась — обновите страницу.');
    return;
  }

  const tick = async () => {
    try {
      for (const code of await detector.detect(video)) {
        const v = code.rawValue;
        // книжный штрихкод — 978/979; остальные EAN-13 — магазинные наклейки
        if (/^97[89]\d{10}$/.test(v)) {
          stream.getTracks().forEach((t) => t.stop());
          if (navigator.vibrate) navigator.vibrate(80);
          status.textContent = 'Найден ' + v;
          location.href = '/add?isbn=' + v;
          return;
        }
        status.textContent = 'Это не книжный штрихкод: ' + v;
      }
    } catch (e) {
      // кадр ещё не готов — пробуем следующий
    }
    setTimeout(tick, 150);
  };
  tick();
})();
