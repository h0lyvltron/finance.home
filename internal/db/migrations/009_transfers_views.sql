-- Slice 9: transfer matching aliases, dashboard view scope, loan interest,
-- and one Transfer split per linked transfer transaction.

PRAGMA foreign_keys = ON;

-- Comma-separated text that identifies this account in other accounts'
-- bank descriptions (e.g. "BARCLAYCARD" for a store card, "2123" for a loan).
ALTER TABLE accounts ADD COLUMN transfer_aliases TEXT NOT NULL DEFAULT '';

ALTER TABLE dashboard_layouts ADD COLUMN view_scope TEXT NOT NULL DEFAULT 'household'
    CHECK (view_scope IN ('household', 'a', 'b'));

INSERT INTO categories(name, kind, parent_id, fixed_flexible, color, display_order)
SELECT 'Loan interest', 'expense', p.id, p.fixed_flexible, p.color,
       (SELECT COALESCE(MAX(display_order), 0) + 1 FROM categories)
FROM categories p
WHERE p.name = 'Debt payments' AND p.kind = 'expense' AND p.parent_id IS NULL
  AND NOT EXISTS (SELECT 1 FROM categories WHERE name = 'Loan interest' AND kind = 'expense')
LIMIT 1;

INSERT INTO categories(name, kind, fixed_flexible, color, display_order)
SELECT 'Loan interest', 'expense', 'fixed', '#A66B6B',
       (SELECT COALESCE(MAX(display_order), 0) + 1 FROM categories)
WHERE EXISTS (SELECT 1 FROM households)
  AND NOT EXISTS (SELECT 1 FROM categories WHERE name = 'Loan interest' AND kind = 'expense');

-- Pairing used to recategorize the existing side without touching its splits,
-- leaving stale expense/income splits on linked transfers.
DELETE FROM transaction_splits
WHERE transaction_id IN (SELECT id FROM transactions WHERE transfer_id IS NOT NULL)
  AND EXISTS (SELECT 1 FROM categories WHERE kind = 'transfer' AND name = 'Transfer');

INSERT INTO transaction_splits(transaction_id, category_id, person, amount, display_order)
SELECT t.id, (SELECT id FROM categories WHERE kind = 'transfer' AND name = 'Transfer' LIMIT 1),
       t.person, t.amount, 0
FROM transactions t
WHERE t.transfer_id IS NOT NULL
  AND EXISTS (SELECT 1 FROM categories WHERE kind = 'transfer' AND name = 'Transfer');

UPDATE transactions
SET category_id = (SELECT id FROM categories WHERE kind = 'transfer' AND name = 'Transfer' LIMIT 1),
    is_split = 0
WHERE transfer_id IS NOT NULL
  AND EXISTS (SELECT 1 FROM categories WHERE kind = 'transfer' AND name = 'Transfer');
