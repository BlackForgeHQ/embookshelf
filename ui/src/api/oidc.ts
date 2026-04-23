import { api } from './client';

export type ClaimMapping = {
  username: string;
  email: string;
  name: string;
  groups?: string;
};

export type OidcProvider = {
  providerName: string;
  clientId: string;
  clientSecret?: string;
  clientSecretSet: boolean;
  issuerUri: string;
  scopes: string;
  claimMapping: ClaimMapping;
};

export type OidcAutoProvision = {
  enableAutoProvisioning: boolean;
  allowLocalAccountLinking: boolean;
  defaultRole: 'admin' | 'user';
};

export type OidcAdminSettings = {
  enabled: boolean;
  forceOnly: boolean;
  provider: OidcProvider;
  autoProvision: OidcAutoProvision;
  redirectUri: string;
};

export type OidcTestCheck = {
  name: string;
  status: 'PASS' | 'FAIL' | 'WARN';
  message: string;
};

export type OidcTestResult = {
  success: boolean;
  checks: Array<OidcTestCheck>;
};

export const oidcAdminSettingsQueryKey = ['oidc-admin-settings'] as const;

export async function fetchOidcAdminSettings(): Promise<OidcAdminSettings> {
  return api<OidcAdminSettings>('/api/v1/settings/oidc');
}

// The server fills in `clientSecret` only when the caller explicitly
// provides one — `clientSecretSet: true` + empty `clientSecret` means
// "keep existing"; `clientSecretSet: false` + empty clears it.
export async function saveOidcAdminSettings(body: OidcAdminSettings): Promise<OidcAdminSettings> {
  return api<OidcAdminSettings>('/api/v1/settings/oidc', {
    method: 'PUT',
    body: JSON.stringify(body),
  });
}

export async function testOidcConnection(provider: OidcProvider): Promise<OidcTestResult> {
  return api<OidcTestResult>('/api/v1/settings/oidc/test', {
    method: 'POST',
    body: JSON.stringify({ provider }),
  });
}
