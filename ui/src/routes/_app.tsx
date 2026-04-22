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
  // staleTime is generous — version + mode don't change at runtime, so
  // the first fetch per session is enough.
  const instance = useQuery({
    queryKey: instanceSummaryQueryKey,
    queryFn: fetchInstanceSummary,
    staleTime: 60 * 60_000,
  });

  const version = instance.data?.version ?? '…';
  const mode = instance.data?.diskMode ?? '…';

  return (
    <div className="status-bar">
      <span className="status-dot green" />
      <span>embookshelf {version} · {mode} mode</span>
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
