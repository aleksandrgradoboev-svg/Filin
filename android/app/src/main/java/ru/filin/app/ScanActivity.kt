package ru.filin.app

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.drawable.GradientDrawable
import android.os.Bundle
import android.os.VibrationEffect
import android.os.Vibrator
import android.view.Gravity
import android.view.ViewGroup
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.TextView
import androidx.activity.ComponentActivity
import androidx.activity.result.contract.ActivityResultContracts
import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.Preview
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.core.content.ContextCompat
import com.google.mlkit.vision.barcode.BarcodeScannerOptions
import com.google.mlkit.vision.barcode.BarcodeScanning
import com.google.mlkit.vision.barcode.common.Barcode
import com.google.mlkit.vision.common.InputImage
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean

// Сканер ISBN: камера + распознавание штрихкода на самом телефоне (ML Kit, без сети).
// Книжный штрихкод — EAN-13 с 978/979; магазинные наклейки с другими префиксами пропускаются.
// Экран остаётся открытым: после сохранения книги — сразу следующая с той же полки.
class ScanActivity : ComponentActivity() {
    private val busy = AtomicBoolean(false) // найден код — ждём экран книги, новые кадры не разбираем
    private val analyzer = Executors.newSingleThreadExecutor()
    private lateinit var status: TextView
    private lateinit var preview: PreviewView

    private val askCamera = registerForActivityResult(ActivityResultContracts.RequestPermission()) { ok ->
        if (ok) startCamera() else status.text = "Без камеры сканер не работает — разрешите её в настройках или введите ISBN ниже."
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        preview = PreviewView(this)
        status = TextView(this).apply {
            text = "Наведите на штрихкод книги"
            setTextColor(0xFFFFFFFF.toInt())
            textSize = 17f
            gravity = Gravity.CENTER
            setShadowLayer(4f, 0f, 1f, 0xFF000000.toInt())
        }
        val frame = android.view.View(this).apply {
            background = GradientDrawable().apply {
                setStroke(dp(3), 0xD9FFFFFF.toInt())
                cornerRadius = dp(8).toFloat()
            }
        }
        val manual = field("или ISBN руками", digits = true)
        val bottom = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(16), dp(12), dp(16), dp(12))
            setBackgroundColor(color(R.color.parchment))
            addView(manual)
            addView(button("Дальше") {
                val v = manual.text.toString().trim()
                if (v.isNotEmpty()) openBook(v)
            })
            // советские книги без ISBN — карточка с фото титула
            addView(button("📖 Нет штрихкода — по титулу или карточке", primary = false) { openBook("") })
        }
        val root = FrameLayout(this).apply {
            addView(preview, FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
            addView(frame, FrameLayout.LayoutParams(dp(280), dp(150), Gravity.CENTER))
            addView(status, FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT,
                Gravity.TOP).apply { topMargin = dp(48) })
            addView(bottom, FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT,
                Gravity.BOTTOM))
        }
        fitSystemBars(root)
        setContentView(root)

        if (ContextCompat.checkSelfPermission(this, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED) {
            startCamera()
        } else {
            askCamera.launch(Manifest.permission.CAMERA)
        }
    }

    override fun onResume() {
        super.onResume()
        busy.set(false) // вернулись с экрана книги — сканируем следующую
        status.text = "Наведите на штрихкод книги"
    }

    // кадр камеры (proxy.image) отдаётся ML Kit напрямую, без копирования — API помечен как требующий согласия
    @androidx.annotation.OptIn(markerClass = [androidx.camera.core.ExperimentalGetImage::class])
    private fun startCamera() {
        val scanner = BarcodeScanning.getClient(
            BarcodeScannerOptions.Builder().setBarcodeFormats(Barcode.FORMAT_EAN_13).build()
        )
        detector = scanner
        val future = ProcessCameraProvider.getInstance(this)
        future.addListener({
            val provider = future.get()
            val pv = Preview.Builder().build().also { it.setSurfaceProvider(preview.surfaceProvider) }
            val analysis = ImageAnalysis.Builder()
                .setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST)
                .build()
            analysis.setAnalyzer(analyzer) { proxy ->
                val media = proxy.image
                if (media == null || busy.get()) {
                    proxy.close()
                    return@setAnalyzer
                }
                scanner.process(InputImage.fromMediaImage(media, proxy.imageInfo.rotationDegrees))
                    .addOnSuccessListener { codes ->
                        val v = codes.firstOrNull()?.rawValue ?: return@addOnSuccessListener
                        if (Regex("^97[89]\\d{10}$").matches(v)) {
                            if (busy.compareAndSet(false, true)) runOnUiThread { found(v) }
                        } else {
                            runOnUiThread { status.text = "Это не книжный штрихкод: $v" }
                        }
                    }
                    .addOnCompleteListener { proxy.close() }
            }
            provider.unbindAll()
            provider.bindToLifecycle(this, CameraSelector.DEFAULT_BACK_CAMERA, pv, analysis)
        }, ContextCompat.getMainExecutor(this))
    }

    private fun found(isbn: String) {
        getSystemService(Vibrator::class.java)?.vibrate(VibrationEffect.createOneShot(80, VibrationEffect.DEFAULT_AMPLITUDE))
        status.text = "Найден $isbn"
        openBook(isbn)
    }

    private fun openBook(isbn: String) {
        busy.set(true)
        startActivity(Intent(this, BookActivity::class.java).putExtra(BookActivity.EXTRA_ISBN, isbn))
    }

    // детектор ML Kit держит родные ресурсы — закрывается вместе с экраном, иначе копится при каждом открытии
    private var detector: com.google.mlkit.vision.barcode.BarcodeScanner? = null

    override fun onDestroy() {
        super.onDestroy()
        analyzer.shutdown()
        detector?.close()
    }
}
