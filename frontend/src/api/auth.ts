import { api, type ApiError } from './client';

// Mirrors internal/handler/auth.go userDTO.
export type AuthUser = {
  id: string;
  email: string;
  name: string;
  role: 'admin' | 'user';
  display: string;
  initials: string;
  createdAt: string;
  lastSeenAt?: string;
};

type UserEnvelope = { user: AuthUser };
type SignupStatus = { enabled: boolean };

// fetchMe returns the authenticated user or null when the server responds
// with 401. Keeps useQuery's error state reserved for actual failures
// (network, 5xx, etc.) rather than the "logged out" case.
export async function fetchMe(): Promise<AuthUser | null> {
  try {
    const { user } = await api<UserEnvelope>('/api/v1/me');
    return user;
  } catch (e) {
    const err = e as ApiError;
    if (err?.status === 401) return null;
    throw e;
  }
}

export async function login(
  email: string,
  password: string,
): Promise<AuthUser> {
  const { user } = await api<UserEnvelope>('/api/v1/auth/login', {
    method: 'POST',
    body: JSON.stringify({ email, password }),
  });
  return user;
}

export async function logout(): Promise<void> {
  await api<void>('/api/v1/auth/logout', { method: 'POST' });
}

export async function signup(
  email: string,
  name: string,
  password: string,
): Promise<AuthUser> {
  const { user } = await api<UserEnvelope>('/api/v1/auth/signup', {
    method: 'POST',
    body: JSON.stringify({ email, name, password }),
  });
  return user;
}

export async function signupStatus(): Promise<SignupStatus> {
  return api<SignupStatus>('/api/v1/auth/signup');
}

// Shared react-query key for the current user. Export so every mutation can
// invalidate it in one line.
export const meQueryKey = ['me'] as const;
