import { useQuery } from '@tanstack/react-query';
import { Outlet, createFileRoute, redirect } from '@tanstack/react-router';

import { fetchMe, meQueryKey } from '@/api/auth';
import { useRealtime } from '@/api/realtime';
import {
  fetchInstanceSummary,
  instanceSummaryQueryKey,
} from '@/api/settings';
import { AppSidebar } from '@/components/Sidebar';
import { SidebarInset, SidebarProvider } from '@/components/ui/sidebar';
import { TooltipProvider } from '@/components/ui/tooltip';

// Pathless layout: every in-app screen (dashboard, library, book detail,
// bookdrop, settings, notebook, metadata editor) renders inside this shell
// with the sidebar on the left and the persistent status bar at the bottom.
// The reader is intentionally OUTSIDE this layout — it's a full-screen
// takeover.
//
// beforeLoad enforces authentication: any in-app route redirects to /login
// when no session exists. The me query is cached in the router's
// QueryClient so sibling routes reuse it without refetching.
export const Route = createFileRoute('/_app')({
  beforeLoad: async ({ context, location }) => {
    const me = await context.queryClient.ensureQueryData({
      queryKey: meQueryKey,
      queryFn: fetchMe,
      staleTime: 60_000,
    });
    if (!me) {
      throw redirect({
        to: '/login',
        search: { next: location.href },
      });
    }
    return { me };
  },
  component: AppLayout,
});

function AppLayout() {
  // Only runs inside the authed shell — unauth'd visitors hit the
  // beforeLoad redirect above and never mount this component, so the
  // EventSource never fires without a valid session cookie.
  useRealtime();

  // SidebarProvider owns the expanded/collapsed state and persists it to
  // a cookie so the sidebar survives page reloads. TooltipProvider is
  // required by shadcn's SidebarMenuButton tooltips when the sidebar is
  // in icon mode.
  return (
    <TooltipProvider delayDuration={100}>
      <SidebarProvider className="h-screen overflow-hidden">
        <AppSidebar />
        <SidebarInset className="min-h-0 overflow-hidden">
          <div className="main-content">
            <Outlet />
          </div>
          <StatusBar />
        </SidebarInset>
      </SidebarProvider>
    </TooltipProvider>
  );
}

function StatusBar() {
  // Counts come from /api/v1/instance alongside version+mode; the endpoint
  // is cheap (single COUNT(*) per library) so a 5-minute staleTime keeps
  // the bar fresh without polling.
  const instance = useQuery({
    queryKey: instanceSummaryQueryKey,
    queryFn: fetchInstanceSummary,
    staleTime: 5 * 60_000,
  });

  const version = instance.data?.version ?? '…';
  const mode = instance.data?.diskMode ?? '…';
  const libCount = instance.data?.libraries;
  const bookCount = instance.data?.books;

  const catalog =
    libCount != null && bookCount != null
      ? `${libCount} ${libCount === 1 ? 'library' : 'libraries'} · ${bookCount.toLocaleString()} ${bookCount === 1 ? 'volume' : 'volumes'}`
      : null;

  return (
    <div className="status-bar">
      <span className="status-dot green" />
      <span>embookshelf {version} · {mode} mode</span>
      {catalog && (
        <>
          <span>·</span>
          <span>{catalog}</span>
        </>
      )}
      <span>·</span>
      <span>PostgreSQL · connected</span>
      <div style={{ flex: 1 }} />
      <span>⌘K to search</span>
    </div>
  );
}
