// Carga no webhook com k6. Não roda no CI nem em `make test`: é uma medida,
// feita contra um ambiente de teste (veja loadtest/README.md).
//
// Cada iteração assina e envia um evento novo e, em 1 de cada 10, reenvia o
// mesmo evento: a reentrega tem de voltar "duplicate" (idempotência sob carga).
import crypto from 'k6/crypto';
import exec from 'k6/execution';
import { check } from 'k6';
import http from 'k6/http';

const BASE = __ENV.BASE_URL || 'http://localhost:8085';
const SECRET = __ENV.WEBHOOK_SECRET || 'whsec_dev_only_change_me';
const RATE = Number(__ENV.RATE || 50);
const MERCHANTS = Number(__ENV.MERCHANTS || 200);
const RUN = __ENV.RUN_ID || String(Date.now());

export const options = {
  scenarios: {
    webhook: {
      executor: 'constant-arrival-rate',
      rate: RATE,
      timeUnit: '1s',
      duration: __ENV.DURATION || '1m',
      preAllocatedVUs: 50,
      maxVUs: 200,
    },
  },
  // Os limiares fazem o k6 sair com código 99 quando violados.
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<300', 'p(99)<800'],
    'checks{kind:recorded}': ['rate>0.99'],
    'checks{kind:duplicate}': ['rate>0.99'],
  },
};

function send(body) {
  const ts = Math.floor(Date.now() / 1000);
  const sig = crypto.hmac('sha256', SECRET, `${ts}.${body}`, 'hex');
  return http.post(`${BASE}/webhooks/pagafacil`, body, {
    headers: { 'Content-Type': 'application/json', 'PagaFacil-Signature': `t=${ts},v1=${sig}` },
  });
}

function status(res) {
  try {
    return res.json('status');
  } catch (_) {
    return undefined;
  }
}

export default function () {
  const n = exec.scenario.iterationInTest;
  const id = `evt_k6_${RUN}_${n}`;
  const body = JSON.stringify({
    id,
    type: n % 5 === 0 ? 'payment.refunded' : 'payment.captured',
    created_at: new Date().toISOString(),
    data: {
      merchant_id: `loja_k6_${n % MERCHANTS}`,
      transaction_id: `txn_k6_${RUN}_${n}`,
      payment_id: `pay_k6_${RUN}_${n}`,
      amount: 1000 + (n % 9000),
      currency: 'BRL',
    },
  });
  const first = send(body);
  check(first, { 'evento novo é registrado': (r) => r.status === 200 && status(r) === 'recorded' }, { kind: 'recorded' });
  if (n % 10 === 0) {
    const again = send(body);
    check(again, { 'reentrega é duplicata': (r) => r.status === 200 && status(r) === 'duplicate' }, { kind: 'duplicate' });
  }
}
