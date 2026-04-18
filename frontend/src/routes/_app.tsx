import { Outlet, createFileRoute } from '@tanstack/react-router';

import { Sidebar } from '@/components/Sidebar';

// Pathless layout: every in-app screen (dashboard, library, book detail,
// bookdrop, settings, notebook, metadata editor) renders inside this shell
// with the sidebar on the left and the persistent status bar at the bottom.
// The reader is intentionally OUTSIDE this layout — it's a full-screen
// takeover.
export const Route = createFileRoute('/_app')({
  component: AppLayout,
});

function AppLayout() {
  return (
    <div
      style={{
        display: 'grid',
        gridTemplateColumns: '240px 1fr',
        height: '100vh',
        overflow: 'hidden',
        background: 'var(--color-paper-1)',
      }}
    >
      <Sidebar />
      <main className="main">
        <Outlet />
        <StatusBar />
      </main>
    </div>
  );
}

function StatusBar() {
  return (
    <div className="status-bar">
      <span className="status-dot green" />
      <span>embookshelf 1.4.2 · LOCAL mode</span>
      <span>·</span>
      <span>3 libraries · 1,202 volumes</span>
      <span>·</span>
      <span>PostgreSQL · connected</span>
      <div style={{ flex: 1 }} />
      <span>2 background tasks</span>
      <span>·</span>
      <span>⌘K to search</span>
    </div>
  );
}
