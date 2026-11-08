package ru.filin.app

// Поиск ISBN в распознанной строке текста: оборот книги 1987–2000-х — номер напечатан,
// штрихкода нет. Распознаватель путает похожие знаки, поэтому решает контрольная цифра:
// номер, у которого она не сходится, не принимается вовсе.
object IsbnText {
    data class Found(
        val isbn: String,      // только цифры и X: «5852553107», «9785170123456»
        val printed: String,   // как напечатано: «5-85255-310-7»
        val labeled: Boolean,  // перед номером подпись «ISBN»
        val note: String,      // что после номера в строке: «(т. 1)»
    )

    // подпись: «ISBN», как её видит латинский распознаватель — иногда «1SBN», «ISBN:»
    private val label = Regex("""(?i)(?:I|1|l|\|)\s?S\s?B\s?N""")
    // кандидат: 10 или 13 знаков с дефисами/пробелами, последний может быть X
    private val candidate = Regex("""\d[\d\-‐–— ]{8,17}[\dXxХх]""")

    fun find(line: String): List<Found> {
        val out = mutableListOf<Found>()
        for (m in candidate.findAll(line)) {
            val before = line.substring(0, m.range.first)
            val labeled = label.containsMatchIn(before.takeLast(8))
            // жадный поиск прихватывает число после номера («ISBN 5-85255-310-7 1993») —
            // берётся самое длинное начало, которое само является верным ISBN
            val f = longestValid(m.value, labeled) ?: continue
            out += Found(f.first, f.second, labeled, line.substring(m.range.first + f.second.length).trim().take(24))
        }
        return out
    }

    private fun longestValid(text: String, labeled: Boolean): Pair<String, String>? {
        for (end in text.length downTo 1) {
            val raw = text.substring(0, end)
            if (raw.last() !in "0123456789XxХх" || raw.trimEnd() != raw) continue
            val norm = raw.filter { it.isDigit() } + if (raw.last() in "XxХх") "X" else ""
            if (norm.length != 10 && norm.length != 13 || !valid(norm)) continue
            // 10 цифр без подписи — номер, только если разбит на четыре группы, как ISBN:
            // случайный код издания («0503010000—006») так не пишут
            if (!labeled && norm.length == 10 && raw.split(Regex("[\\-‐–— ]+")).size != 4) continue
            return norm to raw
        }
        return null
    }

    fun valid(isbn: String): Boolean {
        when (isbn.length) {
            10 -> {
                var sum = 0
                for (i in 0 until 10) {
                    val c = isbn[i]
                    val v = if (c == 'X') { if (i != 9) return false; 10 } else c.digitToIntOrNull() ?: return false
                    sum += v * (10 - i)
                }
                return sum % 11 == 0
            }
            13 -> {
                if (!isbn.startsWith("978") && !isbn.startsWith("979")) return false
                var sum = 0
                for (i in 0 until 13) {
                    val v = isbn[i].digitToIntOrNull() ?: return false
                    sum += if (i % 2 == 0) v else v * 3
                }
                return sum % 10 == 0
            }
            else -> return false
        }
    }
}
