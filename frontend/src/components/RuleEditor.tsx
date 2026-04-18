import { useState, type CSSProperties } from 'react';

import type {
  RuleField,
  RuleMatch,
  RuleOp,
  ShelfPredicate,
  ShelfRule,
} from '@/api/books';
import { Icon } from './Icon';

// FieldMeta keeps the per-field metadata in one place: the operators the
// wire accepts and whether the value is numeric. Mirrors the validation
// at internal/model/shelf_rule.go.
type FieldMeta = {
  label: string;
  ops: ReadonlyArray<{ op: RuleOp; label: string }>;
  kind: 'string' | 'int' | 'tags' | 'progress';
};

const FIELDS: Record<RuleField, FieldMeta> = {
  title: {
    label: 'Title',
    kind: 'string',
    ops: [
      { op: 'contains', label: 'contains' },
      { op: 'starts_with', label: 'starts with' },
      { op: 'eq', label: 'is' },
      { op: 'ne', label: 'is not' },
    ],
  },
  author: {
    label: 'Author',
    kind: 'string',
    ops: [
      { op: 'eq', label: 'is' },
      { op: 'ne', label: 'is not' },
      { op: 'contains', label: 'contains' },
      { op: 'starts_with', label: 'starts with' },
    ],
  },
  series: {
    label: 'Series',
    kind: 'string',
    ops: [
      { op: 'eq', label: 'is' },
      { op: 'contains', label: 'contains' },
    ],
  },
  format: {
    label: 'Format',
    kind: 'string',
    ops: [
      { op: 'eq', label: 'is' },
      { op: 'ne', label: 'is not' },
    ],
  },
  tags: {
    label: 'Tag',
    kind: 'tags',
    ops: [{ op: 'contains', label: 'contains' }],
  },
  year: {
    label: 'Year',
    kind: 'int',
    ops: [
      { op: 'eq', label: '=' },
      { op: 'gte', label: '≥' },
      { op: 'gt', label: '>' },
      { op: 'lte', label: '≤' },
      { op: 'lt', label: '<' },
      { op: 'ne', label: '≠' },
    ],
  },
  rating: {
    label: 'Rating',
    kind: 'int',
    ops: [
      { op: 'eq', label: '=' },
      { op: 'gte', label: '≥' },
      { op: 'gt', label: '>' },
      { op: 'lte', label: '≤' },
      { op: 'lt', label: '<' },
    ],
  },
  progress: {
    label: 'Progress',
    kind: 'progress',
    ops: [
      { op: 'eq', label: '=' },
      { op: 'lt', label: '<' },
      { op: 'lte', label: '≤' },
      { op: 'gt', label: '>' },
      { op: 'gte', label: '≥' },
    ],
  },
};

const FIELD_ORDER: RuleField[] = [
  'author',
  'title',
  'series',
  'tags',
  'year',
  'rating',
  'format',
  'progress',
];

// blankPredicate seeds a new row with the first allowed op for the chosen
// field so the user never sees an invalid combo even for a split second.
function blankPredicate(field: RuleField = 'author'): ShelfPredicate {
  const meta = FIELDS[field];
  const op = meta.ops[0].op;
  const value: string | number = meta.kind === 'string' || meta.kind === 'tags' ? '' : 0;
  return { field, op, value };
}

type Props = {
  title: string;
  initialName?: string;
  initialRule?: ShelfRule;
  submitLabel: string;
  error?: string | null;
  busy?: boolean;
  // When name is omitted (editing an existing shelf), the editor hides
  // the name field and only returns the rule.
  showName?: boolean;
  onSubmit: (draft: { name: string; rule: ShelfRule }) => void;
  onCancel: () => void;
};

