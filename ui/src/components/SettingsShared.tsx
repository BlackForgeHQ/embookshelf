import type { ReactNode } from 'react';

import { Icon } from '@/components/Icon';
import {
  Avatar as ShadcnAvatar,
  AvatarFallback,
} from '@/components/ui/avatar';
import { Button } from '@/components/ui/button';
import {
  Select as ShadcnSelect,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';

// Shared primitives used across the /settings (per-user) and /admin
// (global) pages. Extracted so the two routes can import them without
// duplicating 100 lines each.

export function Card({ children }: { children: ReactNode }) {
  return (
    <div
      style={{
        padding: 16,
        border: '1px solid var(--color-rule-soft)',
        background: 'var(--color-paper-0)',
        marginBottom: 0,
        display: 'flex',
        flexDirection: 'column',
        gap: 14,
      }}
    >
      {children}
    </div>
  );
}

export function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      <span className="t-label">{label}</span>
      {children}
    </label>
  );
}

export function Select({
  value,
  onChange,
  options,
  disabled,
  triggerClassName = 'w-full',
}: {
  value: string;
  onChange: (v: string) => void;
  options: { value: string; label: string }[];
  disabled?: boolean;
  // Callers in inline rows (e.g. Users & roles) should override to
  // `w-[110px]` or similar so the trigger doesn't swallow the flex slot.
  triggerClassName?: string;
}) {
  return (
    <ShadcnSelect value={value} onValueChange={onChange} disabled={disabled}>
      <SelectTrigger className={triggerClassName}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {options.map((o) => (
          <SelectItem key={o.value} value={o.value}>
            {o.label}
          </SelectItem>
        ))}
      </SelectContent>
    </ShadcnSelect>
  );
}

export function Toggle({
  label,
  hint,
  checked,
  onChange,
}: {
  label: string;
  hint?: string;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <label
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 12,
        padding: '8px 0',
        borderTop: '1px dashed var(--color-rule-soft)',
        cursor: 'pointer',
      }}
    >
      <Switch checked={checked} onCheckedChange={onChange} />
      <div style={{ flex: 1 }}>
        <div style={{ fontSize: 13.5 }}>{label}</div>
        {hint && <div className="t-small" style={{ fontSize: 11.5 }}>{hint}</div>}
      </div>
    </label>
  );
}

export function DefRow({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div
      style={{
        display: 'flex',
        gap: 12,
        padding: '6px 0',
        alignItems: 'baseline',
      }}
    >
      <div className="t-label" style={{ width: 160, flexShrink: 0 }}>
        {label}
      </div>
      <div style={{ fontSize: 13.5, flex: 1, minWidth: 0, wordBreak: 'break-word' }}>
        {value}
      </div>
    </div>
  );
}

export function Notice({
  kind,
  children,
  onClose,
}: {
  kind: 'ok' | 'err';
  children: ReactNode;
  onClose?: () => void;
}) {
  const accent = kind === 'ok' ? 'oklch(0.58 0.12 140)' : 'var(--color-accent-ink)';
  return (
    <div
      style={{
        padding: '10px 14px',
        border: `1px solid ${accent}`,
        background: 'var(--color-paper-0)',
        color: accent,
        borderRadius: 2,
        fontSize: 13,
        marginBottom: 20,
        display: 'flex',
        alignItems: 'center',
        gap: 10,
      }}
    >
      <span style={{ flex: 1 }}>{children}</span>
      {onClose && (
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          onClick={onClose}
          aria-label="Dismiss"
        >
          <Icon name="close" size={11} />
        </Button>
      )}
    </div>
  );
}

export function Avatar({ initials, size = 48 }: { initials?: string; size?: number }) {
  // Map the few call-site sizes (48 for Account header, 32 for user rows)
  // onto shadcn's `size` variants. Callers that pass an arbitrary number
  // still get a correctly-sized avatar via the inline style override.
  const preset = size <= 28 ? 'sm' : size >= 40 ? 'lg' : 'default';
  return (
    <ShadcnAvatar
      size={preset}
      style={{ width: size, height: size }}
      className="shrink-0"
    >
      <AvatarFallback
        className="bg-(--color-editorial-accent) text-(--color-paper-0) font-serif font-medium"
        style={{ fontSize: Math.round(size * 0.375) }}
      >
        {initials ?? '…'}
      </AvatarFallback>
    </ShadcnAvatar>
  );
}

export function AdminGate({ label }: { label: string }) {
  return (
    <>
      <h2 className="t-h2" style={{ marginBottom: 24 }}>{label}</h2>
      <div className="t-small" style={{ fontStyle: 'italic' }}>
        {label} are admin-only.
      </div>
    </>
  );
}

// SettingsShell is the two-column nav layout shared by /settings and
// /admin: left nav of section labels, right content slot. Callers
// control the section list, active section, and the content render.
export function SettingsShell<K extends string>({
  sections,
  active,
  onSelect,
  isAdmin,
  children,
}: {
  sections: ReadonlyArray<{ key: K; label: string; adminOnly?: boolean }>;
  active: K;
  onSelect: (key: K) => void;
  isAdmin: boolean;
  children: ReactNode;
}) {
  return (
    <div
      style={{
        padding: '28px 32px',
        display: 'grid',
        gridTemplateColumns: '220px 1fr',
        gap: 40,
        maxWidth: 960,
      }}
    >
      <nav style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
        {sections.map((s) => {
          const selected = s.key === active;
          const gated = s.adminOnly && !isAdmin;
          return (
            <button
              key={s.key}
              type="button"
              onClick={() => onSelect(s.key)}
              disabled={gated}
              style={{
                padding: '8px 12px',
                textAlign: 'left',
                background: selected ? 'var(--color-paper-3)' : 'transparent',
                border: 'none',
                cursor: gated ? 'default' : 'pointer',
                fontFamily: 'var(--font-serif)',
                fontSize: 13.5,
                borderLeft: selected
                  ? '2px solid var(--color-accent)'
                  : '2px solid transparent',
                color: gated
                  ? 'var(--color-ink-4)'
                  : selected
                    ? 'var(--color-ink-1)'
                    : 'var(--color-ink-2)',
                opacity: gated ? 0.6 : 1,
              }}
              title={gated ? 'Admin-only' : undefined}
            >
              {s.label}
            </button>
          );
        })}
      </nav>

      <div style={{ maxWidth: 640 }}>{children}</div>
    </div>
  );
}
