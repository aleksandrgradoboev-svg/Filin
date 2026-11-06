package ru.filin.app

import android.app.Activity
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.text.InputType
import android.util.TypedValue
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.TextView
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat

// Экраны собираются кодом: их немного, и так видно всё сразу, без россыпи XML.

fun Activity.dp(v: Int) = TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, v.toFloat(), resources.displayMetrics).toInt()

fun Activity.color(id: Int) = getColor(id)

/** Вертикальная колонка с отступами; системные панели не наезжают на содержимое (Android 15+ рисует под ними). */
fun Activity.column(padding: Int = 16): LinearLayout = LinearLayout(this).apply {
    orientation = LinearLayout.VERTICAL
    setPadding(dp(padding), dp(padding), dp(padding), dp(padding))
}

fun Activity.fitSystemBars(v: View) {
    val l = v.paddingLeft
    val t = v.paddingTop
    val r = v.paddingRight
    val b = v.paddingBottom
    ViewCompat.setOnApplyWindowInsetsListener(v) { view, insets ->
        val bars = insets.getInsets(WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.ime())
        view.setPadding(l + bars.left, t + bars.top, r + bars.right, b + bars.bottom)
        insets
    }
}

fun Activity.title(text: String) = TextView(this).apply {
    this.text = text
    textSize = 24f
    typeface = Typeface.SERIF
    setTypeface(typeface, Typeface.BOLD)
    setTextColor(color(R.color.ink))
    setPadding(0, 0, 0, dp(8))
}

fun Activity.text(text: String, muted: Boolean = false, size: Float = 16f) = TextView(this).apply {
    this.text = text
    textSize = size
    setTextColor(color(if (muted) R.color.muted else R.color.ink))
    setPadding(0, dp(4), 0, dp(4))
}

fun Activity.field(hint: String, value: String = "", digits: Boolean = false) = EditText(this).apply {
    this.hint = hint
    setText(value)
    setTextColor(color(R.color.ink))
    setHintTextColor(color(R.color.muted))
    inputType = if (digits) InputType.TYPE_CLASS_NUMBER else InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_CAP_SENTENCES
    background = GradientDrawable().apply {
        setColor(0xBFFFFAEB.toInt())
        setStroke(dp(1), 0xFFCDB47E.toInt())
        cornerRadius = dp(8).toFloat()
    }
    setPadding(dp(12), dp(10), dp(12), dp(10))
    layoutParams = LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT)
        .apply { topMargin = dp(6) }
}

fun Activity.button(text: String, primary: Boolean = true, onClick: () -> Unit) = Button(this).apply {
    this.text = text
    isAllCaps = false
    textSize = 16f
    setTextColor(if (primary) 0xFFFFF8E6.toInt() else color(R.color.accent))
    background = GradientDrawable().apply {
        setColor(if (primary) color(R.color.accent) else 0x00000000)
        setStroke(dp(1), color(R.color.accent))
        cornerRadius = dp(8).toFloat()
    }
    layoutParams = LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT)
        .apply { topMargin = dp(10) }
    setOnClickListener { onClick() }
}

/** Лист пергамента — блок с рамкой, как карточки в веб-части. */
fun Activity.sheet(): LinearLayout = LinearLayout(this).apply {
    orientation = LinearLayout.VERTICAL
    setPadding(dp(14), dp(12), dp(14), dp(12))
    background = GradientDrawable().apply {
        setColor(color(R.color.sheet))
        setStroke(dp(1), 0xFFD9C391.toInt())
        cornerRadius = dp(6).toFloat()
    }
    layoutParams = LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT)
        .apply { topMargin = dp(10) }
}

fun Activity.errorText() = TextView(this).apply {
    setTextColor(color(R.color.warn))
    textSize = 15f
    gravity = Gravity.START
    visibility = View.GONE
}

fun TextView.show(msg: String?) {
    text = msg ?: ""
    visibility = if (msg.isNullOrEmpty()) View.GONE else View.VISIBLE
}
