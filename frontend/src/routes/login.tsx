import { createFileRoute } from '@tanstack/react-router';

export const Route = createFileRoute('/login')({
  component: LoginPage,
});

function LoginPage() {
  return (
    <main className="min-h-screen bg-paper-0 text-ink-1 px-8 py-16">
      <div className="mx-auto max-w-sm space-y-4">
        <h1 className="text-3xl font-serif">Sign in</h1>
        <p className="text-ink-3 text-sm">
          Auth endpoints pending — will POST to <code>/api/v1/auth/login</code>.
        </p>
      </div>
    </main>
  );
}
