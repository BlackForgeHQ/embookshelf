import { useState } from "react"

import { AccentPicker } from "./AccentPicker"
import { Icon } from "./Icon"
import { Notice } from "./Notice"
import { ShelfIconPicker } from "./ShelfIconPicker"
import type { ShelfAccent } from "./AccentPicker"
import type {
  RuleField,
  RuleMatch,
  RuleOp,
  ShelfPredicate,
  ShelfRule,
} from "@/api/books"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Button } from "@/components/ui/button"

// FieldMeta keeps the per-field metadata in one place: the operators the
// wire accepts and whether the value is numeric. Mirrors the validation
// at internal/model/shelf_rule.go.
type FieldMeta = {
  label: string
  ops: ReadonlyArray<{ op: RuleOp; label: string }>
  kind: "string" | "int" | "tags" | "progress"
}

const FIELDS: Record<RuleField, FieldMeta> = {
  title: {
    label: "Title",
    kind: "string",
    ops: [
      { op: "contains", label: "contains" },
      { op: "starts_with", label: "starts with" },
      { op: "eq", label: "is" },
      { op: "ne", label: "is not" },
    ],
  },
  author: {
    label: "Author",
    kind: "string",
    ops: [
      { op: "eq", label: "is" },
      { op: "ne", label: "is not" },
      { op: "contains", label: "contains" },
      { op: "starts_with", label: "starts with" },
    ],
  },
  series: {
    label: "Series",
    kind: "string",
    ops: [
      { op: "eq", label: "is" },
      { op: "contains", label: "contains" },
    ],
  },
  format: {
    label: "Format",
    kind: "string",
    ops: [
      { op: "eq", label: "is" },
      { op: "ne", label: "is not" },
    ],
  },
  tags: {
    label: "Tag",
    kind: "tags",
    ops: [{ op: "contains", label: "contains" }],
  },
  year: {
    label: "Year",
    kind: "int",
    ops: [
      { op: "eq", label: "=" },
      { op: "gte", label: "≥" },
      { op: "gt", label: ">" },
      { op: "lte", label: "≤" },
      { op: "lt", label: "<" },
      { op: "ne", label: "≠" },
    ],
  },
  rating: {
    label: "Rating",
    kind: "int",
    ops: [
      { op: "eq", label: "=" },
      { op: "gte", label: "≥" },
      { op: "gt", label: ">" },
      { op: "lte", label: "≤" },
      { op: "lt", label: "<" },
    ],
  },
  progress: {
    label: "Progress",
    kind: "progress",
    ops: [
      { op: "eq", label: "=" },
      { op: "lt", label: "<" },
      { op: "lte", label: "≤" },
      { op: "gt", label: ">" },
      { op: "gte", label: "≥" },
    ],
  },
}

const FIELD_ORDER: Array<RuleField> = [
  "author",
  "title",
  "series",
  "tags",
  "year",
  "rating",
  "format",
  "progress",
]

// nextRowID hands out a stable identity for one predicate row. Only
// uniqueness within a single open editor matters, so a counter is enough
// and keeps the component free of a crypto dependency.
let rowSeq = 0
function nextRowID(): string {
  rowSeq += 1
  return `predicate-${rowSeq}`
}

// blankPredicate seeds a new row with the first allowed op for the chosen
// field so the user never sees an invalid combo even for a split second.
function blankPredicate(field: RuleField = "author"): ShelfPredicate {
  const meta = FIELDS[field]
  // Every FieldMeta is seeded above with at least one op; the `!` is
  // load-bearing under noUncheckedIndexedAccess.
  const op = meta.ops[0]!.op
  const value: string | number =
    meta.kind === "string" || meta.kind === "tags" ? "" : 0
  return { field, op, value }
}

type Props = {
  title: string
  initialName?: string
  initialRule?: ShelfRule
  initialAccent?: ShelfAccent
  initialIcon?: string
  submitLabel: string
  error?: string | null
  busy?: boolean
  // When name is omitted (editing an existing shelf), the editor hides
  // the name field and only returns the rule.
  showName?: boolean
  onSubmit: (draft: {
    name: string
    rule: ShelfRule
    accent: ShelfAccent
    icon: string
  }) => void
  onCancel: () => void
}