// RuleEditor renders the shelf-rule form inside a centered modal card.
// Reusable for create + edit — the caller controls whether the name
// field is shown.
export function RuleEditor({
  title,
  initialName,
  initialRule,
  submitLabel,
  error,
  busy,
  showName = true,
  onSubmit,
  onCancel,
}: Props) {
  const [name, setName] = useState(initialName ?? '');
  const [rule, setRule] = useState<ShelfRule>(
    initialRule ?? { match: 'all', predicates: [blankPredicate()] },
  );

  const setPredicate = (i: number, next: ShelfPredicate) => {
    setRule((r) => {
      const copy = r.predicates.slice();
      copy[i] = next;
      return { ...r, predicates: copy };
    });
  };
  const removePredicate = (i: number) => {
    setRule((r) => ({
      ...r,
      predicates: r.predicates.filter((_, idx) => idx !== i),
    }));
  };
  const addPredicate = () => {
    setRule((r) => ({ ...r, predicates: [...r.predicates, blankPredicate()] }));
  };

  const canSubmit =
    (!showName || name.trim() !== '') &&
    rule.predicates.length > 0 &&
    rule.predicates.every(predicateIsComplete);

  return (
    <div style={overlayStyle}>
      <div
        role="dialog"
        aria-modal="true"
        style={cardStyle}
        onClick={(e) => e.stopPropagation()}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 20 }}>
          <Icon name="sparkle" size={16} />
          <h2 className="t-h3" style={{ fontWeight: 500 }}>{title}</h2>
          <div style={{ flex: 1 }} />
          <button
            type="button"
            className="btn ghost icon-only"
            onClick={onCancel}
            aria-label="Close"
          >
            <Icon name="close" size={14} />
          </button>
        </div>

        {showName && (
          <label style={{ display: 'flex', flexDirection: 'column', gap: 6, marginBottom: 18 }}>
            <span className="t-label">Name</span>
            <input
              className="input"
              value={name}
              onChange={(e) => setName(e.target.value)}
              autoFocus
              placeholder="e.g. Halden & Aoki"
            />
          </label>
        )}

        <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 12 }}>
          <span className="t-label">Match</span>
          <select
            className="input"
            value={rule.match}
            onChange={(e) => setRule((r) => ({ ...r, match: e.target.value as RuleMatch }))}
            style={{ width: 'auto', padding: '5px 10px', fontSize: 12.5 }}
          >
            <option value="all">all of the following</option>
            <option value="any">any of the following</option>
          </select>
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginBottom: 14 }}>
          {rule.predicates.map((p, i) => (
            <PredicateRow
              key={i}
              predicate={p}
              onChange={(next) => setPredicate(i, next)}
              onRemove={() => removePredicate(i)}
              canRemove={rule.predicates.length > 1}
            />
          ))}
        </div>

        <button
          type="button"
          className="btn ghost small"
          onClick={addPredicate}
          style={{ alignSelf: 'flex-start' }}
        >
          <Icon name="plus" size={12} /> Add condition
        </button>

        {error && (
          <div
            style={{
              marginTop: 16,
              padding: '10px 14px',
              border: '1px solid var(--color-accent-soft)',
              background: 'var(--color-accent-soft)',
              color: 'var(--color-accent-ink)',
              borderRadius: 2,
              fontSize: 13,
            }}
          >
            {error}
          </div>
        )}

        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 24 }}>
          <button type="button" className="btn" onClick={onCancel} disabled={busy}>
            Cancel
          </button>
          <button
            type="button"
            className="btn primary"
            disabled={!canSubmit || busy}
            onClick={() => onSubmit({ name: name.trim(), rule })}
          >
            {busy ? 'Saving…' : submitLabel}
          </button>
        </div>
      </div>
    </div>
  );
}

