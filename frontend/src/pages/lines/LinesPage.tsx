import { useEffect, useMemo, useRef, useState } from 'react';
import type { Key, PointerEvent as ReactPointerEvent, ReactNode } from 'react';
import { useLocation, useNavigate, useParams } from 'react-router';
import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Collapse, Descriptions, Drawer, Dropdown, Empty, Form, Input, InputNumber, Layout, Modal, QRCode, Select, Space, Switch, Table, Tabs, Tag, Typography, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { CheckCircleOutlined, CopyOutlined, DeleteOutlined, EditOutlined, MoreOutlined, QrcodeOutlined, SaveOutlined } from '@ant-design/icons';

import { keys } from '@/api/queryKeys';
import { ClipboardManager, HttpUtil } from '@/utils';
import AppSidebar from '@/layouts/AppSidebar';
import './LinesPage.css';

type LineType = {
  type: string;
  name: string;
  description: string;
};

type LineOutbound = {
  type: string;
  host: string;
  port: number;
  username: string;
};

type LineProfile = {
  id: number;
  name: string;
  type: string;
  status: string;
  chainText: string;
  entryHost: string;
  entryPort: number;
  lastError: string;
  lastCheckAt: number;
};

type LineDetail = LineProfile & {
  outbound?: LineOutbound;
  config?: Record<string, string>;
  plan?: LineApplyPlan;
  logs?: LineApplyLog[];
};

type LineApplyPlan = {
  title: string;
  summary: string[];
  xrayInbound: Record<string, unknown>;
  xrayOutbound: Record<string, unknown>;
  nginx?: string;
  checks: string[];
};

type LineApplyLog = {
  id: number;
  action: string;
  level: string;
  message: string;
  detail: string;
  createdAt: number;
};

type LineCheckResponse = {
  status: string;
  passCount: number;
  warnCount: number;
  failCount: number;
  items: Array<{
    name: string;
    status: string;
    message: string;
  }>;
};

type LineShareResponse = {
  links: Array<{
    label: string;
    uri: string;
  }>;
};

type LineDeleteResult = {
  id: number;
  name: string;
  success: boolean;
  message?: string;
};

type LineColumnKey = 'status' | 'name' | 'chain' | 'entry' | 'actions';

const lineColumnDefaults: Record<LineColumnKey, number> = {
  status: 96,
  name: 220,
  chain: 460,
  entry: 260,
  actions: 190,
};

const lineColumnMinimums: Record<LineColumnKey, number> = {
  status: 80,
  name: 150,
  chain: 320,
  entry: 180,
  actions: 160,
};

const lineColumnWidthsStorageKey = 'line-table-column-widths-v1';

type LineFormValues = {
  name?: string;
  entryHost?: string;
  entryPort?: number;
  outboundType?: string;
  outboundHost?: string;
  outboundPort?: number;
  outboundUsername?: string;
  outboundPassword?: string;
  wsPath?: string;
  localXrayPort?: number;
  nginxConfigPath?: string;
  nginxApply?: boolean;
  realitySni?: string;
  realityShortId?: string;
};

type LineSavePayload = {
  type: string;
  name: string;
  entryHost: string;
  entryPort: number;
  outboundType: string;
  outboundHost: string;
  outboundPort: number;
  outboundUsername: string;
  outboundPassword: string;
  config: Record<string, string>;
};

type LineApplyResult = {
  line: LineDetail;
  errorMessage?: string;
};

async function fetchLineTypes(): Promise<LineType[]> {
  const msg = await HttpUtil.get<LineType[]>('/panel/api/line-types', undefined, { silent: true });
  if (!msg.success) throw new Error(msg.msg || 'Failed to fetch line types');
  return Array.isArray(msg.obj) ? msg.obj : [];
}

async function fetchLines(): Promise<LineProfile[]> {
  const msg = await HttpUtil.get<LineProfile[]>('/panel/api/lines', undefined, { silent: true });
  if (!msg.success) throw new Error(msg.msg || 'Failed to fetch lines');
  return Array.isArray(msg.obj) ? msg.obj : [];
}

async function fetchLine(id: number): Promise<LineDetail> {
  const msg = await HttpUtil.get<LineDetail>(`/panel/api/lines/${id}`, undefined, { silent: true });
  if (!msg.success || !msg.obj) throw new Error(msg.msg || 'Failed to fetch line');
  return msg.obj;
}

async function createLine(payload: LineSavePayload): Promise<LineDetail> {
  const msg = await HttpUtil.post<LineDetail>('/panel/api/lines', payload, {
    headers: { 'Content-Type': 'application/json' },
    silent: true,
  });
  if (!msg.success || !msg.obj) throw new Error(msg.msg || 'Failed to save line');
  return msg.obj;
}

async function updateLine(id: number, payload: LineSavePayload): Promise<LineDetail> {
  const msg = await HttpUtil.post<LineDetail>(`/panel/api/lines/${id}`, payload, {
    headers: { 'Content-Type': 'application/json' },
    silent: true,
  });
  if (!msg.success || !msg.obj) throw new Error(msg.msg || 'Failed to save line');
  return msg.obj;
}

async function applyLine(id: number): Promise<LineApplyResult> {
  const msg = await HttpUtil.post<LineDetail>(`/panel/api/lines/${id}/apply`, {}, {
    headers: { 'Content-Type': 'application/json' },
    silent: true,
  });
  if (!msg.obj) throw new Error(msg.msg || 'Failed to apply line');
  return {
    line: msg.obj,
    errorMessage: msg.success ? undefined : (msg.msg || '线路应用失败'),
  };
}

async function checkLine(id: number): Promise<LineCheckResponse> {
  const msg = await HttpUtil.post<LineCheckResponse>(`/panel/api/lines/${id}/check`, {}, {
    headers: { 'Content-Type': 'application/json' },
  });
  if (!msg.success || !msg.obj) throw new Error(msg.msg || 'Failed to check line');
  return msg.obj;
}

