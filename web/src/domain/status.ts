export const outcomeLabel = (outcome: string): string =>
  ({
    recorded: 'Registrado',
    ignored: 'Ignorado (sem efeito financeiro)',
    duplicate_transaction: 'Transação já lançada',
  })[outcome] ?? outcome;

export const outboxLabel = (status: string): string =>
  ({
    pending: 'Aguardando publicação',
    published: 'Publicada no broker',
    dead: 'Esgotou as tentativas',
    discarded: 'Descartada por operador',
  })[status] ?? status;

export const eventTypeLabel = (type: string): string =>
  ({
    'payment.captured': 'captura',
    'payment.refunded': 'estorno',
    'ledger.adjustment': 'ajuste',
  })[type] ?? type;

export const deliveryLabel = (deliveries: number): string =>
  deliveries <= 1 ? 'sem reenvios' : `${String(deliveries - 1)} reenvio${deliveries - 1 > 1 ? 's' : ''} descartado${deliveries - 1 > 1 ? 's' : ''}`;

export const reasonLabel = (reason: string): string =>
  ({
    rejected: 'Recusada pelo consumidor (contrato inválido)',
    delivery_limit: 'Limite de entregas esgotado',
    outbox_exhausted: 'Outbox esgotou as tentativas de publicação',
    unknown: 'Motivo não informado pelo broker',
  })[reason] ?? reason;

/** Escala de idade da DLQ: 0 recente … 3 crítica; o painel mostra em âmbar crescente, sempre com o texto da idade. */
export const ageLevel = (seconds: number): 0 | 1 | 2 | 3 => (seconds < 300 ? 0 : seconds < 1_800 ? 1 : seconds < 7_200 ? 2 : 3);
