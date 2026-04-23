import { useEffect, useState } from 'react';
import {
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { toast } from 'sonner';

import type { ApiError } from '@/api/client';
import {
  defaultNamingPatternQueryKey,
  fetchDefaultNamingPattern,
  fetchSettingsLibraries,
  previewNamingPattern,
  settingsLibrariesQueryKey,
  updateDefaultNamingPattern,
  updateLibraryNamingPattern,
  type SettingsLibrary,
} from '@/api/settings';
import { AdminGate, Card } from '@/components/SettingsShared';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';

// ---------------------------------------------------------------------------
// Reference docs (kept as data so a test can enumerate them)
// ---------------------------------------------------------------------------

export const PLACEHOLDER_REFERENCE: ReadonlyArray<{ token: string; desc: string }> = [
  { token: '{title}', desc: 'Book title' },
  { token: '{subtitle}', desc: 'Book subtitle' },
  { token: '{authors}', desc: 'Author(s)' },
  { token: '{year}', desc: 'Publication year' },
  { token: '{series}', desc: 'Series name' },
  { token: '{seriesIndex}', desc: 'Series index' },
  { token: '{language}', desc: 'Language code' },
  { token: '{publisher}', desc: 'Publisher' },
  { token: '{isbn}', desc: 'ISBN number' },
  { token: '{currentFilename}', desc: 'Original filename' },
];

export const MODIFIER_REFERENCE: ReadonlyArray<{ token: string; desc: string }> = [
  { token: '{authors:first}', desc: 'First author only' },
  { token: '{authors:sort}', desc: '"Last, First" format' },
  { token: '{title:initial}', desc: 'First letter, uppercased' },
  { token: '{title:upper}', desc: 'UPPERCASE' },
  { token: '{title:lower}', desc: 'lowercase' },
];

type ExampleBlock = {
  label: string;
  pattern: string;
  // Each row is (sample caption → expected output). The admin UI renders
  // these verbatim and the Go reference_test.go asserts they match.
  rows: ReadonlyArray<{ caption: string; output: string }>;
};

export const BASIC_EXAMPLES: ReadonlyArray<ExampleBlock> = [
  {
    label: 'Basic pattern',
    pattern: '{authors} - {title}',
    rows: [{ caption: '', output: 'Patrick Rothfuss - The Name of the Wind.epub' }],
  },
  {
    label: 'Series in folder',
    pattern: '{authors}/{series}/{seriesIndex} - {title}',
    rows: [
      {
        caption: '',
        output: 'Patrick Rothfuss/The Kingkiller Chronicle/01 - The Name of the Wind.epub',
      },
    ],
  },
  {
    label: 'Title + subtitle',
    pattern: '{title}: {subtitle}',
    rows: [{ caption: '', output: 'The Name of the Wind Special Edition.epub' }],
  },
  {
    label: 'Absolute path',
    pattern: '/{authors}/{title}',
    rows: [{ caption: '', output: 'Patrick Rothfuss/The Name of the Wind.epub' }],
  },
  {
    label: 'Folder only',
    pattern: '{title}/',
    rows: [{ caption: '', output: 'The Name of the Wind/the_name_of_the_wind.epub' }],
  },
  {
    label: 'Year prefix',
    pattern: '({year}) {title}',
    rows: [{ caption: '', output: '(2007) The Name of the Wind.epub' }],
  },
];

export const CONDITIONAL_EXAMPLES: ReadonlyArray<ExampleBlock> = [
  {
    label: 'Optional block',
    pattern: '<{seriesIndex}. >{title}',
    rows: [
      { caption: 'With index', output: '01. The Name of the Wind.epub' },
      { caption: 'Without', output: 'Project Hail Mary.epub' },
    ],
  },
  {
    label: 'Subtitle conditional',
    pattern: '{title}<: {subtitle}>',
    rows: [
      { caption: 'With subtitle', output: 'The Name of the Wind Special Edition.epub' },
      { caption: 'Without', output: 'Project Hail Mary.epub' },
    ],
  },
  {
    label: 'Multiple optionals',
    pattern: '{authors}/<{series}/><{seriesIndex}. >{title}< ({year})>',
    rows: [
      {
        caption: 'Full metadata',
        output:
          'Patrick Rothfuss/The Kingkiller Chronicle/01. The Name of the Wind (2007).epub',
      },
      { caption: 'Partial', output: 'Andy Weir/Project Hail Mary (2021).epub' },
    ],
  },
  {
    label: 'Else clause',
    pattern: '<{series}|Standalone>/{title}',
    rows: [
      {
        caption: 'With series',
        output: 'The Kingkiller Chronicle/The Name of the Wind.epub',
      },
      { caption: 'Without', output: 'Standalone/Project Hail Mary.epub' },
    ],
  },
  {
    label: 'Else fallback',
    pattern: '<{series}/{seriesIndex} - {title}|{title}>',
    rows: [
      {
        caption: 'Series path',
        output: 'The Kingkiller Chronicle/01 - The Name of the Wind.epub',
      },
      { caption: 'Fallback', output: 'Project Hail Mary.epub' },
    ],
  },
  {
    label: 'Else + modifier',
    pattern: '<{series}|{authors:sort}>/{title}',
    rows: [
      {
        caption: 'Series path',
        output: 'The Kingkiller Chronicle/The Name of the Wind.epub',
      },
      { caption: 'Sort fallback', output: 'Weir, Andy/Project Hail Mary.epub' },
    ],
  },
];

export const MODIFIER_EXAMPLES: ReadonlyArray<ExampleBlock> = [
  {
    label: 'Author sort',
    pattern: '{authors:sort}/{title}',
    rows: [{ caption: '', output: 'Rothfuss, Patrick/The Name of the Wind.epub' }],
  },
  {
    label: 'Author initial',
    pattern: '{authors:initial}/{authors:sort}/{title}',
    rows: [
      { caption: '', output: 'R/Rothfuss, Patrick/The Name of the Wind.epub' },
    ],
  },
  {
    label: 'First author',
    pattern: '{authors:first}/{title}',
    rows: [{ caption: '', output: 'Patrick Rothfuss/The Name of the Wind.epub' }],
  },
  {
    label: 'Uppercase',
    pattern: '{title:upper}',
    rows: [{ caption: '', output: 'THE NAME OF THE WIND.epub' }],
  },
  {
    label: 'Lowercase',
    pattern: '{title:lower}',
    rows: [{ caption: '', output: 'the name of the wind.epub' }],
  },
  {
    label: 'Letter folder',
    pattern: '{title:initial}/{authors}/{title}',
    rows: [{ caption: '', output: 'T/Patrick Rothfuss/The Name of the Wind.epub' }],
  },
  {
    label: 'Combined modifiers',
    pattern: '{authors:sort} - {title:lower}',
    rows: [{ caption: '', output: 'Rothfuss, Patrick - the name of the wind.epub' }],
  },
];

// ---------------------------------------------------------------------------
// Panel
// ---------------------------------------------------------------------------

export function NamingPatternsPanel({ isAdmin }: { isAdmin: boolean }) {
  const queryClient = useQueryClient();

  const defaultQuery = useQuery({
    queryKey: defaultNamingPatternQueryKey,
    queryFn: fetchDefaultNamingPattern,
    enabled: isAdmin,
  });

  const libraries = useQuery({
    queryKey: settingsLibrariesQueryKey,
    queryFn: fetchSettingsLibraries,
    enabled: isAdmin,
  });

  const invalidateLibraries = () => {
    queryClient.invalidateQueries({ queryKey: settingsLibrariesQueryKey });
    queryClient.invalidateQueries({ queryKey: ['libraries'] });
  };

  const defaultMut = useMutation({
    mutationFn: (pattern: string) => updateDefaultNamingPattern(pattern),
    onSuccess: (saved) => {
      queryClient.setQueryData(defaultNamingPatternQueryKey, saved);
      toast.success('Default pattern saved.');
    },
    onError: (e) => toast.error((e as unknown as ApiError).message),
  });

  const libraryMut = useMutation({
    mutationFn: (args: { id: string; pattern: string | null }) =>
      updateLibraryNamingPattern(args.id, args.pattern),
    onSuccess: () => {
      invalidateLibraries();
      toast.success('Library pattern saved.');
    },
    onError: (e) => toast.error((e as unknown as ApiError).message),
  });

  if (!isAdmin) return <AdminGate label="File naming patterns" />;

  return (
    <>
      <h2 className="t-h2" style={{ marginBottom: 8 }}>File naming patterns</h2>
      <p className="t-small" style={{ marginBottom: 24, fontStyle: 'italic' }}>
        Patterns decide where an approved BookDrop file lands on disk and how
        it&apos;s renamed. Libraries use their own pattern when set; otherwise
        they fall back to the default below. An empty default means
        &ldquo;keep the original filename&rdquo;.
      </p>

      <h3 className="t-h3" style={{ marginTop: 0, marginBottom: 8 }}>Default pattern</h3>
      <Card>
        <PatternEditor
          label="Instance default"
          saved={defaultQuery.data ?? ''}
          busy={defaultMut.isPending}
          loading={defaultQuery.isLoading}
          onSave={(pattern) => defaultMut.mutate(pattern)}
          placeholder="{authors}/<{series}/><{seriesIndex}. >{title}< ({year})>"
        />
      </Card>

      <h3 className="t-h3" style={{ marginTop: 24, marginBottom: 8 }}>Per-library patterns</h3>
      {libraries.isLoading && (
        <div className="t-small" style={{ fontStyle: 'italic' }}>Loading libraries…</div>
      )}
      {libraries.data?.length === 0 && (
        <div
          className="t-small"
          style={{
            fontStyle: 'italic',
            padding: '12px 14px',
            border: '1px dashed var(--color-rule-soft)',
            background: 'var(--color-paper-2)',
          }}
        >
          No libraries yet. Create one in the Libraries section first.
        </div>
      )}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
        {(libraries.data ?? []).map((lib) => (
          <LibraryPatternRow
            key={lib.id}
            library={lib}
            busy={libraryMut.isPending}
            onSave={(pattern) =>
              libraryMut.mutate({ id: lib.id, pattern: pattern === '' ? null : pattern })
            }
          />
        ))}
      </div>

      <h3 className="t-h3" style={{ marginTop: 32, marginBottom: 8 }}>
        Placeholders reference
      </h3>
      <p className="t-small" style={{ marginBottom: 16, fontStyle: 'italic' }}>
        Use placeholders to dynamically insert metadata into file names and
        folder paths.
      </p>
      <Card>
        <ReferenceGrid items={PLACEHOLDER_REFERENCE} />
      </Card>

      <h3 className="t-h3" style={{ marginTop: 24, marginBottom: 8 }}>Optional blocks</h3>
      <p className="t-small" style={{ marginBottom: 16, fontStyle: 'italic' }}>
        Wrap parts in angle brackets <span className="mono">&lt;…&gt;</span> to
        make them conditional. If any placeholder in the block has no value,
        the entire block is omitted.
      </p>
      <Card>
        <ExampleRow
          pattern="<{seriesIndex} - >{title}"
          rows={[
            { caption: 'With index', output: '01 - Dune' },
            { caption: 'Without', output: 'Dune' },
          ]}
        />
      </Card>

      <h3 className="t-h3" style={{ marginTop: 24, marginBottom: 8 }}>Else clause</h3>
      <p className="t-small" style={{ marginBottom: 16, fontStyle: 'italic' }}>
        Add a fallback after a pipe <span className="mono">|</span> inside an
        optional block. If the primary side has missing values, the fallback is
        used instead.
      </p>
      <Card>
        <ExampleRow
          pattern="<{series}|Standalone>/{title}"
          rows={[
            {
              caption: 'With series',
              output: 'The Kingkiller Chronicle/The Name of the Wind',
            },
            { caption: 'Without', output: 'Standalone/The Name of the Wind' },
          ]}
        />
      </Card>

      <h3 className="t-h3" style={{ marginTop: 24, marginBottom: 8 }}>Value modifiers</h3>
      <p className="t-small" style={{ marginBottom: 16, fontStyle: 'italic' }}>
        Append <span className="mono">:modifier</span> to a placeholder to
        transform its value. Example:{' '}
        <span className="mono">{'{authors:sort}'}</span>.
      </p>
      <Card>
        <ReferenceGrid items={MODIFIER_REFERENCE} />
      </Card>

      <h3 className="t-h3" style={{ marginTop: 32, marginBottom: 8 }}>Pattern examples</h3>
      <p className="t-small" style={{ marginBottom: 16, fontStyle: 'italic' }}>
        Sample patterns rendered against the two reference books below.
      </p>

      <div
        style={{
          display: 'grid',
          gridTemplateColumns: '1fr 1fr',
          gap: 12,
          marginBottom: 18,
        }}
      >
        <Card>
          <div className="t-label" style={{ marginBottom: 6 }}>Full metadata</div>
          <ReferenceGrid
            items={[
              { token: 'title', desc: 'The Name of the Wind' },
              { token: 'subtitle', desc: 'Special Edition' },
              { token: 'authors', desc: 'Patrick Rothfuss' },
              { token: 'series', desc: 'The Kingkiller Chronicle' },
              { token: 'seriesIndex', desc: '01' },
              { token: 'year', desc: '2007' },
            ]}
          />
        </Card>
        <Card>
          <div className="t-label" style={{ marginBottom: 6 }}>Partial metadata</div>
          <ReferenceGrid
            items={[
              { token: 'title', desc: 'Project Hail Mary' },
              { token: 'subtitle', desc: 'not set' },
              { token: 'authors', desc: 'Andy Weir' },
              { token: 'year', desc: '2021' },
              { token: 'series', desc: 'not set' },
              { token: 'seriesIndex', desc: 'not set' },
            ]}
          />
        </Card>
      </div>

      <ExampleGroup title="Basic patterns" blocks={BASIC_EXAMPLES} />
      <ExampleGroup title="Conditional blocks" blocks={CONDITIONAL_EXAMPLES} />
      <ExampleGroup title="Value modifiers" blocks={MODIFIER_EXAMPLES} />
    </>
  );
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

function LibraryPatternRow({
  library,
  busy,
  onSave,
}: {
  library: SettingsLibrary;
  busy: boolean;
  onSave: (pattern: string) => void;
}) {
  return (
    <Card>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, marginBottom: 4 }}>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ fontSize: 14, fontWeight: 500 }}>{library.name}</div>
          <div
            className="mono"
            style={{
              fontSize: 11.5,
              color: 'var(--color-ink-3)',
              wordBreak: 'break-all',
            }}
          >
            {library.path || '(empty path)'}
          </div>
        </div>
        <span
          className="t-micro"
          style={{
            color: library.fileNamingPattern
              ? 'var(--color-accent-ink)'
              : 'var(--color-ink-3)',
          }}
        >
          {library.fileNamingPattern ? 'custom' : 'uses default'}
        </span>
      </div>
      <PatternEditor
        label={library.name}
        saved={library.fileNamingPattern ?? ''}
        busy={busy}
        onSave={onSave}
        placeholder="leave blank to use the default"
        allowClear
      />
    </Card>
  );
}

