package ru.filin.app

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class IsbnTextTest {
    // строки с оборота Жаколио (ТЕРРА, 1993), как их напечатали
    @Test fun labeledVolumeAndSet() {
        val a = IsbnText.find("ISBN 5-85255-310-7 (т. 1)")
        assertEquals(listOf("5852553107"), a.map { it.isbn })
        assertTrue(a[0].labeled)
        assertEquals("(т. 1)", a[0].note)
        assertEquals("5852553093", IsbnText.find("ISBN 5-85255-309-3").single().isbn)
    }

    // подпись прочитана с ошибкой — номер с четырьмя группами всё равно номер
    @Test fun mangledLabel() {
        assertEquals("5852553107", IsbnText.find("BM 5-85255-310-7 (т. 1)").single().isbn)
        assertEquals("5852553107", IsbnText.find("1SBN 5-85255-310-7").single().isbn)
    }

    // знак ошибся — контрольная цифра не сходится, номера нет
    @Test fun wrongDigit() {
        assertTrue(IsbnText.find("ISBN 5-85255-318-7").isEmpty())
    }

    // код издания из выходных данных — не ISBN
    @Test fun publisherCode() {
        assertTrue(IsbnText.find("Ж 4703010100-066").isEmpty())
        assertTrue(IsbnText.find("0503010000 006").isEmpty())
    }

    // за номером через пробел — другое число: номер всё равно находится
    @Test fun trailingNumber() {
        val f = IsbnText.find("ISBN 5-85255-310-7 1993").single()
        assertEquals("5852553107", f.isbn)
        assertEquals("5-85255-310-7", f.printed)
        assertEquals("1993", f.note)
        assertEquals("5726004817", IsbnText.find("ISBN 5-7260-0481-7 : 12 р.").single().isbn)
    }

    @Test fun isbn13AndX() {
        assertEquals("9785600017153", IsbnText.find("ISBN 978-5-600-01715-3").single().isbn)
        assertTrue(IsbnText.valid("080442957X"))
        assertEquals("080442957X", IsbnText.find("ISBN 0-8044-2957-X").single().isbn)
    }
}
