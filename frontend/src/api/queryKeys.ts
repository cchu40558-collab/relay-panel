export const keys = {
  server: {
    status: () => ['server', 'status'] as const,
    fail2banStatus: () => ['server', 'fail2banStatus'] as const,
  },
  nodes: {
    root: () => ['nodes'] as const,
    list: () => ['nodes', 'list'] as const,
  },
  hosts: {
    root: () => ['hosts'] as const,
    list: () => ['hosts', 'list'] as const,
    byInbound: (inboundId: number) => ['hosts', 'byInbound', inboundId] as const,
    tags: () => ['hosts', 'tags'] as const,
  },
  settings: {
    root: () => ['settings'] as const,
    all: () => ['settings', 'all'] as const,
    defaults: () => ['settings', 'defaults'] as const,
    factoryDefaults: () => ['settings', 'factoryDefaults'] as const,
  },
  inbounds: {
    root: () => ['inbounds'] as const,
    slim: () => ['inbounds', 'slim'] as const,
    options: () => ['inbounds', 'options'] as const,
  },
  lines: {
    root: () => ['lines'] as const,
    list: () => ['lines', 'list'] as const,
    detail: (id: number) => ['lines', 'detail', id] as const,
    metrics: () => ['lines', 'metrics'] as const,
    metric: (id: number) => ['lines', 'metrics', id] as const,
    types: () => ['lines', 'types'] as const,
  },
  clients: {
    root: () => ['clients'] as const,
    list: (params: unknown) => ['clients', 'list', params] as const,
    all: () => ['clients', 'all'] as const,
    onlines: () => ['clients', 'onlines'] as const,
    onlinesByGuid: () => ['clients', 'onlinesByGuid'] as const,
    activeInbounds: () => ['clients', 'activeInbounds'] as const,
    lastOnline: () => ['clients', 'lastOnline'] as const,
    groups: () => ['clients', 'groups'] as const,
  },
  xray: {
    root: () => ['xray'] as const,
    config: () => ['xray', 'config'] as const,
    outboundsTraffic: () => ['xray', 'outboundsTraffic'] as const,
  },
} as const;
