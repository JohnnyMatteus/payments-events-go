import { createHmac } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { test, expect, type APIRequestContext } from '@playwright/test';
import { narrar } from './narrar';

const REPO = resolve(__dirname, '../../..');
const PROJETO = process.env.COMPOSE_PROJECT ?? 'payments-events-go';
const API = `http://localhost:${process.env.API_PORT ?? '8085'}`;
const RABBIT_UI = `http://localhost:${process.env.RABBITMQ_UI_PORT ?? '15673'}`;
const RABBIT_AUTH = 'Basic ' + Buffer.from(`payments:${process.env.RABBITMQ_PASSWORD ?? 'dev-only-change-me'}`).toString('base64');
const S3 = `http://localhost:${process.env.LOCALSTACK_PORT ?? '4567'}`;
const JAEGER = `http://localhost:${process.env.JAEGER_UI_PORT ?? '16687'}`;
const CONSUMER_OPS = `http://localhost:${process.env.CONSUMER_OPS_PORT ?? '9102'}`;
const RELAY_OPS = `http://localhost:${process.env.RELAY_OPS_PORT ?? '9101'}`;
const SECRET = process.env.WEBHOOK_SECRET ?? 'whsec_dev_only_change_me';
const DIA = '2026-11-20';
const WEBHOOK = `${API}/webhooks/pagafacil`;

const compose = (...args: string[]) =>
  execFileSync('docker', ['compose', '-p', PROJETO, '--profile', 'core', '--profile', 'tools', ...args], {
    cwd: REPO,
    encoding: 'utf8',
    env: { ...process.env, RECONCILE_DATE: DIA },
  });

const sql = (query: string) =>
  compose('exec', '-T', 'postgres', 'psql', '-U', 'payments', '-d', 'payments', '-c', query).trim();

const redis = (...args: string[]) => compose('exec', '-T', 'redis', 'redis-cli', ...args).trim();

function evento(id: string, tipo: string, txn: string, pagamento: string, centavos: number, hora: string,
  lojista = 'loja_demo_001') {
  return JSON.stringify({
    id,
    type: tipo,
    created_at: `${DIA}T${hora}:00Z`,
    data: { merchant_id: lojista, transaction_id: txn, payment_id: pagamento, amount: centavos, currency: 'BRL' },
  });
}

function assinar(corpo: string, segredo = SECRET, ts = Math.floor(Date.now() / 1000)) {
  const mac = createHmac('sha256', segredo).update(`${ts}.${corpo}`).digest('hex');
  return `t=${ts},v1=${mac}`;
}

async function enviar(request: APIRequestContext, corpo: string, assinatura: string) {
  const resp = await request.post(WEBHOOK, {
    data: corpo,
    headers: { 'Content-Type': 'application/json', 'PagaFacil-Signature': assinatura },
  });
  return { status: resp.status(), corpo: await resp.json(), headers: resp.headers() };
}

async function enviarCronometrado(request: APIRequestContext, corpo: string) {
  const inicio = performance.now();
  const r = await enviar(request, corpo, assinar(corpo));
  return { ...r, ms: Math.round((performance.now() - inicio) * 10) / 10 };
}

async function filaRabbit(request: APIRequestContext, fila: string) {
  const resp = await request.get(`${RABBIT_UI}/api/queues/%2F/${encodeURIComponent(fila)}`, {
    headers: { Authorization: RABBIT_AUTH },
  });
  expect(resp.status()).toBe(200);
  return resp.json();
}