function PatternEditor({
  label,
  saved,
  busy,
  loading,
  onSave,
  placeholder,
  allowClear = false,
}: {
  label: string;
  saved: string;
  busy: boolean;
  loading?: boolean;
  onSave: (pattern: string) => void;
  placeholder?: string;
  allowClear?: boolean;
}) {
  const [draft, setDraft] = useState(saved);

  useEffect(() => {
    setDraft(saved);
  }, [saved]);

  const trimmed = draft.trim();
  const dirty = trimmed !== saved;

  const [debounced, setDebounced] = useState(trimmed);
  useEffect(() => {
    const handle = window.setTimeout(() => setDebounced(trimmed), 250);
    return () => window.clearTimeout(handle);
  }, [trimmed]);

  const preview = useQuery({
    queryKey: ['settings', 'pattern-preview', debounced],
    queryFn: () => previewNamingPattern(debounced),
    enabled: debounced !== '',
    staleTime: 60_000,
  });

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      <Input
        aria-label={`Pattern for ${label}`}
        placeholder={placeholder}
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        className="mono text-[12.5px]"
        disabled={loading || busy}
      />
      {debounced !== '' && (
        <div
          className="mono"
          style={{
            padding: '8px 12px',
            background: 'var(--color-paper-2)',
            borderRadius: 2,
            fontSize: 12,
            color: 'var(--color-ink-2)',
            wordBreak: 'break-all',
          }}
        >
          <span className="t-micro" style={{ marginRight: 8 }}>Preview</span>
          {preview.data ?? (preview.isLoading ? 'Resolving…' : '')}
        </div>
      )}
      <div style={{ display: 'flex', gap: 8 }}>
        <Button
          type="button"
          size="sm"
          disabled={!dirty || busy}
          onClick={() => onSave(trimmed)}
        >
          {busy ? 'Saving…' : 'Save'}
        </Button>
        {allowClear && saved !== '' && (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            disabled={busy}
            onClick={() => {
              setDraft('');
              onSave('');
            }}
          >
            Clear override
          </Button>
        )}
      </div>
    </div>
  );
}

