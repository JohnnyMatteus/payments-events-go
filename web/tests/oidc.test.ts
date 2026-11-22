import { describe, expect, it } from 'vitest';
import { parseRuntimeConfig } from '@/auth/oidc';

describe('config de runtime do painel', () => {
  it('sem oidc usa o token de dev', () => {
    expect(parseRuntimeConfig({ oidc: null })).toBeNull();
    expect(parseRuntimeConfig('lixo')).toBeNull();
    expect(parseRuntimeConfig({ oidc: { authority: 'não é url', clientId: 'x' } })).toBeNull();
  });

  it('aceita emissor e client público', () => {
    expect(parseRuntimeConfig({ oidc: { authority: 'https://idp.example.com/realms/edge', clientId: 'razao' } })).toEqual({
      authority: 'https://idp.example.com/realms/edge',
      clientId: 'razao',
    });
  });
});
