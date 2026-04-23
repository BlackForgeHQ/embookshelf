import { useEffect, useState, type FormEvent } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  createFileRoute,
  redirect,
  useNavigate,
} from '@tanstack/react-router';

import {
  fetchMe,
  login,
  meQueryKey,
  oidcConfig,
  oidcConfigQueryKey,
  signup,
  signupStatus,
} from '@/api/auth';
import type { ApiError } from '@/api/client';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';

type LoginSearch = {
  next?: string;
  mode?: 'login' | 'signup';
  oidcError?: string;
  local?: boolean;
};

// OIDC error codes the callback handler may redirect with. Kept in one
// place so new failure paths pick up a friendly message automatically.
const OIDC_ERROR_MESSAGES: Record<string, string> = {
  stateMismatch:
    'The SSO login timed out or was tampered with — please try again.',
  userNotProvisioned:
    'Your SSO account is not authorised for this instance. Contact an administrator.',
  disabled: 'SSO is not currently enabled on this instance.',
  notConfigured: 'SSO is not configured on this instance.',
  invalidRequest:
    'The provider returned an incomplete response — please try again.',
  unknown: 'Sign-in failed. Please try again or use a local account.',
};

export const Route = createFileRoute('/login')({
  validateSearch: (raw: Record<string, unknown>): LoginSearch => ({
    next: typeof raw.next === 'string' ? raw.next : undefined,
    mode: raw.mode === 'signup' ? 'signup' : 'login',
    oidcError:
      typeof raw.oidcError === 'string' ? raw.oidcError : undefined,
    local: raw.local === true || raw.local === 'true',
  }),
  // Skip rendering the page entirely for already-authenticated users.
  beforeLoad: async ({ context, search }) => {
    const me = await context.queryClient.ensureQueryData({
      queryKey: meQueryKey,
      queryFn: fetchMe,
      staleTime: 60_000,
    });
    if (me) {
      throw redirect({ to: safeNext(search.next) });
    }
  },
  component: LoginPage,
});

// safeNext rejects external redirects and the login page itself, collapsing
// those back to the dashboard.
function safeNext(raw: string | undefined): string {
  if (!raw) return '/';
  if (!raw.startsWith('/')) return '/';
  if (raw.startsWith('/login')) return '/';
  return raw;
}