const linhasMetricas = (texto: string, prefixos: string[]) =>
  texto
    .split('\n')
    .filter((l) => prefixos.some((p) => l.startsWith(p)) && !l.includes('_bucket'))
    .map((l) => l.replace(/otel_scope_name="[^"]*",otel_scope_schema_url="[^"]*",otel_scope_version="[^"]*",?/, ''))
    .join('\n');

test('fluxo principal: webhook → ledger → outbox → RabbitMQ → conciliação → observabilidade', async ({ page, request }) => {
  await narrar(
    page,
    'payments-events-go — evidência do fluxo principal',
    `Processador de webhooks do provedor fictício PagaFácil Sandbox. Vamos provar, com a stack real em Docker ` +
      `(Postgres, RabbitMQ, LocalStack S3, Jaeger): assinatura HMAC e anti-replay, idempotência com atalho no Redis, rate limit por lojista, ledger + ` +
      `transactional outbox, consumidor com DLQ, conciliação do extrato e observabilidade. Gravado em ` +
      `${new Date().toLocaleString('pt-BR')}.`,
    { pausaMs: 4000 },
  );

  // 1. Captura com assinatura válida.
  const captura = evento('evt_demo_001', 'payment.captured', 'txn_demo_001', 'pay_demo_001', 15000, '12:00');
  const r1 = await enviarCronometrado(request, captura);
  expect(r1.status).toBe(200);
  expect(r1.corpo).toEqual({ event_id: 'evt_demo_001', status: 'recorded' });
  await narrar(page, '1. Webhook de captura com HMAC válido', `Corpo: ${captura}. Header PagaFacil-Signature = t=<agora>,v1=HMAC-SHA256("t.corpo"). O evento é gravado no ledger (${r1.ms} ms, passando pelo Postgres).`, {
    chamada: { metodo: 'POST', url: WEBHOOK, status: r1.status, corpo: r1.corpo },
  });

  // 2. Reentrega do mesmo evento.
  const r2 = await enviarCronometrado(request, captura);
  expect(r2.status).toBe(200);
  expect(r2.corpo).toEqual({ event_id: 'evt_demo_001', status: 'duplicate' });
  const contagem = sql("select count(*) as lancamentos_evt_demo_001 from ledger_entries where event_id = 'evt_demo_001'");
  expect(contagem).toMatch(/\n\s*1\n/);
  await narrar(page, '2. Reentrega do mesmo evento: idempotência', `O provedor reenviou evt_demo_001. A API responde 200 com status "duplicate" (para o provedor parar de tentar) e não cria um segundo lançamento. Primeira entrega: ${r1.ms} ms; reentrega: ${r2.ms} ms.`, {
    chamada: { metodo: 'POST', url: WEBHOOK, status: r2.status, corpo: r2.corpo },
    rotuloSaida: 'docker compose exec postgres psql (consulta real)',
    saida: contagem,
  });

  // 2b. Atalho no Redis: SET NX com TTL antes do INSERT.
  const chaveIdem = 'payments:idem:evt_demo_001';
  const valorIdem = redis('GET', chaveIdem);
  const ttlIdem = Number(redis('TTL', chaveIdem));
  expect(valorIdem).toBe('done');
  expect(ttlIdem).toBeGreaterThan(23 * 3600);
  const fastpath = linhasMetricas(await (await request.get(`${API}/metrics`)).text(), ['idempotency_fastpath_total']);
  expect(fastpath).toMatch(/result="acquired"} 1/);
  expect(fastpath).toMatch(/result="done"} 1/);
  await narrar(page, '2b. Reentrega respondida pelo Redis, sem ir ao Postgres', 'Antes do INSERT a API faz SET NX GET com TTL. Na primeira entrega a chave não existia (acquired) e, gravado o evento, virou "done" por 24 h. Na reentrega o Redis já respondia "done": o banco nem foi consultado.', {
    rotuloSaida: 'docker compose exec redis redis-cli GET/TTL  +  GET /metrics',
    saida: `GET ${chaveIdem}  →  "${valorIdem}"\nTTL ${chaveIdem}  →  ${ttlIdem} s\n\n${fastpath}`,
    pausaMs: 4000,
  });

  // 2c. Sem o Redis, o Postgres continua garantindo.
  expect(redis('DEL', chaveIdem)).toBe('1');
  const r2c = await enviarCronometrado(request, captura);
  expect(r2c.status).toBe(200);
  expect(r2c.corpo).toEqual({ event_id: 'evt_demo_001', status: 'duplicate' });
  const contagem2 = sql("select count(*) as lancamentos_evt_demo_001 from ledger_entries where event_id = 'evt_demo_001'");
  expect(contagem2).toMatch(/\n\s*1\n/);
  await narrar(page, '2c. Chave apagada do Redis: o Postgres segue como garantia', `redis-cli DEL ${chaveIdem} e o mesmo evento de novo: sem atalho, a constraint única do Postgres decide e a resposta continua "duplicate" (${r2c.ms} ms). Ainda um único lançamento.`, {
    chamada: { metodo: 'POST', url: WEBHOOK, status: r2c.status, corpo: r2c.corpo },
    rotuloSaida: 'docker compose exec postgres psql (consulta real)',
    saida: contagem2,
  });

  // 3a. Assinatura inválida.
  const r3 = await enviar(request, captura, assinar(captura, 'whsec_segredo_errado'));
  expect(r3.status).toBe(401);
  expect(r3.corpo.type).toContain('invalid_signature');
  await narrar(page, '3a. Assinatura inválida → 401', 'Mesmo corpo, assinado com um segredo que o serviço não conhece. Resposta em application/problem+json, sem revelar a causa ao cliente.', {
    chamada: { metodo: 'POST', url: WEBHOOK, ...r3 },
  });

  // 3b. Timestamp antigo (replay).
  const antigo = Math.floor(Date.now() / 1000) - 10 * 60;
  const r4 = await enviar(request, captura, assinar(captura, SECRET, antigo));
  expect(r4.status).toBe(401);
  expect(r4.corpo.type).toContain('stale_timestamp');
  await narrar(page, '3b. Timestamp de 10 minutos atrás → replay rejeitado', 'Assinatura correta, mas t= está fora da janela de tolerância (5 min). Uma requisição capturada não pode ser reenviada depois.', {
    chamada: { metodo: 'POST', url: WEBHOOK, ...r4 },
  });

  // 3c. Limite por lojista (contagem no Redis, janela de 1 minuto).
  const segundos = new Date().getSeconds();
  if (segundos > 50) await page.waitForTimeout((61 - segundos) * 1000);
  const rajada: { status: number; corpo: any; headers: Record<string, string> }[] = [];
  for (let i = 1; i <= 22; i++) {
    const id = `evt_rajada_${String(i).padStart(2, '0')}`;
    const corpo = evento(id, 'payment.dispute_opened', `txn_rajada_${i}`, 'pay_rajada', 1, '16:00', 'loja_demo_rajada');
    rajada.push(await enviar(request, corpo, assinar(corpo)));
  }
  const aceitos = rajada.filter((r) => r.status === 200).length;
  const limitados = rajada.filter((r) => r.status === 429);
  expect(aceitos).toBe(20);
  expect(limitados.length).toBe(2);
  expect(limitados[0].corpo.type).toContain('rate_limited');
  expect(Number(limitados[0].headers['retry-after'])).toBeGreaterThan(0);
  const chavesLimite = redis('--scan', '--pattern', 'payments:ratelimit:merchant:*');
  const chaveRajada = chavesLimite.split('\n').find((k) => k.includes('loja_demo_rajada'))!;
  const contador = redis('GET', chaveRajada);
  expect(contador).toBe('22');
  await narrar(page, '3c. Rate limit por lojista → 429', `loja_demo_rajada enviou 22 webhooks válidos em sequência: ${aceitos} aceitos, ${limitados.length} recusados com 429 e Retry-After. O contador fica no Redis (INCR + EXPIRE NX numa transação), então vale para todas as réplicas da API.`, {
    chamada: { metodo: 'POST', url: WEBHOOK, status: limitados[0].status, corpo: {
      headers: {
        'retry-after': limitados[0].headers['retry-after'],
        'ratelimit-limit': limitados[0].headers['ratelimit-limit'],
        'ratelimit-remaining': limitados[0].headers['ratelimit-remaining'],
      },
      body: limitados[0].corpo,
    } },
    rotuloSaida: 'docker compose exec redis redis-cli',
    saida: `--scan --pattern payments:ratelimit:merchant:*\n${chavesLimite}\n\nGET ${chaveRajada}  →  ${contador}\nTTL  →  ${redis('TTL', chaveRajada)} s`,
    pausaMs: 4500,
  });

  // Mais dois eventos do dia, usados na conciliação (outro lojista, não afetado pelo limite).
  const estorno = evento('evt_demo_002', 'payment.refunded', 'txn_demo_002', 'pay_demo_001', 2500, '13:00');
  const captura2 = evento('evt_demo_003', 'payment.captured', 'txn_demo_003', 'pay_demo_002', 8000, '14:00');
  const r5 = await enviar(request, estorno, assinar(estorno));
  const r6 = await enviar(request, captura2, assinar(captura2));
  expect([r5.status, r6.status]).toEqual([200, 200]);
  expect([r5.corpo.status, r6.corpo.status]).toEqual(['recorded', 'recorded']);
  await narrar(page, 'Mais dois eventos do dia', 'Um estorno de R$ 25,00 (evt_demo_002) e uma captura de R$ 80,00 (evt_demo_003) de loja_demo_001, aceitos logo depois da rajada: o limite é por lojista.', {
    rotuloSaida: 'respostas reais da API',
    saida: JSON.stringify([r5.corpo, r6.corpo], null, 2),
  });

  // 4. Ledger e outbox.
  await expect
    .poll(() => sql("select count(*) from outbox_messages where status = 'published'"), { timeout: 20_000 })
    .toMatch(/\n\s*3\n/);
  const ledger = sql('select event_id, transaction_id, payment_id, direction, amount_cents, currency from ledger_entries order by id');
  expect(ledger).toContain('(3 rows)');
  await narrar(page, '4. Lançamentos no ledger (append-only)', 'Três lançamentos, um por evento aceito. A duplicata e as requisições rejeitadas não deixaram rastro.', {
    rotuloSaida: 'docker compose exec postgres psql -c "select ... from ledger_entries"',
    saida: ledger,
    pausaMs: 3500,
  });
  const outbox = sql('select message_id, routing_key, status, attempts, published_at, headers->>\'traceparent\' as traceparent from outbox_messages order by id');
  expect(outbox.match(/\| published /g)?.length).toBe(3);
  await narrar(page, 'Transactional outbox → RabbitMQ', 'A mensagem é gravada na mesma transação do lançamento; o relay publica com publisher confirms e marca como published. O traceparent segue junto para ligar o trace.', {
    rotuloSaida: 'docker compose exec postgres psql -c "select ... from outbox_messages"',
    saida: outbox,
    pausaMs: 3500,
  });

  // RabbitMQ Management: fila principal com consumidor.
  const fila = await filaRabbit(request, 'balance-projector');
  expect(fila.consumers).toBeGreaterThanOrEqual(1);
  await page.goto(RABBIT_UI, { waitUntil: 'domcontentloaded' });
  await page.locator('input[name="username"]').fill('payments');
  await page.locator('input[name="password"]').fill(process.env.RABBITMQ_PASSWORD ?? 'dev-only-change-me');
  await page.locator('input[type="submit"]').click();
  await page.getByRole('link', { name: 'Queues and Streams' }).click();
  await page.waitForTimeout(1500);
  await page.locator('table.list').highlight();
  await page.waitForTimeout(2500);
  await page.goto(`${RABBIT_UI}/#/queues/%2F/balance-projector`, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(2500);
  await page.getByText('Consumers', { exact: false }).first().highlight();
  await page.waitForTimeout(2500);

  const projecao = sql('select payment_id, captured_cents, refunded_cents, updated_at from payment_balances order by payment_id');
  expect(projecao).toMatch(/pay_demo_001\s*\|\s*15000\s*\|\s*2500/);
  expect(projecao).toMatch(/pay_demo_002\s*\|\s*8000\s*\|\s*0/);
  await narrar(page, 'Consumidor processou as mensagens', 'O consumidor balance-projector leu a fila e projetou o saldo por pagamento (idempotente por message_id).', {
    rotuloSaida: 'docker compose exec postgres psql -c "select ... from payment_balances"',
    saida: projecao,
  });

  // Erro permanente → DLQ.
  const publicar = await request.post(`${RABBIT_UI}/api/exchanges/%2F/payments.events/publish`, {
    headers: { Authorization: RABBIT_AUTH },
    data: {
      properties: { message_id: 'msg_demo_invalida', content_type: 'application/json' },
      routing_key: 'ledger.entry_recorded',
      payload: '{isto não é JSON',
      payload_encoding: 'string',
    },
  });
  expect(publicar.status()).toBe(200);
  const publicado = await publicar.json();
  expect(publicado.routed).toBe(true);
  await narrar(page, 'Mensagem com erro permanente', 'Publicamos na exchange payments.events uma mensagem com corpo inválido. Não adianta tentar de novo: o consumidor dá nack sem requeue e o broker a manda para a DLQ.', {
    chamada: { metodo: 'POST', url: `${RABBIT_UI}/api/exchanges/%2F/payments.events/publish`, status: publicar.status(), corpo: publicado },
  });
  await expect.poll(async () => (await filaRabbit(request, 'balance-projector.dlq')).messages ?? 0, { timeout: 30_000 }).toBe(1);
  const logConsumer = compose('logs', '--no-log-prefix', 'consumer')
    .split('\n')
    .filter((l) => l.includes('msg_demo_invalida'))
    .map((l) => JSON.stringify(JSON.parse(l), null, 2))
    .join('\n');
  expect(logConsumer).toContain('permanent failure, sending to DLQ');
  await narrar(page, 'Log do consumidor', 'O consumidor classificou o erro como permanente e mandou a mensagem para a DLQ.', {
    rotuloSaida: 'docker compose logs consumer',
    saida: logConsumer,
  });
  await page.goto(`${RABBIT_UI}/#/queues/%2F/balance-projector.dlq`, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(2000);
  await page.locator('#main table').first().highlight();
  await page.waitForTimeout(2000);
  await page.getByText('Get messages', { exact: true }).first().click();
  await page.getByRole('button', { name: /Get Message/ }).click();
  await page.waitForTimeout(1000);
  const msgDlq = page.locator('pre.msg-payload').first();
  await expect(msgDlq).toContainText('isto não é JSON');
  await msgDlq.scrollIntoViewIfNeeded();
  await msgDlq.highlight();
  await page.waitForTimeout(3000);

  // 5. Extrato no S3 e conciliação.
  const csv = readFileSync(resolve(REPO, `testdata/statements/${DIA}.csv`), 'utf8');
  const chave = `pagafacil-statements/settlements/${DIA}.csv`;
  const upload = await request.put(`${S3}/${chave}`, { data: csv, headers: { 'Content-Type': 'text/csv' } });
  expect(upload.status()).toBe(200);
  await narrar(page, '5. Extrato do provedor enviado ao S3 (LocalStack)', 'O PagaFácil deposita o extrato diário de liquidação no bucket. Conteúdo do CSV enviado:', {
    chamada: { metodo: 'PUT', url: `${S3}/${chave}`, status: upload.status(), corpo: csv },
  });
  await page.goto(`${S3}/pagafacil-statements?list-type=2&prefix=settlements/`, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(3000);

  const relatorio = compose('run', '--rm', '-T', 'reconcile');
  expect(relatorio).toMatch(/valor divergente\s+txn_demo_002/);
  expect(relatorio).toMatch(/faltando no ledger\s+txn_demo_004/);
  expect(relatorio).toMatch(/sobrando no ledger\s+txn_demo_003/);
  expect(relatorio).toMatch(/Conciliadas\s+1/);
  await narrar(page, 'Relatório de conciliação', 'docker compose run reconcile: lê o extrato do S3 e compara com o ledger. txn_demo_001 bate; txn_demo_002 tem valor divergente; txn_demo_004 falta no ledger; txn_demo_003 sobra no ledger.', {
    rotuloSaida: `payments reconcile -statement s3://${chave} -from ${DIA}`,
    saida: relatorio.trim(),
    pausaMs: 5000,
  });

  // 6. Trace distribuído no Jaeger.
  const traceparent = sql("select headers->>'traceparent' from outbox_messages where message_id = 'evt_demo_001'");
  const traceId = traceparent.match(/00-([0-9a-f]{32})-/)?.[1];
  expect(traceId).toBeTruthy();
  await expect
    .poll(async () => {
      const r = await request.get(`${JAEGER}/api/traces/${traceId}`);
      if (r.status() !== 200) return [];
      const dados = (await r.json()).data?.[0];
      return [...new Set(Object.values(dados?.processes ?? {}).map((p: any) => p.serviceName))].sort();
    }, { timeout: 30_000 })
    .toEqual(['payments-api', 'payments-consumer', 'payments-relay']);
  await narrar(page, '6. Trace ponta a ponta no Jaeger', `Trace ${traceId} do evt_demo_001: webhook na API → gravação no Postgres → relay do outbox → consumo no RabbitMQ, em três serviços.`);
  await page.goto(`${JAEGER}/trace/${traceId}`, { waitUntil: 'domcontentloaded' });
  await page.getByText('consume ledger.entry_recorded').first().waitFor({ timeout: 20_000 });
  await page.waitForTimeout(4000);

  // Métricas Prometheus.
  await page.goto(`${API}/metrics`, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(2500);
  const metricasApi = await (await request.get(`${API}/metrics`)).text();
  const metricasConsumer = await (await request.get(`${CONSUMER_OPS}/metrics`)).text();
  const metricasRelay = await (await request.get(`${RELAY_OPS}/metrics`)).text();
  const webhook = linhasMetricas(metricasApi, ['webhook_requests_total']);
  expect(webhook).toMatch(/outcome="duplicate"} 2/);
  expect(webhook).toMatch(/outcome="rate_limited"} 2/);
  expect(webhook).toMatch(/outcome="ignored"} 20/);
  expect(webhook).toMatch(/outcome="invalid_signature"} 1/);
  expect(webhook).toMatch(/outcome="stale_timestamp"} 1/);
  expect(webhook).toMatch(/outcome="recorded"} 3/);
  const idem = linhasMetricas(metricasApi, ['idempotency_fastpath_total']);
  const consumidor = linhasMetricas(metricasConsumer, ['consumer_messages_total']);
  expect(consumidor).toMatch(/outcome="dead_lettered"} 1/);
  await narrar(page, 'Métricas em /metrics', 'Contadores reais expostos no formato Prometheus pela API (:8085), relay (:9101) e consumidor (:9102).', {
    rotuloSaida: 'GET /metrics (linhas filtradas)',
    saida: [webhook, idem, consumidor, linhasMetricas(metricasRelay, ['outbox_'])].join('\n'),
    pausaMs: 4500,
  });

  await narrar(
    page,
    'Resumo: o que foi provado',
    '✔ HMAC válido → 200 recorded   ✔ reentrega → duplicate pelo Redis, sem ir ao banco; sem a chave, o Postgres garante   ' +
      '✔ assinatura inválida e replay → 401   ✔ rate limit por lojista no Redis → 429 com Retry-After   ' +
      '✔ ledger + outbox publicados no RabbitMQ   ✔ consumidor projetou saldos; erro permanente foi para a DLQ   ' +
      '✔ extrato no S3 conciliado: 1 ok, 1 faltando, 1 sobrando, 1 valor divergente   ✔ trace em 3 serviços no Jaeger e métricas em /metrics.',
    { pausaMs: 4500 },
  );
});
