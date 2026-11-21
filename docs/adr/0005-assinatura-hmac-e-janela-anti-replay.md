# 0005. Webhook autenticado por HMAC-SHA256 com timestamp e janela anti-replay

- Status: Aceito
- Data: 2026-11-20

## Contexto

O endpoint de webhook é público. Sem autenticação, qualquer um poderia forjar uma captura; sem proteção contra replay, uma requisição legítima interceptada poderia ser reenviada mais tarde.

## Decisão

- Header `PagaFacil-Signature: t=<unix>,v1=<hex>`, em que `v1 = HMAC-SHA256(segredo, "<t>.<corpo bruto>")`.
- A verificação usa o corpo **bruto** (antes de qualquer parse) e compara em tempo constante (`hmac.Equal`).
- Timestamps com diferença maior que `WEBHOOK_SIGNATURE_TOLERANCE` (5 min) para o relógio local, no passado ou no futuro, são rejeitados.
- Dentro da janela, um replay do mesmo evento cai na idempotência (ADR 0002) e não tem efeito.
- Rotação de segredos: `WEBHOOK_SECRETS` aceita vários segredos; o header pode trazer vários `v1`. Esquemas desconhecidos (`v0`, `v2`) são ignorados.
- Qualquer falha responde 401 com corpo genérico; a causa exata vai só para o log e para a métrica (`invalid_signature` ou `stale_timestamp`).
- Corpo limitado a 1 MiB e servidor com timeouts de leitura e escrita.

## Consequências

- Forjar ou alterar um evento exige o segredo; reenviar um evento antigo exige que ele ainda esteja na janela, e mesmo assim não reprocessa.
- Relógio do servidor precisa de NTP; desvio maior que a janela recusa eventos legítimos (o provedor reenvia, e a métrica `stale_timestamp` denuncia o problema).
- Segredo compartilhado: vazamento do segredo permite forjar eventos até a rotação.

## Alternativas

- **mTLS**: autentica o canal, não a mensagem, e depende de o provedor suportar.
- **Assinatura assimétrica (Ed25519/JWS)**: elimina segredo compartilhado; boa evolução se o provedor publicar chaves.
- **Allowlist de IPs**: frágil e insuficiente sozinha; pode ser somada no gateway (projeto platform-edge).
- **Guardar nonces vistos**: redundante, já que `event_id` é único e persistido.