function LoginPage() {
  const { next, mode: modeSearch, oidcError, local } = Route.useSearch();
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  // /auth/signup reports whether the first-run bootstrap is still available.
  // Shown as a toggle at the bottom of the card when it is.
  const signupOpen = useQuery({
    queryKey: ['signup-status'],
    queryFn: signupStatus,
    staleTime: 5 * 60_000,
  });

  const oidc = useQuery({
    queryKey: oidcConfigQueryKey,
    queryFn: oidcConfig,
    staleTime: 5 * 60_000,
  });

  const [mode, setMode] = useState<'login' | 'signup'>(modeSearch ?? 'login');
  const [email, setEmail] = useState('');
  const [name, setName] = useState('');
  const [password, setPassword] = useState('');

  const loginMut = useMutation({
    mutationFn: () => login(email, password),
    onSuccess: (user) => {
      queryClient.setQueryData(meQueryKey, user);
      void navigate({ to: safeNext(next), replace: true });
    },
  });
  const signupMut = useMutation({
    mutationFn: () => signup(email, name, password),
    onSuccess: (user) => {
      queryClient.setQueryData(meQueryKey, user);
      void navigate({ to: safeNext(next), replace: true });
    },
  });

  const active = mode === 'signup' ? signupMut : loginMut;
  const error = active.error as ApiError | null;

  // Force-only mode: when exactly one provider is enabled we can
  // auto-redirect to it. When multiple are enabled we still show the
  // login card (but hide the local form) so the user picks one. The
  // `?local=true` escape hatch and any OIDC error bail out to the
  // normal card — matches the spec recovery path.
  const providers = oidc.data?.providers ?? [];
  const canAutoRedirect =
    oidc.data?.forceOnly === true &&
    providers.length === 1 &&
    !local &&
    !oidcError;
  useEffect(() => {
    if (canAutoRedirect && mode === 'login' && providers[0]) {
      window.location.href = providers[0].loginUrl;
    }
  }, [canAutoRedirect, mode, providers]);

  const hideLocalForm =
    oidc.data?.forceOnly === true && providers.length > 0 && !local && !oidcError;

  const oidcErrorMessage = oidcError
    ? OIDC_ERROR_MESSAGES[oidcError] ?? OIDC_ERROR_MESSAGES.unknown
    : null;

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    active.reset();
    active.mutate();
  }

  const showSignupToggle = signupOpen.data?.enabled;

  return (
    <main
      style={{
        minHeight: '100vh',
        background: 'var(--color-paper-1)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        padding: 32,
      }}
    >
      <div style={{ width: '100%', maxWidth: 420 }}>
        <div
          style={{
            textAlign: 'center',
            marginBottom: 24,
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            gap: 10,
          }}
        >
          <div
            style={{
              width: 40,
              height: 40,
              background: 'var(--color-ink-1)',
              color: 'var(--color-paper-0)',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              fontFamily: 'var(--font-serif)',
              fontWeight: 600,
              fontSize: 24,
              fontStyle: 'italic',
              borderRadius: 2,
            }}
          >
            e
          </div>
          <div
            style={{
              fontFamily: 'var(--font-serif)',
              fontSize: 22,
              fontWeight: 500,
              letterSpacing: '-0.01em',
            }}
          >
            embookshelf
          </div>
        </div>

        <div
          style={{
            background: 'var(--color-paper-0)',
            border: '1px solid var(--color-rule-soft)',
            padding: 32,
            borderRadius: 3,
            boxShadow: '0 12px 32px -8px oklch(0.2 0.02 60 / 0.14)',
          }}
        >
          <h1 className="t-h2" style={{ marginBottom: 4 }}>
            {mode === 'signup' ? 'Create the first account' : 'Sign in'}
          </h1>
          <p className="t-small" style={{ marginBottom: 24, fontStyle: 'italic' }}>
            {mode === 'signup'
              ? 'You are the first user — this account will be the admin.'
              : 'Enter your credentials to continue.'}
          </p>

          {oidcErrorMessage && (
            <div
              className="flash error"
              style={{
                padding: '10px 14px',
                border: '1px solid var(--color-accent-soft)',
                background: 'var(--color-accent-soft)',
                color: 'var(--color-accent-ink)',
                borderRadius: 2,
                fontSize: 13,
                marginBottom: 16,
              }}
            >
              {oidcErrorMessage}
            </div>
          )}

          {error && (
            <div
              className="flash error"
              style={{
                padding: '10px 14px',
                border: '1px solid var(--color-accent-soft)',
                background: 'var(--color-accent-soft)',
                color: 'var(--color-accent-ink)',
                borderRadius: 2,
                fontSize: 13,
                marginBottom: 16,
              }}
            >
              {error.message}
            </div>
          )}

          <form onSubmit={onSubmit} style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
            {!hideLocalForm && (
              <>
                {mode === 'signup' && (
                  <Field label="Name">
                    <Input
                      autoComplete="name"
                      value={name}
                      onChange={(e) => setName(e.target.value)}
                    />
                  </Field>
                )}
                <Field label="Email">
                  <Input
                    type="email"
                    autoComplete="email"
                    required
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                  />
                </Field>
                <Field label="Password">
                  <Input
                    type="password"
                    autoComplete={mode === 'signup' ? 'new-password' : 'current-password'}
                    required
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                  />
                </Field>

                <Button
                  type="submit"
                  className="mt-1 w-full"
                  disabled={active.isPending}
                >
                  {active.isPending
                    ? 'Working…'
                    : mode === 'signup'
                      ? 'Create account'
                      : 'Sign in'}
                </Button>
              </>
            )}

            {mode === 'login' && providers.length > 0 && (
              <>
                {!hideLocalForm && (
                  <div
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: 12,
                      marginTop: 4,
                    }}
                  >
                    <div style={{ flex: 1, height: 1, background: 'var(--color-rule-soft)' }} />
                    <span className="t-small" style={{ color: 'var(--color-ink-3)' }}>or</span>
                    <div style={{ flex: 1, height: 1, background: 'var(--color-rule-soft)' }} />
                  </div>
                )}
                {providers.map((p) => (
                  <Button key={p.slug} asChild variant="outline" className="w-full">
                    <a href={p.loginUrl}>Sign in with {p.name}</a>
                  </Button>
                ))}
              </>
            )}
          </form>
        </div>

        {showSignupToggle && (
          <div style={{ textAlign: 'center', marginTop: 16 }}>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => {
                setMode((m) => (m === 'signup' ? 'login' : 'signup'));
                loginMut.reset();
                signupMut.reset();
              }}
            >
              {mode === 'signup'
                ? 'I already have an account'
                : 'First-run? Create the admin account'}
            </Button>
          </div>
        )}
      </div>
    </main>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      <span className="t-label">{label}</span>
      {children}
    </label>
  );
}
