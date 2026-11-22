import { UserManager, WebStorageStateStore } from 'oidc-client-ts';
import { z } from 'zod';
import { setToken } from '@/auth/session';

const runtimeConfig = z.object({
  oidc: z.object({ authority: z.url(), clientId: z.string().min(1) }).nullable(),
});

export type OidcSettings = NonNullable<z.infer<typeof runtimeConfig>['oidc']>;

/** Lê o /config.json do painel (montado no deploy); sem ele, o painel usa o token de dev. */
export function parseRuntimeConfig(raw: unknown): OidcSettings | null {
  const parsed = runtimeConfig.safeParse(raw);
  return parsed.success ? parsed.data.oidc : null;
}

let manager: UserManager | null = null;
export const oidcEnabled = (): boolean => manager !== null;

export async function setupOidc(): Promise<void> {
  try {
    const response = await fetch('/config.json', { cache: 'no-store' });
    const settings = response.ok ? parseRuntimeConfig(await response.json()) : null;
    if (!settings) return;
    // O fluxo é authorization code com PKCE (S256): o navegador não guarda segredo
    // e o access token só vive na sessão da aba.
    manager = new UserManager({
      authority: settings.authority,
      client_id: settings.clientId,
      redirect_uri: `${window.location.origin}/callback`,
      post_logout_redirect_uri: `${window.location.origin}/entrar`,
      response_type: 'code',
      scope: 'openid profile',
      userStore: new WebStorageStateStore({ store: window.sessionStorage }),
    });
  } catch {
    manager = null;
  }
}

export async function startLogin(returnTo: string): Promise<void> {
  if (!manager) throw new Error('OIDC não configurado');
  await manager.signinRedirect({ state: returnTo });
}

/** Conclui o login na volta do provedor e devolve a rota de destino. */
export async function finishLogin(): Promise<string> {
  if (!manager) throw new Error('OIDC não configurado');
  const user = await manager.signinRedirectCallback();
  setToken(user.access_token);
  return typeof user.state === 'string' && user.state.startsWith('/') ? user.state : '/';
}

export async function logout(): Promise<void> {
  setToken(null);
  if (manager) await manager.signoutRedirect();
}