async function fetchLineShare(id: number): Promise<LineShareResponse> {
  const msg = await HttpUtil.get<LineShareResponse>(`/panel/api/lines/${id}/share`);
  if (!msg.success || !msg.obj) throw new Error(msg.msg || 'Failed to generate share link');
  return msg.obj;
}

async function deleteLine(id: number): Promise<LineDeleteResult> {
  const msg = await HttpUtil.post<LineDeleteResult>(`/panel/api/lines/${id}/delete`, {}, { silent: true });
  if (!msg.success || !msg.obj) throw new Error(msg.msg || 'Failed to delete line');
  return msg.obj;
}

async function deleteLines(ids: number[]): Promise<LineDeleteResult[]> {
  const msg = await HttpUtil.post<LineDeleteResult[]>('/panel/api/lines/batch-delete', { ids }, { silent: true });
  if (!Array.isArray(msg.obj)) throw new Error(msg.msg || 'Failed to delete lines');
  return msg.obj;
}

function lineTypeFromPath(pathname: string) {
  if (pathname.includes('/deploy/reality')) return 'reality_direct';
  return 'cloudflare_ws_tls';
}

function formatLineTime(value: number) {
  if (!value) return '尚未执行';
  return new Date(value * 1000).toLocaleString('zh-CN', { hour12: false });
}

function LinePageShell({ children }: { children: ReactNode }) {
  return (
    <Layout className="lines-layout">
      <AppSidebar />
      <Layout.Content className="lines-content">{children}</Layout.Content>
    </Layout>
  );
}

function typeLabel(type: string) {
  switch (type) {
    case 'cloudflare_ws_tls':
      return 'Cloudflare 主线路';
    case 'reality_direct':
      return 'Reality 直连';
    case 'trojan_direct':
      return 'Trojan 直连';
    default:
      return type;
  }
}

function defaultEntryPort(type: string) {
  return type === 'cloudflare_ws_tls' ? 8443 : 443;
}

function statusTag(status: string) {
  const color = status === 'active' ? 'green' : status === 'failed' || status === 'apply_failed' ? 'red' : status === 'warning' || status === 'pending_apply' || status === 'applying' || status === 'pending_check' ? 'gold' : 'default';
  const text = status === 'active' ? '正常' : status === 'failed' || status === 'apply_failed' ? '异常' : status === 'warning' ? '警告' : status === 'applying' ? '应用中' : status === 'pending_check' ? '待检测' : status === 'pending_apply' ? '待应用' : '草稿';
  return <Tag color={color}>{text}</Tag>;
}

function buildPayload(type: string, values: LineFormValues): LineSavePayload {
  const config: Record<string, string> = {};
  if (values.wsPath) config.wsPath = values.wsPath;
  if (values.localXrayPort) config.localXrayPort = String(values.localXrayPort);
  if (values.nginxConfigPath) config.nginxConfigPath = values.nginxConfigPath;
  config.nginxApply = values.nginxApply ? 'true' : 'false';
  if (values.realitySni) config.realitySni = values.realitySni;
  if (values.realityShortId) config.realityShortId = values.realityShortId;

  return {
    type,
    name: values.name?.trim() || typeLabel(type),
    entryHost: values.entryHost?.trim() || '',
    entryPort: values.entryPort ?? defaultEntryPort(type),
    outboundType: values.outboundType || 'socks5',
    outboundHost: values.outboundHost?.trim() || '',
    outboundPort: values.outboundPort ?? 0,
    outboundUsername: values.outboundUsername?.trim() || '',
    outboundPassword: values.outboundPassword || '',
    config,
  };
}

function detailToFormValues(line: LineDetail): LineFormValues {
  const config = line.config ?? {};
  return {
    name: line.name,
    entryHost: line.entryHost,
    entryPort: line.entryPort,
    outboundType: line.outbound?.type || 'socks5',
    outboundHost: line.outbound?.host || '',
    outboundPort: line.outbound?.port || undefined,
    outboundUsername: line.outbound?.username || '',
    wsPath: config.wsPath,
    localXrayPort: config.localXrayPort ? Number(config.localXrayPort) : undefined,
    nginxConfigPath: config.nginxConfigPath,
    nginxApply: config.nginxApply === 'true',
    realitySni: config.realitySni,
    realityShortId: config.realityShortId,
  };
}

function JsonPreview({ value }: { value: unknown }) {
  return <pre className="line-code-preview">{JSON.stringify(value, null, 2)}</pre>;
}

function LinePlanPanel({ line }: { line: LineDetail }) {
  if (!line.plan) return null;
  return (
    <section className="line-plan-shell">
      <Collapse
        size="small"
        items={[
          {
            key: 'plan',
            label: '部署计划',
            children: (
              <>
                <ul className="line-plan-list">
                  {line.plan.summary.map((item) => (
                    <li key={item}>{item}</li>
                  ))}
                </ul>
                <div className="line-plan-grid">
                  <div className="line-plan-box">
                    <Typography.Title level={5}>应用前检测</Typography.Title>
                    <ul className="line-plan-list">
                      {line.plan.checks.map((item) => (
                        <li key={item}>{item}</li>
                      ))}
                    </ul>
                  </div>
                </div>

                <div className="line-plan-grid">
                  <div className="line-plan-box">
                    <Typography.Title level={5}>Xray 入站草案</Typography.Title>
                    <JsonPreview value={line.plan.xrayInbound} />
                  </div>
                  <div className="line-plan-box">
                    <Typography.Title level={5}>Xray 出站草案</Typography.Title>
                    <JsonPreview value={line.plan.xrayOutbound} />
                  </div>
                </div>

                {line.plan.nginx && (
                  <div className="line-plan-box">
                    <Typography.Title level={5}>Nginx 草案</Typography.Title>
                    <pre className="line-code-preview">{line.plan.nginx}</pre>
                  </div>
                )}
              </>
            ),
          },
        ]}
      />
    </section>
  );
}

