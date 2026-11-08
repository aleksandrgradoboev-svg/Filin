-- Оценка — диапазон «от – до» (решено 22.10.2026): аналоги на букинистическом рынке
-- дают разброс, и точная сумма делала бы вид, что разброса нет. amount — нижняя
-- граница, amount_to — верхняя; NULL — точная сумма (как у всех оценок до этой миграции).
ALTER TABLE valuation ADD COLUMN amount_to INTEGER CHECK (amount_to IS NULL OR amount_to > amount);