// RuleEditor renders the shelf-rule form inside a shadcn Dialog. Reusable
// for create + edit — the caller controls whether the name field is shown.
export function RuleEditor({
  title,
  initialName,
  initialRule,
  initialAccent = "accent",
  initialIcon = "sparkles",
  submitLabel,
  error,
  busy,
  showName = true,
  onSubmit,
  onCancel,
}: Props) {
  const [name, setName] = useState(initialName ?? "")
  const [accent, setAccent] = useState<ShelfAccent>(initialAccent)
  const [icon, setIcon] = useState<string>(initialIcon)
  const [rule, setRule] = useState<ShelfRule>(
    () => initialRule ?? { match: "all", predicates: [blankPredicate()] }
  )
  // Row identity travels beside the predicates rather than being their
  // position. removePredicate filters by index, so every later row shifts
  // down after a delete: keyed by index, React would reuse each
  // PredicateRow instance for a different predicate and carry that row's
  // internal state — focus, a half-typed value — onto the wrong rule.
  //
  // The id stays out of ShelfPredicate because that type is the wire
  // shape the API stores; this is a view concern with no business on it.
  const [rowIDs, setRowIDs] = useState<Array<string>>(() =>
    (initialRule?.predicates ?? [null]).map(() => nextRowID())
  )

  const setPredicate = (i: number, next: ShelfPredicate) => {
    setRule((r) => {
      const copy = r.predicates.slice()
      copy[i] = next
      return { ...r, predicates: copy }
    })
  }
  const removePredicate = (i: number) => {
    setRule((r) => ({
      ...r,
      predicates: r.predicates.filter((_, idx) => idx !== i),
    }))
    setRowIDs((ids) => ids.filter((_, idx) => idx !== i))
  }
  const addPredicate = () => {
    setRule((r) => ({ ...r, predicates: [...r.predicates, blankPredicate()] }))
    setRowIDs((ids) => [...ids, nextRowID()])
  }

  const canSubmit =
    (!showName || name.trim() !== "") &&
    rule.predicates.length > 0 &&
    rule.predicates.every(predicateIsComplete)

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onCancel()
      }}
    >
      <DialogContent className="sm:max-w-[560px]">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Icon name="sparkle" size={16} />
            <span>{title}</span>
          </DialogTitle>
        </DialogHeader>

        {showName && (
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="rule-name">Name</Label>
            <Input
              id="rule-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              autoFocus
              placeholder="e.g. Halden & Aoki"
            />
          </div>
        )}

        <div className="flex flex-col gap-2">
          <Label>Accent</Label>
          <AccentPicker value={accent} onChange={setAccent} />
        </div>

        <div className="flex flex-col gap-2">
          <Label>Icon</Label>
          <ShelfIconPicker value={icon} onChange={setIcon} />
        </div>

        <div className="flex items-center gap-2.5">
          <span className="t-label">Match</span>
          <Select
            value={rule.match}
            onValueChange={(v) =>
              setRule((r) => ({ ...r, match: v as RuleMatch }))
            }
          >
            <SelectTrigger className="w-[200px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">all of the following</SelectItem>
              <SelectItem value="any">any of the following</SelectItem>
            </SelectContent>
          </Select>
        </div>

        <div className="flex flex-col gap-2">
          {rule.predicates.map((p, i) => (
            <PredicateRow
              key={rowIDs[i]}
              predicate={p}
              onChange={(next) => setPredicate(i, next)}
              onRemove={() => removePredicate(i)}
              canRemove={rule.predicates.length > 1}
            />
          ))}
        </div>

        <Button
          variant="ghost"
          size="sm"
          onClick={addPredicate}
          className="self-start"
        >
          <Icon name="plus" size={12} /> Add condition
        </Button>

        {error && <Notice>{error}</Notice>}

        <div className="flex justify-end gap-2">
          <Button variant="outline" onClick={onCancel} disabled={busy}>
            Cancel
          </Button>
          <Button
            disabled={!canSubmit || busy}
            onClick={() => onSubmit({ name: name.trim(), rule, accent, icon })}
          >
            {busy ? "Saving…" : submitLabel}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}

function PredicateRow({
  predicate,
  onChange,
  onRemove,
  canRemove,
}: {
  predicate: ShelfPredicate
  onChange: (p: ShelfPredicate) => void
  onRemove: () => void
  canRemove: boolean
}) {
  const meta = FIELDS[predicate.field]
  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "140px 140px 1fr auto",
        gap: 8,
        alignItems: "center",
      }}
    >
      <Select
        value={predicate.field}
        onValueChange={(v) => onChange(blankPredicate(v as RuleField))}
      >
        <SelectTrigger className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {FIELD_ORDER.map((f) => (
            <SelectItem key={f} value={f}>
              {FIELDS[f].label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>

      <Select
        value={predicate.op}
        onValueChange={(v) => onChange({ ...predicate, op: v as RuleOp })}
      >
        <SelectTrigger className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {meta.ops.map((o) => (
            <SelectItem key={o.op} value={o.op}>
              {o.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>

      <ValueInput predicate={predicate} onChange={onChange} />

      <Button
        variant="ghost"
        size="icon-sm"
        onClick={onRemove}
        disabled={!canRemove}
        aria-label="Remove condition"
      >
        <Icon name="close" size={12} />
      </Button>
    </div>
  )
}

function ValueInput({
  predicate,
  onChange,
}: {
  predicate: ShelfPredicate
  onChange: (p: ShelfPredicate) => void
}) {
  const meta = FIELDS[predicate.field]

  if (predicate.field === "format") {
    return (
      <Select
        value={String(predicate.value || "EPUB")}
        onValueChange={(v) => onChange({ ...predicate, value: v })}
      >
        <SelectTrigger className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {["EPUB", "PDF", "CBZ", "M4B"].map((f) => (
            <SelectItem key={f} value={f}>
              {f}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    )
  }

  if (predicate.field === "progress") {
    // Wire format is 0..1 float; render as a percent input for clarity.
    const pct = Math.round(Number(predicate.value) * 100)
    return (
      <div className="flex items-center gap-2">
        <Input
          type="number"
          min={0}
          max={100}
          step={1}
          value={pct}
          onChange={(e) => {
            const n = Number(e.target.value)
            const clamped = Number.isFinite(n)
              ? Math.max(0, Math.min(100, n))
              : 0
            onChange({ ...predicate, value: clamped / 100 })
          }}
          className="w-24"
        />
        <span className="mono text-[11px] text-muted-foreground">%</span>
      </div>
    )
  }

  if (meta.kind === "int") {
    return (
      <Input
        type="number"
        value={Number(predicate.value)}
        onChange={(e) =>
          onChange({ ...predicate, value: Number(e.target.value) })
        }
      />
    )
  }

  // String + tags
  return (
    <Input
      value={String(predicate.value)}
      onChange={(e) => onChange({ ...predicate, value: e.target.value })}
      placeholder={meta.kind === "tags" ? "Tag name" : "Value"}
    />
  )
}

function predicateIsComplete(p: ShelfPredicate): boolean {
  if (typeof p.value === "string") return p.value.trim() !== ""
  if (typeof p.value === "number") return Number.isFinite(p.value)
  return false
}