function LineShareModal({
  open,
  data,
  onClose,
}: {
  open: boolean;
  data: LineShareResponse | null;
  onClose: () => void;
}) {
  const firstLink = data?.links?.[0]?.uri ?? '';
  const qrCodeRef = useRef<HTMLDivElement>(null);

  const copyQRCodeImage = async () => {
    const canvas = qrCodeRef.current?.querySelector('canvas');
    if (!canvas) {
      message.error('二维码尚未生成，请稍后重试');
      return;
    }
    if (!window.isSecureContext || !navigator.clipboard?.write || typeof ClipboardItem === 'undefined') {
      message.error('当前浏览器不支持复制二维码图片');
      return;
    }
    const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/png'));
    if (!blob) {
      message.error('二维码图片生成失败');
      return;
    }
    try {
      await navigator.clipboard.write([new ClipboardItem({ 'image/png': blob })]);
      message.success('二维码图片已复制');
    } catch {
      message.error('复制二维码图片失败，请检查浏览器权限');
    }
  };

  return (
    <Modal title="分享线路" open={open} onCancel={onClose} footer={null} width={560}>
      {firstLink ? (
        <Space direction="vertical" size="middle" style={{ width: '100%' }}>
          <div className="line-share-qr" ref={qrCodeRef}>
            <QRCode value={firstLink} size={220} type="canvas" bordered />
            <Button icon={<CopyOutlined />} onClick={() => void copyQRCodeImage()}>
              复制二维码图片
            </Button>
          </div>
          {data?.links.map((link) => (
            <div key={link.uri}>
              <Typography.Text strong>{link.label}</Typography.Text>
              <Input.TextArea value={link.uri} autoSize={{ minRows: 3, maxRows: 5 }} readOnly />
              <Button
                icon={<CopyOutlined />}
                onClick={async () => {
                  const ok = await ClipboardManager.copyText(link.uri);
                  message[ok ? 'success' : 'error'](ok ? '已复制' : '复制失败');
                }}
              >
                复制链接
              </Button>
            </div>
          ))}
        </Space>
      ) : (
        <Empty description="暂无分享链接" />
      )}
    </Modal>
  );
}