function PredicateRow({
  predicate,
  onChange,
  onRemove,
  canRemove,
}: {
  predicate: ShelfPredicate;
  onChange: (p: ShelfPredicate) => void;
  onRemove: () => void;
  canRemove: boolean;
}) {
  const meta = FIELDS[predicate.field];
  return (
    <div
      style={{
        display: 'grid',
        gridTemplateColumns: '120px 120px 1fr auto',
        gap: 8,
        alignItems: 'center',
      }}
    >
      <select
        className="input"
        style={{ fontSize: 13 }}
        value={predicate.field}
        onChange={(e) => {
          const nextField = e.target.value as RuleField;
          // Reset op + value so we don't end up with, e.g., op "contains"
          // on a numeric field.
          onChange(blankPredicate(nextField));
        }}
      >
        {FIELD_ORDER.map((f) => (
          <option key={f} value={f}>{FIELDS[f].label}</option>
        ))}
      </select>

      <select
        className="input"
        style={{ fontSize: 13 }}
        value={predicate.op}
        onChange={(e) => onChange({ ...predicate, op: e.target.value as RuleOp })}
      >
        {meta.ops.map((o) => (
          <option key={o.op} value={o.op}>{o.label}</option>
        ))}
      </select>

      <ValueInput predicate={predicate} onChange={onChange} />

      <button
        type="button"
        className="btn ghost icon-only"
        onClick={onRemove}
        disabled={!canRemove}
        aria-label="Remove condition"
        style={{ opacity: canRemove ? 1 : 0.4 }}
      >
        <Icon name="close" size={12} />
      </button>
    </div>
  );
}

function ValueInput({
  predicate,
  onChange,
}: {
  predicate: ShelfPredicate;
  onChange: (p: ShelfPredicate) => void;
}) {
  const meta = FIELDS[predicate.field];

  if (predicate.field === 'format') {
    return (
      <select
        className="input"
        style={{ fontSize: 13 }}
        value={String(predicate.value ?? 'EPUB')}
        onChange={(e) => onChange({ ...predicate, value: e.target.value })}
      >
        {['EPUB', 'PDF', 'CBZ', 'M4B'].map((f) => (
          <option key={f} value={f}>{f}</option>
        ))}
      </select>
    );
  }

  if (predicate.field === 'progress') {
    // Wire format is 0..1 float; render as a percent input for clarity.
    const pct = Math.round(Number(predicate.value ?? 0) * 100);
    return (
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <input
          className="input"
          type="number"
          min={0}
          max={100}
          step={1}
          value={pct}
          onChange={(e) => {
            const n = Number(e.target.value);
            const clamped = Number.isFinite(n) ? Math.max(0, Math.min(100, n)) : 0;
            onChange({ ...predicate, value: clamped / 100 });
          }}
          style={{ fontSize: 13, width: 90 }}
        />
        <span className="mono" style={{ fontSize: 11, color: 'var(--color-ink-3)' }}>%</span>
      </div>
    );
  }

  if (meta.kind === 'int') {
    return (
      <input
        className="input"
        type="number"
        value={Number(predicate.value ?? 0)}
        onChange={(e) => onChange({ ...predicate, value: Number(e.target.value) })}
        style={{ fontSize: 13 }}
      />
    );
  }

  // String + tags
  return (
    <input
      className="input"
      value={String(predicate.value ?? '')}
      onChange={(e) => onChange({ ...predicate, value: e.target.value })}
      placeholder={meta.kind === 'tags' ? 'Tag name' : 'Value'}
      style={{ fontSize: 13 }}
    />
  );
}

function predicateIsComplete(p: ShelfPredicate): boolean {
  if (typeof p.value === 'string') return p.value.trim() !== '';
  if (typeof p.value === 'number') return Number.isFinite(p.value);
  return false;
}

const overlayStyle: CSSProperties = {
  position: 'fixed',
  inset: 0,
  zIndex: 500,
  background: 'oklch(0.2 0.02 60 / 0.45)',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  padding: 24,
};

const cardStyle: CSSProperties = {
  width: '100%',
  maxWidth: 560,
  maxHeight: '90vh',
  overflowY: 'auto',
  background: 'var(--color-paper-0)',
  border: '1px solid var(--color-rule-soft)',
  padding: 24,
  borderRadius: 3,
  boxShadow: '0 24px 48px -12px oklch(0.2 0.02 60 / 0.35)',
  display: 'flex',
  flexDirection: 'column',
};
