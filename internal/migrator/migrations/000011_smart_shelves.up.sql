-- Smart shelves (rule-based dynamic shelves). Membership is derived at
-- query time from `rule`, so the shelf_books join table is ignored when
-- is_smart = true. Keep the same `shelves` row shape so the list
-- endpoint can return both kinds through one query.
ALTER TABLE shelves
    ADD COLUMN IF NOT EXISTS is_smart BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS rule     JSONB;

-- Smart shelves must have a rule; regular shelves must not. The check
-- keeps legacy rows (regular shelves) safe because the default for
-- is_smart is false.
ALTER TABLE shelves DROP CONSTRAINT IF EXISTS shelves_rule_presence;
ALTER TABLE shelves
    ADD CONSTRAINT shelves_rule_presence CHECK (
        (is_smart = true  AND rule IS NOT NULL) OR
        (is_smart = false AND rule IS NULL)
    );