function LineEditor({
  type,
  selectedType,
  initialValues,
  submitText,
  saving,
  onSubmit,
}: {
  type: string;
  selectedType: LineType;
  initialValues?: LineFormValues;
  submitText: string;
  saving: boolean;
  onSubmit: (values: LineFormValues) => void;
}) {
  const [form] = Form.useForm<LineFormValues>();

  const validateHost = (label: string) => async (_: unknown, value?: string) => {
    const host = value?.trim() || '';
    if (!host) return Promise.reject(new Error(`请填写${label}`));
    if (/\s|[\\/]/.test(host)) return Promise.reject(new Error(`${label}不能包含空格或路径字符`));
    return Promise.resolve();
  };

  const validateOptionalWSPath = async (_: unknown, value?: string) => {
    const path = value?.trim() || '';
    if (!path) return Promise.resolve();
    if (!path.startsWith('/') || /[\s?#]/.test(path)) {
      return Promise.reject(new Error('WS 路径必须以 / 开头，且不能包含空格、? 或 #'));
    }
    return Promise.resolve();
  };

  const validateOptionalShortID = async (_: unknown, value?: string) => {
    const shortID = value?.trim() || '';
    if (!shortID) return Promise.resolve();
    if (!/^[0-9a-fA-F]{2,16}$/.test(shortID) || shortID.length % 2 !== 0) {
      return Promise.reject(new Error('Short ID 必须是 2 到 16 位的偶数长度十六进制字符'));
    }
    return Promise.resolve();
  };

  useEffect(() => {
    form.setFieldsValue({
      name: selectedType.name,
      entryPort: defaultEntryPort(type),
      outboundType: 'socks5',
      ...initialValues,
    });
  }, [form, initialValues, selectedType.name, type]);

  return (
    <section className="line-form-shell">
      <Form form={form} layout="vertical" onFinish={onSubmit}>
        <div className="line-form-grid">
          <Form.Item label="线路名称" name="name">
            <Input placeholder={selectedType.name} />
          </Form.Item>
          <Form.Item label="入口地址" name="entryHost" rules={[{ validator: validateHost('入口地址') }]}>
            <Input placeholder={type === 'cloudflare_ws_tls' ? 'Cloudflare 域名' : '服务器 IP 或域名'} />
          </Form.Item>
          <Form.Item label="入口端口" name="entryPort" rules={[{ required: true, message: '请填写入口端口' }, { type: 'number', min: 1, max: 65535, message: '端口必须在 1 到 65535 之间' }]}>
            <InputNumber min={1} max={65535} />
          </Form.Item>
          <Form.Item label="住宅出口类型" name="outboundType" rules={[{ required: true, message: '请选择住宅出口类型' }]}>
            <Select
              options={[
                { value: 'socks5', label: 'SOCKS5' },
                { value: 'http', label: 'HTTP' },
                { value: 'https', label: 'HTTPS' },
              ]}
            />
          </Form.Item>
          <Form.Item label="住宅出口地址" name="outboundHost" rules={[{ validator: validateHost('住宅出口地址') }]}>
            <Input />
          </Form.Item>
          <Form.Item label="住宅出口端口" name="outboundPort" rules={[{ required: true, message: '请填写住宅出口端口' }, { type: 'number', min: 1, max: 65535, message: '端口必须在 1 到 65535 之间' }]}>
            <InputNumber min={1} max={65535} />
          </Form.Item>
          <Form.Item label="住宅出口账号" name="outboundUsername">
            <Input />
          </Form.Item>
          <Form.Item label="住宅出口密码" name="outboundPassword">
            <Input.Password placeholder="详情页留空表示不修改" />
          </Form.Item>
        </div>

        {type === 'cloudflare_ws_tls' && (
          <div className="line-subsection">
            <Typography.Title level={5}>Cloudflare / Nginx</Typography.Title>
            <div className="line-form-grid">
              <Form.Item label="WS 路径" name="wsPath" rules={[{ validator: validateOptionalWSPath }]}>
                <Input placeholder="自动生成" />
              </Form.Item>
              <Form.Item
                label="本地 Xray 端口"
                name="localXrayPort"
                dependencies={['entryPort']}
                rules={[
                  ({ getFieldValue }) => ({
                    validator: async (_: unknown, value?: number) => {
                      if (value && value === getFieldValue('entryPort')) {
                        return Promise.reject(new Error('本地 Xray 端口不能与公网入口端口相同'));
                      }
                      return Promise.resolve();
                    },
                  }),
                ]}
              >
                <InputNumber min={1} max={65535} placeholder="自动寻找" />
              </Form.Item>
              <Form.Item label="Nginx 配置路径" name="nginxConfigPath">
                <Input placeholder="/etc/nginx/conf.d/x-ui-line-1.conf" />
              </Form.Item>
              <Form.Item label="写入 Nginx 并重载" name="nginxApply" valuePropName="checked">
                <Switch />
              </Form.Item>
            </div>
          </div>
        )}

        {type === 'reality_direct' && (
          <div className="line-subsection">
            <Typography.Title level={5}>Reality</Typography.Title>
            <div className="line-form-grid">
              <Form.Item label="SNI" name="realitySni" rules={[{ validator: async (_: unknown, value?: string) => !value?.trim() ? Promise.resolve() : validateHost('SNI')(_, value) }]}>
                <Input placeholder="自动测速选择" />
              </Form.Item>
              <Form.Item label="Short ID" name="realityShortId" rules={[{ validator: validateOptionalShortID }]}>
                <Input placeholder="自动生成" />
              </Form.Item>
            </div>
          </div>
        )}

        <Space className="line-actions">
          <Button type="primary" htmlType="submit" icon={<SaveOutlined />} loading={saving}>
            {submitText}
          </Button>
        </Space>
      </Form>
    </section>
  );
}

function LineTypePicker({ onSelect }: { onSelect: (type: string) => void }) {
  const options = [
    {
      type: 'cloudflare_ws_tls',
      name: 'Cloudflare 主线路',
      chain: ['用户', 'Cloudflare', 'Nginx -> Xray', '住宅出口'],
      description: '通过 Cloudflare 橙云接入，VPS 由 Nginx 转发到本地 Xray。',
    },
    {
      type: 'reality_direct',
      name: 'Reality 直连',
      chain: ['用户', 'VPS Reality', 'Xray', '住宅出口'],
      description: '用户直接连接 VPS 的 Reality 入站，不经过 Cloudflare。',
    },
  ];

  return (
    <main className="lines-page">
      <div className="lines-header">
        <div>
          <Typography.Title level={3}>线路部署</Typography.Title>
          <Typography.Text type="secondary">选择线路类型后填写部署信息</Typography.Text>
        </div>
      </div>
      <div className="line-type-grid">
        {options.map((option) => (
          <section key={option.type} className="line-type-option">
            <Typography.Title level={4}>{option.name}</Typography.Title>
            <Typography.Paragraph type="secondary">{option.description}</Typography.Paragraph>
            <ol className="line-type-chain">
              {option.chain.map((item) => <li key={item}>{item}</li>)}
            </ol>
            <Button type="primary" onClick={() => onSelect(option.type)}>开始部署</Button>
          </section>
        ))}
      </div>
    </main>
  );
}

function LineOperations({ logs }: { logs?: LineApplyLog[] }) {
  if (!logs?.length) return <Empty description="暂无操作记录" />;
  return (
    <div className="line-log-list">
      {logs.map((log) => (
        <div key={log.id} className="line-log-item">
          <div className="line-log-heading">
            <Tag color={log.level === 'error' ? 'red' : log.level === 'warning' ? 'gold' : 'blue'}>{log.action}</Tag>
            <Typography.Text strong>{log.message}</Typography.Text>
            <Typography.Text type="secondary">{formatLineTime(log.createdAt)}</Typography.Text>
          </div>
          {log.detail && <Typography.Paragraph type="secondary">{log.detail}</Typography.Paragraph>}
        </div>
      ))}
    </div>
  );
}

type DiagnosticRow = LineApplyLog & {
  lineId: number;
  lineName: string;
};

function DiagnosticsPage({
  lines,
  details,
  checking,
  onCheckAll,
  onOpenLine,
}: {
  lines: LineProfile[];
  details: Array<LineDetail | undefined>;
  checking: boolean;
  onCheckAll: () => void;
  onOpenLine: (id: number) => void;
}) {
  const [lineFilter, setLineFilter] = useState('all');
  const [levelFilter, setLevelFilter] = useState('all');
  const [selected, setSelected] = useState<DiagnosticRow | null>(null);
  const rows = useMemo<DiagnosticRow[]>(() => details.flatMap((detail) => {
    if (!detail) return [];
    return (detail.logs ?? []).map((log) => ({
      ...log,
      lineId: detail.id,
      lineName: detail.name || typeLabel(detail.type),
    }));
  }).sort((a, b) => b.createdAt - a.createdAt), [details]);
  const filteredRows = rows.filter((row) => (lineFilter === 'all' || String(row.lineId) === lineFilter) && (levelFilter === 'all' || row.level === levelFilter));
  const abnormal = lines.filter((line) => line.status === 'apply_failed' || line.status === 'failed').length;
  const pending = lines.filter((line) => line.status === 'pending_apply' || line.status === 'pending_check' || line.status === 'applying').length;
  const normal = lines.filter((line) => line.status === 'active').length;
  const columns: ColumnsType<DiagnosticRow> = [
    { title: '时间', width: 180, render: (_, row) => formatLineTime(row.createdAt) },
    { title: '线路', render: (_, row) => <button type="button" className="line-link-button" onClick={() => onOpenLine(row.lineId)}>{row.lineName}</button> },
    { title: '检测项', dataIndex: 'action', width: 120 },
    { title: '结果', width: 90, render: (_, row) => <Tag color={row.level === 'error' ? 'red' : row.level === 'warning' ? 'gold' : 'blue'}>{row.level === 'error' ? '异常' : row.level === 'warning' ? '警告' : '记录'}</Tag> },
    { title: '摘要', dataIndex: 'message', render: (_, row) => <button type="button" className="diagnostic-summary-button" onClick={() => setSelected(row)}>{row.message}</button> },
  ];

  return (
    <>
      <main className="lines-page">
        <div className="lines-header">
          <div>
            <Typography.Title level={3}>诊断日志</Typography.Title>
            <Typography.Text type="secondary">集中查看所有线路的应用、检测和异常记录</Typography.Text>
          </div>
          <Button type="primary" icon={<CheckCircleOutlined />} loading={checking} onClick={onCheckAll}>执行全局检测</Button>
        </div>
        <div className="diagnostic-status-strip">
          <span>正常线路 <strong>{normal}</strong></span>
          <span>待处理 <strong>{pending}</strong></span>
          <span>异常线路 <strong className={abnormal ? 'diagnostic-error-count' : ''}>{abnormal}</strong></span>
        </div>
        <div className="diagnostic-filters">
          <Select value={lineFilter} onChange={setLineFilter} options={[{ value: 'all', label: '全部线路' }, ...lines.map((line) => ({ value: String(line.id), label: line.name || typeLabel(line.type) }))]} />
          <Select value={levelFilter} onChange={setLevelFilter} options={[{ value: 'all', label: '全部结果' }, { value: 'error', label: '异常' }, { value: 'warning', label: '警告' }, { value: 'info', label: '正常记录' }]} />
        </div>
        <Table rowKey={(row) => `${row.lineId}-${row.id}`} columns={columns} dataSource={filteredRows} pagination={{ pageSize: 12 }} locale={{ emptyText: <Empty description="暂无诊断记录" /> }} />
      </main>
      <Drawer title="诊断详情" open={Boolean(selected)} onClose={() => setSelected(null)} width={480}>
        {selected && (
          <Space direction="vertical" size="middle" style={{ width: '100%' }}>
            <div><Typography.Text type="secondary">线路</Typography.Text><br /><Typography.Text strong>{selected.lineName}</Typography.Text></div>
            <div><Typography.Text type="secondary">检测项</Typography.Text><br /><Tag color={selected.level === 'error' ? 'red' : selected.level === 'warning' ? 'gold' : 'blue'}>{selected.action}</Tag></div>
            <div><Typography.Text type="secondary">原因</Typography.Text><br /><Typography.Text>{selected.detail || selected.message}</Typography.Text></div>
            <div><Typography.Text type="secondary">时间</Typography.Text><br /><Typography.Text>{formatLineTime(selected.createdAt)}</Typography.Text></div>
            <Button onClick={() => onOpenLine(selected.lineId)}>打开线路详情</Button>
          </Space>
        )}
      </Drawer>
    </>
  );
}

export default function LinesPage() {
  const location = useLocation();
  const navigate = useNavigate();
  const params = useParams();
  const queryClient = useQueryClient();
  const [shareData, setShareData] = useState<LineShareResponse | null>(null);
  const [shareOpen, setShareOpen] = useState(false);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [selectedLineIds, setSelectedLineIds] = useState<Key[]>([]);
  const [columnWidths, setColumnWidths] = useState<Record<LineColumnKey, number>>(() => {
    try {
      const stored = localStorage.getItem(lineColumnWidthsStorageKey);
      if (!stored) return lineColumnDefaults;
      const parsed = JSON.parse(stored) as Partial<Record<LineColumnKey, number>>;
      return {
        status: Math.max(lineColumnMinimums.status, parsed.status ?? lineColumnDefaults.status),
        name: Math.max(lineColumnMinimums.name, parsed.name ?? lineColumnDefaults.name),
        chain: Math.max(lineColumnMinimums.chain, parsed.chain ?? lineColumnDefaults.chain),
        entry: Math.max(lineColumnMinimums.entry, parsed.entry ?? lineColumnDefaults.entry),
        actions: Math.max(lineColumnMinimums.actions, parsed.actions ?? lineColumnDefaults.actions),
      };
    } catch {
      return lineColumnDefaults;
    }
  });
  const isDeployPicker = location.pathname === '/lines/deploy';
  const isDeployForm = location.pathname === '/lines/deploy/cloudflare' || location.pathname === '/lines/deploy/reality';
  const isDiagnostics = location.pathname === '/diagnostics';
  const isLineEdit = location.pathname.endsWith('/edit');
  const lineId = params.id ? Number(params.id) : 0;
  const currentType = lineTypeFromPath(location.pathname);

  useEffect(() => {
    localStorage.setItem(lineColumnWidthsStorageKey, JSON.stringify(columnWidths));
  }, [columnWidths]);

  const { data: lineTypes = [] } = useQuery({
    queryKey: keys.lines.types(),
    queryFn: fetchLineTypes,
    staleTime: Infinity,
  });
  const { data: lines = [], isLoading } = useQuery({
    queryKey: keys.lines.list(),
    queryFn: fetchLines,
  });
  const { data: lineDetail, isLoading: isLineLoading } = useQuery({
    queryKey: keys.lines.detail(lineId),
    queryFn: () => fetchLine(lineId),
    enabled: lineId > 0,
  });
  const diagnosticQueries = useQueries({
    queries: isDiagnostics
      ? lines.map((line) => ({
          queryKey: keys.lines.detail(line.id),
          queryFn: () => fetchLine(line.id),
        }))
      : [],
  });
  const diagnosticDetails = diagnosticQueries.map((query) => query.data);

  const selectedType = useMemo(
    () => lineTypes.find((item) => item.type === currentType) ?? { type: currentType, name: typeLabel(currentType), description: '' },
    [currentType, lineTypes],
  );
  const detailType = lineDetail?.type ?? currentType;
  const detailSelectedType = useMemo(
    () => lineTypes.find((item) => item.type === detailType) ?? { type: detailType, name: typeLabel(detailType), description: '' },
    [detailType, lineTypes],
  );

  const createMutation = useMutation({
    mutationFn: async (values: LineFormValues) => {
      const saved = await createLine(buildPayload(currentType, values));
      return applyLine(saved.id);
    },
    onSuccess: async ({ line, errorMessage }) => {
      await queryClient.invalidateQueries({ queryKey: keys.lines.root() });
      queryClient.setQueryData(keys.lines.detail(line.id), line);
      if (errorMessage) {
        message.error(errorMessage);
        navigate(`/lines/${line.id}`);
        return;
      }
      message.success('线路已保存并应用，等待检测');
      navigate('/lines');
    },
    onError: (error) => message.error(error instanceof Error ? error.message : '线路保存失败'),
  });

  const updateMutation = useMutation({
    mutationFn: async (values: LineFormValues) => {
      const saved = await updateLine(lineId, buildPayload(detailType, values));
      return applyLine(saved.id);
    },
    onSuccess: async ({ line, errorMessage }) => {
      await queryClient.invalidateQueries({ queryKey: keys.lines.root() });
      queryClient.setQueryData(keys.lines.detail(line.id), line);
      if (errorMessage) {
        message.error(errorMessage);
        return;
      }
      message.success('线路已保存并应用，等待检测');
      navigate(`/lines/${line.id}`);
    },
    onError: (error) => message.error(error instanceof Error ? error.message : '线路保存失败'),
  });

  const checkMutation = useMutation({
    mutationFn: checkLine,
    onSuccess: async (result) => {
      await queryClient.invalidateQueries({ queryKey: keys.lines.root() });
      if (result.failCount > 0) {
        message.error(`检测失败 ${result.failCount} 项`);
      } else if (result.warnCount > 0) {
        message.warning(`检测完成，有 ${result.warnCount} 项警告`);
      } else {
        message.success('检测通过');
      }
    },
    onError: (error) => message.error(error instanceof Error ? error.message : '线路检测失败'),
  });

  const applyMutation = useMutation({
    mutationFn: applyLine,
    onSuccess: async ({ line, errorMessage }) => {
      await queryClient.invalidateQueries({ queryKey: keys.lines.root() });
      queryClient.setQueryData(keys.lines.detail(line.id), line);
      if (errorMessage) {
        message.error(errorMessage);
        return;
      }
      message.success('线路已重新应用，等待检测');
    },
    onError: (error) => message.error(error instanceof Error ? error.message : '线路应用失败'),
  });

  const allChecksMutation = useMutation({
    mutationFn: () => Promise.all(lines.map((line) => checkLine(line.id))),
    onSuccess: async (results) => {
      await queryClient.invalidateQueries({ queryKey: keys.lines.root() });
      const failed = results.reduce((count, result) => count + result.failCount, 0);
      if (failed > 0) message.error(`全局检测完成，发现 ${failed} 项异常`);
      else message.success('全局检测完成');
    },
    onError: (error) => message.error(error instanceof Error ? error.message : '全局检测失败'),
  });

  const shareMutation = useMutation({
    mutationFn: fetchLineShare,
    onSuccess: (result) => {
      setShareData(result);
      setShareOpen(true);
    },
    onError: (error) => message.error(error instanceof Error ? error.message : '生成分享链接失败'),
  });

  const deleteMutation = useMutation({
    mutationFn: deleteLine,
    onSuccess: async (result) => {
      await queryClient.invalidateQueries({ queryKey: keys.lines.root() });
      queryClient.removeQueries({ queryKey: keys.lines.detail(result.id) });
      setSelectedLineIds((ids) => ids.filter((id) => Number(id) !== result.id));
      message.success(`已删除线路：${result.name || result.id}`);
    },
    onError: (error) => message.error(error instanceof Error ? error.message : '删除线路失败'),
  });

  const batchDeleteMutation = useMutation({
    mutationFn: deleteLines,
    onSuccess: async (results) => {
      await queryClient.invalidateQueries({ queryKey: keys.lines.root() });
      results.filter((result) => result.success).forEach((result) => {
        queryClient.removeQueries({ queryKey: keys.lines.detail(result.id) });
      });
      const failures = results.filter((result) => !result.success);
      setSelectedLineIds(failures.map((result) => result.id));
      const successCount = results.length - failures.length;
      if (successCount > 0) message.success(`已删除 ${successCount} 条线路`);
      if (failures.length > 0) message.error(`${failures.length} 条线路删除失败，请查看错误信息后重试`);
    },
    onError: (error) => message.error(error instanceof Error ? error.message : '批量删除线路失败'),
  });

  const startColumnResize = (key: LineColumnKey, event: ReactPointerEvent<HTMLButtonElement>) => {
    if (event.button !== 0) return;
    event.preventDefault();
    const startX = event.clientX;
    const startWidth = columnWidths[key];
    document.body.classList.add('line-column-resizing');

    const onMove = (moveEvent: PointerEvent) => {
      const width = Math.min(800, Math.max(lineColumnMinimums[key], startWidth + moveEvent.clientX - startX));
      setColumnWidths((current) => ({ ...current, [key]: width }));
    };
    const onEnd = () => {
      document.body.classList.remove('line-column-resizing');
      document.removeEventListener('pointermove', onMove);
      document.removeEventListener('pointerup', onEnd);
      document.removeEventListener('pointercancel', onEnd);
    };
    document.addEventListener('pointermove', onMove);
    document.addEventListener('pointerup', onEnd);
    document.addEventListener('pointercancel', onEnd);
  };

  const columnTitle = (label: string, key: LineColumnKey) => (
    <div className="line-column-title">
      <span>{label}</span>
      <button
        type="button"
        className="line-column-resize-handle"
        aria-label={`调整${label}列宽度`}
        onPointerDown={(event) => startColumnResize(key, event)}
      />
    </div>
  );

  const confirmDeleteLine = (line: LineProfile) => {
    Modal.confirm({
      title: '删除线路？',
      content: `将删除“${line.name || typeLabel(line.type)}”及其 Xray、Nginx 配置。此操作不可恢复。`,
      okText: '删除',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: () => deleteMutation.mutateAsync(line.id),
    });
  };

  const confirmBatchDelete = () => {
    const selectedLines = lines.filter((line) => selectedLineIds.includes(line.id));
    if (!selectedLines.length) return;
    Modal.confirm({
      title: `删除 ${selectedLines.length} 条线路？`,
      content: '将删除所选线路及其 Xray、Nginx 配置。此操作不可恢复。',
      okText: '批量删除',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: () => batchDeleteMutation.mutateAsync(selectedLines.map((line) => line.id)),
    });
  };

  const runCheck = (id: number) => {
    checkMutation.mutate(id);
  };

  const openShare = (id: number) => {
    shareMutation.mutate(id);
  };

  const columns: ColumnsType<LineProfile> = [
    {
      title: columnTitle('状态', 'status'),
      dataIndex: 'status',
      width: columnWidths.status,
      render: statusTag,
    },
    {
      title: columnTitle('线路', 'name'),
      dataIndex: 'name',
      width: columnWidths.name,
      render: (_, row) => (
        <button type="button" className="line-link-button" onClick={() => navigate(`/lines/${row.id}`)}>
          {row.name || typeLabel(row.type)}
        </button>
      ),
    },
    {
      title: columnTitle('链路结构', 'chain'),
      dataIndex: 'chainText',
      width: columnWidths.chain,
      render: (value: string) => <span className="line-chain-cell">{value}</span>,
    },
    {
      title: columnTitle('入口', 'entry'),
      width: columnWidths.entry,
      render: (_, row) => row.entryHost ? `${row.entryHost}:${row.entryPort}` : '-',
    },
    {
      title: columnTitle('操作', 'actions'),
      width: columnWidths.actions,
      render: (_, row) => (
        <Space>
          <Button size="small" icon={<CheckCircleOutlined />} loading={checkMutation.isPending} onClick={() => runCheck(row.id)}>
            检测
          </Button>
          <Button size="small" icon={<QrcodeOutlined />} loading={shareMutation.isPending} onClick={() => openShare(row.id)}>
            分享
          </Button>
          <Dropdown
            trigger={['click']}
            menu={{
              items: [
                { key: 'details', label: '查看详情', onClick: () => navigate(`/lines/${row.id}`) },
                { key: 'edit', label: '编辑', onClick: () => navigate(`/lines/${row.id}/edit`) },
                { type: 'divider' },
                { key: 'delete', danger: true, icon: <DeleteOutlined />, label: '删除线路', onClick: () => confirmDeleteLine(row) },
              ],
            }}
          >
            <Button size="small" icon={<MoreOutlined />} aria-label={`更多操作：${row.name || typeLabel(row.type)}`} />
          </Dropdown>
        </Space>
      ),
    },
  ];

  if (isDiagnostics) {
    return (
      <LinePageShell>
        <DiagnosticsPage
          lines={lines}
          details={diagnosticDetails}
          checking={allChecksMutation.isPending}
          onCheckAll={() => allChecksMutation.mutate()}
          onOpenLine={(id) => navigate(`/lines/${id}`)}
        />
      </LinePageShell>
    );
  }

  if (lineId > 0) {
    if (isLineEdit) {
      return (
        <LinePageShell>
          <main className="lines-page">
            <div className="lines-header">
              <div>
                <Button type="link" className="line-back-button" onClick={() => navigate(`/lines/${lineId}`)}>返回线路详情</Button>
                <Typography.Title level={3}>编辑 {lineDetail?.name || '线路'}</Typography.Title>
                <Typography.Text type="secondary">修改后需要重新应用服务器配置</Typography.Text>
              </div>
            </div>
            {isLineLoading || !lineDetail ? (
              <section className="line-form-shell line-empty-panel"><Empty description="正在读取线路详情" /></section>
            ) : (
              <LineEditor
                type={detailType}
                selectedType={detailSelectedType}
                initialValues={detailToFormValues(lineDetail)}
                submitText="保存并应用"
                saving={updateMutation.isPending}
                onSubmit={(values) => updateMutation.mutate(values)}
              />
            )}
          </main>
          <LineShareModal open={shareOpen} data={shareData} onClose={() => setShareOpen(false)} />
        </LinePageShell>
      );
    }

    return (
      <LinePageShell>
        <main className="lines-page">
          <div className="lines-header">
            <div>
              <Button type="link" className="line-back-button" onClick={() => navigate('/lines')}>返回线路列表</Button>
              <Space size="small" align="center">
                <Typography.Title level={3}>{lineDetail?.name || '线路详情'}</Typography.Title>
                {lineDetail && statusTag(lineDetail.status)}
              </Space>
              <Typography.Text type="secondary">{lineDetail?.entryHost ? `${lineDetail.entryHost}:${lineDetail.entryPort}` : detailSelectedType.description}</Typography.Text>
            </div>
            <Space wrap>
              <Button icon={<EditOutlined />} onClick={() => navigate(`/lines/${lineId}/edit`)}>编辑</Button>
              <Button icon={<CheckCircleOutlined />} loading={checkMutation.isPending} onClick={() => runCheck(lineId)}>
                检测
              </Button>
              <Button icon={<QrcodeOutlined />} loading={shareMutation.isPending} onClick={() => openShare(lineId)}>
                分享
              </Button>
              <Button type="primary" icon={<SaveOutlined />} loading={applyMutation.isPending} onClick={() => applyMutation.mutate(lineId)}>
                重新应用
              </Button>
              <Button icon={<MoreOutlined />} onClick={() => setAdvancedOpen(true)} aria-label="更多线路操作" />
            </Space>
          </div>
          {isLineLoading || !lineDetail ? (
            <section className="line-form-shell line-empty-panel">
              <Empty description="正在读取线路详情" />
            </section>
          ) : (
            <>
              {lineDetail.lastError && <Alert className="line-detail-alert" type="error" showIcon message="当前线路存在异常" description={lineDetail.lastError} />}
              <Tabs
                className="line-detail-tabs"
                items={[
                  {
                    key: 'overview',
                    label: '概览',
                    children: (
                      <Descriptions className="line-overview" bordered size="small" column={{ xs: 1, md: 2 }} items={[
                        { key: 'type', label: '线路类型', children: typeLabel(lineDetail.type) },
                        { key: 'status', label: '当前状态', children: statusTag(lineDetail.status) },
                        { key: 'entry', label: '入口', children: lineDetail.entryHost ? `${lineDetail.entryHost}:${lineDetail.entryPort}` : '-' },
                        { key: 'outbound', label: '住宅出口', children: lineDetail.outbound?.host ? `${lineDetail.outbound.type.toUpperCase()} / ${lineDetail.outbound.host}:${lineDetail.outbound.port}` : '-' },
                        { key: 'check', label: '最近检测', children: formatLineTime(lineDetail.lastCheckAt) },
                        { key: 'name', label: '线路名称', children: lineDetail.name || typeLabel(lineDetail.type) },
                      ]} />),
                  },
                  { key: 'operations', label: '操作记录', children: <LineOperations logs={lineDetail.logs} /> },
                  {
                    key: 'health',
                    label: '检测结果',
                    children: (
                      <div className="line-health-panel">
                        <Typography.Title level={5}>最近检测</Typography.Title>
                        <Space direction="vertical">
                          {statusTag(lineDetail.status)}
                          <Typography.Text type="secondary">最近检测时间：{formatLineTime(lineDetail.lastCheckAt)}</Typography.Text>
                          <Button icon={<CheckCircleOutlined />} loading={checkMutation.isPending} onClick={() => runCheck(lineId)}>重新检测</Button>
                        </Space>
                      </div>
                    ),
                  },
                ]}
              />
            </>
          )}
        </main>
        <Drawer title="高级部署信息" open={advancedOpen} onClose={() => setAdvancedOpen(false)} width={720}>
          {lineDetail && <LinePlanPanel line={lineDetail} />}
        </Drawer>
        <LineShareModal open={shareOpen} data={shareData} onClose={() => setShareOpen(false)} />
      </LinePageShell>
    );
  }

  if (isDeployPicker) {
    return <LinePageShell><LineTypePicker onSelect={(type) => navigate(type === 'reality_direct' ? '/lines/deploy/reality' : '/lines/deploy/cloudflare')} /></LinePageShell>;
  }

  if (isDeployForm) {
    return (
      <LinePageShell>
      <main className="lines-page">
        <div className="lines-header">
          <div>
            <Button type="link" className="line-back-button" onClick={() => navigate('/lines/deploy')}>返回线路类型</Button>
            <Typography.Title level={3}>{selectedType.name}</Typography.Title>
            <Typography.Text type="secondary">{selectedType.description}</Typography.Text>
          </div>
        </div>

        <LineEditor
          type={currentType}
          selectedType={selectedType}
          submitText="保存并应用"
          saving={createMutation.isPending}
          onSubmit={(values) => createMutation.mutate(values)}
        />
      </main>
      </LinePageShell>
    );
  }

  return (
    <LinePageShell>
      <main className="lines-page">
        <div className="lines-header">
          <div>
            <Typography.Title level={3}>线路列表</Typography.Title>
            <Typography.Text type="secondary">管理已部署线路，新增线路请进入左侧“线路部署”</Typography.Text>
          </div>
        </div>

        {selectedLineIds.length > 0 && (
          <div className="line-batch-toolbar">
            <Typography.Text>已选择 {selectedLineIds.length} 条线路</Typography.Text>
            <Button
              danger
              icon={<DeleteOutlined />}
              loading={batchDeleteMutation.isPending}
              onClick={confirmBatchDelete}
            >
              删除所选
            </Button>
          </div>
        )}

        <Table
          rowKey="id"
          columns={columns}
          dataSource={lines}
          loading={isLoading}
          pagination={false}
          tableLayout="fixed"
          scroll={{ x: Object.values(columnWidths).reduce((total, width) => total + width, 0) + 56 }}
          rowSelection={{
            selectedRowKeys: selectedLineIds,
            onChange: (ids) => setSelectedLineIds(ids),
          }}
          locale={{ emptyText: <Empty description="暂无线路" /> }}
        />
      </main>
      <LineShareModal open={shareOpen} data={shareData} onClose={() => setShareOpen(false)} />
    </LinePageShell>
  );
}