function ReferenceGrid({
  items,
}: {
  items: ReadonlyArray<{ token: string; desc: string }>;
}) {
  return (
    <div
      style={{
        display: 'grid',
        gridTemplateColumns: '220px 1fr',
        rowGap: 4,
        columnGap: 14,
        fontSize: 13,
      }}
    >
      {items.map((it) => (
        <Row key={it.token} token={it.token} desc={it.desc} />
      ))}
    </div>
  );
}

function Row({ token, desc }: { token: string; desc: string }) {
  return (
    <>
      <span className="mono" style={{ fontSize: 12, color: 'var(--color-ink-2)' }}>
        {token}
      </span>
      <span>{desc}</span>
    </>
  );
}

function ExampleGroup({
  title,
  blocks,
}: {
  title: string;
  blocks: ReadonlyArray<ExampleBlock>;
}) {
  return (
    <>
      <h4
        className="t-label"
        style={{ marginTop: 16, marginBottom: 8, letterSpacing: 0.5 }}
      >
        {title}
      </h4>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginBottom: 8 }}>
        {blocks.map((b) => (
          <div
            key={b.label}
            style={{
              border: '1px solid var(--color-rule-soft)',
              background: 'var(--color-paper-0)',
              padding: '10px 12px',
            }}
          >
            <div style={{ fontSize: 13, fontWeight: 500, marginBottom: 6 }}>{b.label}</div>
            <ExampleRow pattern={b.pattern} rows={b.rows} />
          </div>
        ))}
      </div>
    </>
  );
}

function ExampleRow({
  pattern,
  rows,
}: {
  pattern: string;
  rows: ReadonlyArray<{ caption: string; output: string }>;
}) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <div
        className="mono"
        style={{
          fontSize: 12,
          padding: '6px 8px',
          background: 'var(--color-paper-2)',
          borderRadius: 2,
          wordBreak: 'break-all',
        }}
      >
        {pattern}
      </div>
      {rows.map((r, i) => (
        <div
          key={i}
          style={{
            display: 'grid',
            gridTemplateColumns: '110px 1fr',
            columnGap: 10,
            fontSize: 12.5,
            padding: '2px 4px',
          }}
        >
          <span
            className="t-micro"
            style={{ color: 'var(--color-ink-3)', alignSelf: 'center' }}
          >
            {r.caption}
          </span>
          <span className="mono" style={{ wordBreak: 'break-all' }}>
            {r.output}
          </span>
        </div>
      ))}
    </div>
  );
}
