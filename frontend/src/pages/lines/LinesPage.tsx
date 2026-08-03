import { useEffect, useMemo, useRef, useState } from 'react';
import type { Key, PointerEvent as ReactPointerEvent, ReactNode } from 'react';
import { useLocation, useNavigate, useParams } from 'react-router';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Collapse, DatePicker, Descriptions, Drawer, Dropdown, Empty, Form, Input, InputNumber, Layout, Modal, QRCode, Select, Space, Switch, Table, Tabs, Tag, Typography, Upload, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { UploadFile } from 'antd';
import dayjs from 'dayjs';
import type { Dayjs } from 'dayjs';
import { CheckCircleOutlined, CopyOutlined, DeleteOutlined, DownloadOutlined, EditOutlined, MoreOutlined, QrcodeOutlined, RedoOutlined, SaveOutlined } from '@ant-design/icons';

import { keys } from '@/api/queryKeys';
import { useWebSocket } from '@/hooks/useWebSocket';
import { ClipboardManager, FileManager, HttpUtil, SizeFormatter } from '@/utils';
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
  lastInboundLatencyMs?: number;
  lastOutboundLatencyMs?: number;
  createdAt: number;
  updatedAt: number;
  validFrom: number;
  validUntil: number;
  expiredAt: number;
  manualReenableRequired: boolean;
};

type LineDetail = LineProfile & {
  outbound?: LineOutbound;
  config?: Record<string, string>;
  plan?: LineApplyPlan;
};

type LineApplyPlan = {
  title: string;
  summary: string[];
  xrayInbound: Record<string, unknown>;
  xrayOutbound: Record<string, unknown>;
  nginx?: string;
  checks: string[];
};

type LineDiagnosticEvent = {
  id: string;
  lineId: number;
  lineName: string;
  kind: 'check' | 'operation';
  action: string;
  level: 'info' | 'warning' | 'error';
  message: string;
  detail?: string;
  passCount?: number;
  warnCount?: number;
  failCount?: number;
  items?: LineCheckResponse['items'];
  createdAt: number;
};

type LineDiagnosticsResponse = {
  items: LineDiagnosticEvent[];
  total: number;
};

type LineCheckResponse = {
  status: string;
  passCount: number;
  warnCount: number;
  failCount: number;
  metrics?: LineMetrics;
  items: Array<{
    name: string;
    status: string;
    message: string;
  }>;
};

type LineMetrics = {
  lineId: number;
  inboundTag: string;
  outboundTag: string;
  inboundSpeedUp: number;
  inboundSpeedDown: number;
  outboundSpeedUp: number;
  outboundSpeedDown: number;
  inboundTraffic: number;
  outboundTraffic: number;
  totalTraffic: number;
  inboundLatencyMs: number;
  outboundLatencyMs: number;
  lastCheckedAt: number;
};

type LineTrafficSnapshot = Pick<LineMetrics, 'lineId' | 'inboundSpeedUp' | 'inboundSpeedDown' | 'outboundSpeedUp' | 'outboundSpeedDown'> & {
  collectedAt: number;
};

type LineShareResponse = {
  links: Array<{
    label: string;
    uri: string;
  }>;
  clashSubscription?: LineClashSubscriptionShare;
  clashSubscriptionError?: string;
};

type LineClashSubscriptionShare = {
  url: string;
  createdAt: number;
  rotatedAt: number;
};

type LineDeleteResult = {
  id: number;
  name: string;
  success: boolean;
  message?: string;
};

type LineColumnKey = 'status' | 'name' | 'validity' | 'speed' | 'outboundLatency' | 'traffic' | 'actions';

const lineColumnDefaults: Record<LineColumnKey, number> = {
  status: 72,
  name: 136,
  validity: 154,
  speed: 100,
  outboundLatency: 92,
  traffic: 96,
  actions: 150,
};

const lineColumnMinimums: Record<LineColumnKey, number> = {
  status: 62,
  name: 96,
  validity: 128,
  speed: 82,
  outboundLatency: 78,
  traffic: 80,
  actions: 108,
};

const lineColumnWidthsStorageKey = 'line-table-column-widths-v4';

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
  nginxCertFile?: string;
  nginxKeyFile?: string;
  nginxApply?: boolean;
	originHost?: string;
	originPort?: number;
  acmeEmail?: string;
  validFrom?: Dayjs;
  validUntil?: Dayjs;
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
  validFrom?: number;
  validUntil?: number;
  config: Record<string, string>;
};

type LineApplyResult = {
  line: LineDetail;
  errorMessage?: string;
  scheduled?: boolean;
};

type LineFormSubmission = {
  values: LineFormValues;
  certificateFile?: File;
  privateKeyFile?: File;
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

async function fetchLineMetrics(): Promise<LineMetrics[]> {
  const msg = await HttpUtil.get<LineMetrics[]>('/panel/api/lines/metrics', undefined, { silent: true });
  if (!msg.success) throw new Error(msg.msg || 'Failed to fetch line metrics');
  return Array.isArray(msg.obj) ? msg.obj : [];
}

async function fetchSingleLineMetrics(id: number): Promise<LineMetrics> {
  const msg = await HttpUtil.get<LineMetrics>(`/panel/api/lines/${id}/metrics`, undefined, { silent: true });
  if (!msg.success || !msg.obj) throw new Error(msg.msg || 'Failed to fetch line metrics');
  return msg.obj;
}

async function fetchLineDiagnostics(params: { page: number; pageSize: number; lineId?: number; kind?: string; level?: string }): Promise<LineDiagnosticsResponse> {
  const query = new URLSearchParams({ page: String(params.page), pageSize: String(params.pageSize) });
  if (params.lineId) query.set('lineId', String(params.lineId));
  if (params.kind) query.set('kind', params.kind);
  if (params.level) query.set('level', params.level);
  const msg = await HttpUtil.get<LineDiagnosticsResponse>(`/panel/api/lines/diagnostics?${query.toString()}`, undefined, { silent: true });
  if (!msg.success || !msg.obj) throw new Error(msg.msg || 'Failed to load diagnostics');
  return msg.obj;
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

async function uploadOriginCertificate(id: number, certificateFile: File, privateKeyFile: File): Promise<void> {
  const formData = new FormData();
  formData.append('certificate', certificateFile);
  formData.append('privateKey', privateKeyFile);
  const msg = await HttpUtil.post(`/panel/api/lines/${id}/origin-certificate`, formData, { silent: true });
  if (!msg.success) throw new Error(msg.msg || 'Origin certificate upload failed');
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

async function resetLineClashSubscription(id: number): Promise<LineClashSubscriptionShare> {
  const msg = await HttpUtil.post<LineClashSubscriptionShare>(`/panel/api/lines/${id}/clash-subscription/reset`, {}, {
    headers: { 'Content-Type': 'application/json' },
    silent: true,
  });
  if (!msg.success || !msg.obj) throw new Error(msg.msg || 'Failed to reset Clash subscription');
  return msg.obj;
}

async function downloadLineClashSubscription(id: number): Promise<void> {
  const basePath = window.X_UI_BASE_PATH || '';
  const response = await fetch(`${basePath}/panel/api/lines/${id}/clash-subscription/yaml`, {
    credentials: 'same-origin',
    headers: { 'X-Requested-With': 'XMLHttpRequest' },
  });
  if (!response.ok) throw new Error('下载 YAML 失败');
  FileManager.downloadTextFile(await response.text(), `relay-panel-line-${id}.yaml`, { type: 'application/yaml;charset=utf-8' });
}

async function updateLineValidity(id: number, validUntil: number): Promise<LineDetail> {
  const msg = await HttpUtil.post<LineDetail>(`/panel/api/lines/${id}/validity`, { validUntil }, {
    headers: { 'Content-Type': 'application/json' },
    silent: true,
  });
  if (!msg.success || !msg.obj) throw new Error(msg.msg || 'Failed to extend line validity');
  return msg.obj;
}

async function renewLine(id: number, validUntil: number): Promise<LineDetail> {
  const msg = await HttpUtil.post<LineDetail>(`/panel/api/lines/${id}/renew`, { validUntil }, {
    headers: { 'Content-Type': 'application/json' },
    silent: true,
  });
  if (!msg.success || !msg.obj) throw new Error(msg.msg || 'Failed to renew line');
  return msg.obj;
}

function lineTypeFromPath(pathname: string) {
  if (pathname.includes('/deploy/reality')) return 'reality_direct';
	if (pathname.includes('/deploy/bunny')) return 'bunny_ws_tls';
  return 'cloudflare_ws_tls';
}

function formatLineTime(value: number) {
  if (!value) return '尚未执行';
  return new Date(value * 1000).toLocaleString('zh-CN', { hour12: false });
}

function lineRequiresManualRenewal(line: LineProfile) {
  return line.manualReenableRequired || line.status === 'expired' || line.status === 'expiring' || line.status === 'expiry_failed';
}

function formatLineValidity(line: LineProfile) {
  const start = line.validFrom || line.createdAt;
  const date = (value: number) => dayjs(value * 1000).format('YYYY-MM-DD');
  if (!line.validUntil) return `起：${date(start)}`;
  return `起：${date(start)}\n止：${date(line.validUntil)}`;
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
	case 'bunny_ws_tls':
		return 'Bunny CDN WS';
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
  const color = status === 'active' ? 'green' : status === 'expired' || status === 'expiry_failed' || status === 'failed' || status === 'apply_failed' ? 'red' : status === 'warning' || status === 'pending_apply' || status === 'applying' || status === 'pending_check' || status === 'scheduled' || status === 'expiring' ? 'gold' : 'default';
  const text = status === 'active' ? '正常' : status === 'expired' ? '已过期' : status === 'expiry_failed' ? '过期清理异常' : status === 'scheduled' ? '待启用' : status === 'expiring' ? '停用中' : status === 'failed' || status === 'apply_failed' ? '异常' : status === 'warning' ? '警告' : status === 'applying' ? '应用中' : status === 'pending_check' ? '待检测' : status === 'pending_apply' ? '待应用' : '草稿';
  return <Tag color={color}>{text}</Tag>;
}

function formatLatency(value?: number) {
  return value && value > 0 ? `${value} ms` : '--';
}

function LineSpeedCell({ up, down }: { up?: number; down?: number }) {
  const hasTraffic = Boolean((up && up > 0) || (down && down > 0));
  if (!hasTraffic) {
    return (
      <div className="line-speed-cell">
        <span>--</span>
        <span>--</span>
      </div>
    );
  }
  return (
    <div className="line-speed-cell">
      <span className="line-speed-up">↑ {SizeFormatter.speedFormat(up)}</span>
      <span className="line-speed-down">↓ {SizeFormatter.speedFormat(down)}</span>
    </div>
  );
}

function LineChainDiagram({ line }: { line: LineDetail }) {
  const nodes = line.type === 'cloudflare_ws_tls' || line.type === 'bunny_ws_tls'
    ? ['手机/客户端', line.type === 'bunny_ws_tls' ? 'Bunny CDN' : 'Cloudflare', 'Nginx', `Xray 入站 line-${line.id}-in`, `Xray 出站 line-${line.id}-out`, '住宅代理', '测试网站']
    : ['手机/客户端', `Xray 入站 line-${line.id}-in`, `Xray 出站 line-${line.id}-out`, '住宅代理', '测试网站'];
  return (
    <section className="line-detail-card">
      <Typography.Title level={5}>链路结构</Typography.Title>
      <div className="line-chain-diagram">
        {nodes.map((node, index) => (
          <div className="line-chain-step" key={`${node}-${index}`}>
            <span>{node}</span>
            {index < nodes.length - 1 && <i aria-hidden="true" />}
          </div>
        ))}
      </div>
    </section>
  );
}

function LineRuntimeOverview({ line, metrics }: { line: LineDetail; metrics?: LineMetrics }) {
  const config = line.config ?? {};
  const start = line.validFrom || line.createdAt;
  const isWS = line.type === 'cloudflare_ws_tls' || line.type === 'bunny_ws_tls';
  const outbound = line.outbound?.host ? `${line.outbound.type.toUpperCase()} ${line.outbound.host}:${line.outbound.port}` : '--';
  const localPort = config.localXrayPort || '--';
  return (
    <section className="line-detail-card">
      <div className="line-runtime-heading">
        <div>
          <Typography.Title level={5}>线路运行概览</Typography.Title>
          <Typography.Text type="secondary">当前保存的线路配置与最近一次运行数据</Typography.Text>
        </div>
        {statusTag(line.status)}
      </div>
      <div className="line-runtime-grid">
        <div className="line-runtime-group">
          <Typography.Text strong>生命周期</Typography.Text>
          <Descriptions size="small" column={1} items={[
            { key: 'start', label: '开始时间', children: formatLineTime(start) },
            { key: 'end', label: '结束时间', children: line.validUntil ? formatLineTime(line.validUntil) : '长期有效' },
            { key: 'lock', label: '到期策略', children: lineRequiresManualRenewal(line) ? '已锁定，需人工续期启用' : '到期后强制断开' },
            { key: 'updated', label: '最近更新', children: formatLineTime(line.updatedAt) },
          ]} />
        </div>
        <div className="line-runtime-group">
          <Typography.Text strong>公网入口</Typography.Text>
          <Descriptions size="small" column={1} items={[
            { key: 'type', label: '线路类型', children: typeLabel(line.type) },
            { key: 'entry', label: '客户端入口', children: line.entryHost ? `${line.entryHost}:${line.entryPort}` : '--' },
            { key: 'transport', label: isWS ? 'WS 路径' : 'Reality SNI', children: isWS ? (config.wsPath || '--') : (config.realitySni || '--') },
            ...(isWS ? [{ key: 'nginx', label: 'Nginx 配置', children: config.nginxConfigPath || `x-ui-line-${line.id}.conf` }] : []),
          ]} />
        </div>
        <div className="line-runtime-group">
          <Typography.Text strong>Xray 与住宅出口</Typography.Text>
          <Descriptions size="small" column={1} items={[
            { key: 'inbound', label: '受管入站', children: `line-${line.id}-in` },
            { key: 'localPort', label: '本地端口', children: localPort },
            { key: 'outboundTag', label: '出站标签', children: metrics?.outboundTag || `line-${line.id}-out` },
            { key: 'outbound', label: '住宅出口', children: outbound },
          ]} />
        </div>
        <div className="line-runtime-group">
          <Typography.Text strong>最近运行数据</Typography.Text>
          <Descriptions size="small" column={1} items={[
            { key: 'speed', label: '实时吞吐', children: <LineSpeedCell up={metrics?.inboundSpeedUp} down={metrics?.inboundSpeedDown} /> },
            { key: 'traffic', label: '累计流量', children: SizeFormatter.sizeFormat(metrics?.totalTraffic) },
            { key: 'latency', label: '出站延迟', children: formatLatency(metrics?.outboundLatencyMs) },
            { key: 'check', label: '最近检测', children: formatLineTime(metrics?.lastCheckedAt || line.lastCheckAt) },
          ]} />
        </div>
      </div>
    </section>
  );
}

function buildPayload(type: string, values: LineFormValues, includeValidity = false): LineSavePayload {
  const config: Record<string, string> = {};
  if (values.wsPath) config.wsPath = values.wsPath;
  if (values.localXrayPort) config.localXrayPort = String(values.localXrayPort);
  if (values.nginxConfigPath) config.nginxConfigPath = values.nginxConfigPath;
  if (values.nginxCertFile) config.nginxCertFile = values.nginxCertFile;
  if (values.nginxKeyFile) config.nginxKeyFile = values.nginxKeyFile;
	config.nginxApply = type === 'bunny_ws_tls' ? 'true' : (values.nginxApply ? 'true' : 'false');
	if (values.originHost) config.originHost = values.originHost.trim();
	if (values.originPort) config.originPort = String(values.originPort);
	if (values.acmeEmail) config.acmeEmail = values.acmeEmail.trim();
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
    ...(includeValidity ? { validFrom: values.validFrom?.unix() ?? dayjs().unix(), validUntil: values.validUntil?.unix() ?? 0 } : {}),
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
    nginxCertFile: config.nginxCertFile,
    nginxKeyFile: config.nginxKeyFile,
    nginxApply: config.nginxApply === 'true',
	originHost: config.originHost,
	originPort: config.originPort ? Number(config.originPort) : undefined,
    acmeEmail: config.acmeEmail,
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
  lineId,
  resetting,
  onClose,
  onResetSubscription,
}: {
  open: boolean;
  data: LineShareResponse | null;
  lineId: number | null;
  resetting: boolean;
  onClose: () => void;
  onResetSubscription: () => Promise<unknown>;
}) {
  const firstLink = data?.links?.[0]?.uri ?? '';
  const subscriptionURL = data?.clashSubscription?.url ?? '';
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
      {data && firstLink ? (
        <Tabs
          items={[
            {
              key: 'vless',
              label: 'VLESS 链接',
              children: (
                <Space direction="vertical" size="middle" style={{ width: '100%' }}>
                  <div className="line-share-qr" ref={qrCodeRef}>
                    <QRCode value={firstLink} size={220} type="canvas" bordered />
                    <Button icon={<CopyOutlined />} onClick={() => void copyQRCodeImage()}>
                      复制二维码图片
                    </Button>
                  </div>
                  {data.links.map((link) => (
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
              ),
            },
            {
              key: 'clash',
              label: 'Clash 订阅',
              children: subscriptionURL ? (
                <Space direction="vertical" size="middle" style={{ width: '100%' }}>
                  <div className="line-share-qr" ref={qrCodeRef}>
                    <QRCode value={subscriptionURL} size={220} type="canvas" bordered />
                    <Button icon={<CopyOutlined />} onClick={() => void copyQRCodeImage()}>
                      复制二维码图片
                    </Button>
                  </div>
                  <Typography.Text strong>订阅地址</Typography.Text>
                  <Input.TextArea value={subscriptionURL} autoSize={{ minRows: 3, maxRows: 5 }} readOnly />
                  <Space wrap>
                    <Button
                      icon={<CopyOutlined />}
                      onClick={async () => {
                        const ok = await ClipboardManager.copyText(subscriptionURL);
                        message[ok ? 'success' : 'error'](ok ? '已复制订阅地址' : '复制失败');
                      }}
                    >
                      复制订阅地址
                    </Button>
                    <Button
                      icon={<DownloadOutlined />}
                      onClick={() => {
                        if (!lineId) return;
                        void downloadLineClashSubscription(lineId).catch((error) => message.error(error instanceof Error ? error.message : '下载 YAML 失败'));
                      }}
                    >
                      下载 YAML
                    </Button>
                    <Button
                      danger
                      icon={<RedoOutlined />}
                      loading={resetting}
                      onClick={() => {
                        Modal.confirm({
                          title: '重置 Clash 订阅地址？',
                          content: '旧订阅地址和旧订阅二维码将立即失效；已经下载的 YAML 与 VLESS 链接不受影响。',
                          okText: '重置',
                          okButtonProps: { danger: true },
                          cancelText: '取消',
                          onOk: onResetSubscription,
                        });
                      }}
                    >
                      重置订阅地址
                    </Button>
                  </Space>
                </Space>
              ) : (
                <Alert type="warning" showIcon message="Clash 订阅暂不可用" description={data.clashSubscriptionError || '请先完成 Cloudflare 主线路部署。'} />
              ),
            },
          ]}
        />
      ) : (
        <Empty description="暂无分享链接" />
      )}
    </Modal>
  );
}

function LineValidityModal({
  line,
  open,
  saving,
  onClose,
  onSubmit,
}: {
  line: LineProfile | null;
  open: boolean;
  saving: boolean;
  onClose: () => void;
  onSubmit: (validUntil: number) => void;
}) {
  const [form] = Form.useForm<{ validUntil?: Dayjs }>();
  const renewing = Boolean(line && lineRequiresManualRenewal(line));

  useEffect(() => {
    if (!open) return;
    form.setFieldsValue({
      validUntil: undefined,
    });
  }, [form, line, open]);

  return (
    <Modal
      title={renewing ? '续期并重新启用线路' : '延长线路有效期'}
      open={open}
      onCancel={onClose}
      okText={renewing ? '续期并重新启用' : '保存有效期'}
      cancelText="取消"
      okButtonProps={{ danger: renewing }}
      confirmLoading={saving}
      onOk={() => void form.validateFields().then((values) => onSubmit(values.validUntil?.unix() ?? 0))}
    >
      <Form form={form} layout="vertical">
        <Form.Item label="新的结束时间" name="validUntil">
          <DatePicker showTime style={{ width: '100%' }} disabledDate={(date) => date.endOf('day').isBefore(dayjs())} />
        </Form.Item>
        <Typography.Text type="secondary">留空即设为长期有效。</Typography.Text>
        {renewing && <Alert type="warning" showIcon message="该线路已到期，确认后会重新写入 Xray 和 Nginx 配置，并恢复连接。" />}
      </Form>
    </Modal>
  );
}

function LineEditor({
  type,
  selectedType,
  initialValues,
  submitText,
  saving,
  showValidity,
  onSubmit,
}: {
  type: string;
  selectedType: LineType;
  initialValues?: LineFormValues;
  submitText: string;
  saving: boolean;
  showValidity?: boolean;
  onSubmit: (submission: LineFormSubmission) => void;
}) {
  const [form] = Form.useForm<LineFormValues>();
  const [certificateFiles, setCertificateFiles] = useState<UploadFile[]>([]);
  const [privateKeyFiles, setPrivateKeyFiles] = useState<UploadFile[]>([]);

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
		originPort: type === 'bunny_ws_tls' ? 8443 : undefined,
      outboundType: 'socks5',
		validFrom: showValidity ? dayjs() : undefined,
      ...initialValues,
    });
  }, [form, initialValues, selectedType.name, showValidity, type]);

  const handleFinish = (values: LineFormValues) => {
    const certificateFile = certificateFiles[0]?.originFileObj;
    const privateKeyFile = privateKeyFiles[0]?.originFileObj;
    const hasManualCertificate = Boolean(values.nginxCertFile?.trim());
    const hasManualKey = Boolean(values.nginxKeyFile?.trim());
    const hasUpload = Boolean(certificateFile || privateKeyFile);
	if (type === 'cloudflare_ws_tls' && values.nginxApply && !((hasManualCertificate && hasManualKey) || (certificateFile && privateKeyFile))) {
      message.error(hasUpload ? '请同时选择源站证书和私钥文件' : '启用 Nginx 时请填写证书路径，或同时上传证书和私钥');
      return;
    }
	if (type === 'cloudflare_ws_tls' && ((hasManualCertificate && !hasManualKey) || (!hasManualCertificate && hasManualKey))) {
      message.error('手工证书路径和私钥路径必须同时填写');
      return;
    }
    onSubmit({ values, certificateFile, privateKeyFile });
  };

  return (
    <section className="line-form-shell">
      <Form form={form} layout="vertical" onFinish={handleFinish}>
        <div className="line-form-grid">
          <Form.Item label="线路名称" name="name">
            <Input placeholder={selectedType.name} />
          </Form.Item>
          <Form.Item label="入口地址" name="entryHost" rules={[{ validator: validateHost('入口地址') }]}>
            <Input placeholder={type === 'cloudflare_ws_tls' ? 'Cloudflare 域名' : type === 'bunny_ws_tls' ? 'Bunny 公网入口，例如 wakeup01.b-cdn.net' : '服务器 IP 或域名'} />
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
		  {showValidity && (
			<>
			  <Form.Item label="开始时间（中国时间）" name="validFrom" rules={[{ required: true, message: '请选择开始时间' }]}>
				<DatePicker showTime style={{ width: '100%' }} disabledDate={(date) => date.endOf('minute').isBefore(dayjs())} />
			  </Form.Item>
			  <Form.Item label="结束时间（留空即长期）" name="validUntil" dependencies={['validFrom']} rules={[({ getFieldValue }) => ({
				validator: async (_: unknown, value?: Dayjs) => !value || !getFieldValue('validFrom') || value.isAfter(getFieldValue('validFrom'))
				  ? Promise.resolve() : Promise.reject(new Error('结束时间必须晚于开始时间')),
			  })]}>
				<DatePicker showTime style={{ width: '100%' }} disabledDate={(date) => date.endOf('minute').isBefore(dayjs())} />
			  </Form.Item>
			</>
		  )}
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
              <Form.Item label="源站证书路径" name="nginxCertFile">
                <Input placeholder="/etc/nginx/ssl/origin.crt" />
              </Form.Item>
              <Form.Item label="源站私钥路径" name="nginxKeyFile">
                <Input placeholder="/etc/nginx/ssl/origin.key" />
              </Form.Item>
              <Form.Item label="上传源站证书">
                <Upload
                  accept=".crt,.cer,.pem"
                  beforeUpload={() => false}
                  fileList={certificateFiles}
                  maxCount={1}
                  onChange={({ fileList }) => setCertificateFiles(fileList.slice(-1))}
                >
                  <Button>选择证书文件</Button>
                </Upload>
              </Form.Item>
              <Form.Item label="上传源站私钥">
                <Upload
                  accept=".key,.pem"
                  beforeUpload={() => false}
                  fileList={privateKeyFiles}
                  maxCount={1}
                  onChange={({ fileList }) => setPrivateKeyFiles(fileList.slice(-1))}
                >
                  <Button>选择私钥文件</Button>
                </Upload>
              </Form.Item>
              <Form.Item label="写入 Nginx 并重载" name="nginxApply" valuePropName="checked">
                <Switch />
              </Form.Item>
            </div>
          </div>
        )}

        {type === 'bunny_ws_tls' && (
          <div className="line-subsection">
            <Typography.Title level={5}>Bunny / Nginx</Typography.Title>
            <div className="line-form-grid">
              <Form.Item label="WS 路径" name="wsPath" rules={[{ validator: validateOptionalWSPath }]}>
                <Input placeholder="自动生成" />
              </Form.Item>
              <Form.Item label="本地 Xray 端口" name="localXrayPort">
                <InputNumber min={1} max={65535} placeholder="自动寻找" />
              </Form.Item>
              <Form.Item label="Nginx 配置路径" name="nginxConfigPath">
                <Input placeholder="/etc/nginx/conf.d/x-ui-line-2.conf" />
              </Form.Item>
              <Form.Item label="Bunny 源站域名" name="originHost" rules={[{ validator: validateHost('Bunny 源站域名') }]}>
                <Input placeholder="01-bunny-02-inter-los.wakeup-ai.top" />
              </Form.Item>
              <Form.Item label="Bunny 源站端口" name="originPort" rules={[{ required: true, message: '请填写 Bunny 源站端口' }, { type: 'number', min: 1, max: 65535, message: '端口必须在 1 到 65535 之间' }]}>
                <InputNumber min={1} max={65535} placeholder="8443" />
              </Form.Item>
              <Form.Item label="证书通知邮箱" name="acmeEmail" rules={[{ required: true, message: '请填写证书通知邮箱' }, { type: 'email', message: '邮箱格式不正确' }]}>
                <Input placeholder="name@example.com" />
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

function DiagnosticsPage({
  lines,
  checking,
  onCheckAll,
  onOpenLine,
}: {
  lines: LineProfile[];
  checking: boolean;
  onCheckAll: () => void;
  onOpenLine: (id: number) => void;
}) {
  const [lineFilter, setLineFilter] = useState('all');
  const [kindFilter, setKindFilter] = useState('all');
  const [levelFilter, setLevelFilter] = useState('all');
  const [page, setPage] = useState(1);
  const [selected, setSelected] = useState<LineDiagnosticEvent | null>(null);
  const diagnosticsQuery = useQuery({
    queryKey: ['line-diagnostics', page, lineFilter, kindFilter, levelFilter],
    queryFn: () => fetchLineDiagnostics({
      page,
      pageSize: 20,
      lineId: lineFilter === 'all' ? undefined : Number(lineFilter),
      kind: kindFilter === 'all' ? undefined : kindFilter,
      level: levelFilter === 'all' ? undefined : levelFilter,
    }),
  });
  const abnormal = lines.filter((line) => line.status === 'apply_failed' || line.status === 'failed').length;
  const pending = lines.filter((line) => line.status === 'pending_apply' || line.status === 'pending_check' || line.status === 'applying').length;
  const normal = lines.filter((line) => line.status === 'active').length;
  const columns: ColumnsType<LineDiagnosticEvent> = [
    { title: '时间', width: 180, render: (_, row) => formatLineTime(row.createdAt) },
    { title: '线路', render: (_, row) => <button type="button" className="line-link-button" onClick={() => onOpenLine(row.lineId)}>{row.lineName}</button> },
    { title: '类型', width: 130, render: (_, row) => row.kind === 'check' ? '连通性检测' : row.action },
    { title: '结果', width: 90, render: (_, row) => <Tag color={row.level === 'error' ? 'red' : row.level === 'warning' ? 'gold' : 'blue'}>{row.level === 'error' ? '异常' : row.level === 'warning' ? '警告' : '正常'}</Tag> },
    { title: '摘要', dataIndex: 'message', render: (_, row) => <button type="button" className="diagnostic-summary-button" onClick={() => setSelected(row)}>{row.message}</button> },
  ];

  const resetPage = (change: (value: string) => void) => (value: string) => {
    setPage(1);
    change(value);
  };

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
          <Select value={lineFilter} onChange={resetPage(setLineFilter)} options={[{ value: 'all', label: '全部线路' }, ...lines.map((line) => ({ value: String(line.id), label: line.name || typeLabel(line.type) }))]} />
          <Select value={kindFilter} onChange={resetPage(setKindFilter)} options={[{ value: 'all', label: '全部事件' }, { value: 'check', label: '连通性检测' }, { value: 'operation', label: '部署与运维' }]} />
          <Select value={levelFilter} onChange={resetPage(setLevelFilter)} options={[{ value: 'all', label: '全部结果' }, { value: 'error', label: '异常' }, { value: 'warning', label: '警告' }, { value: 'info', label: '正常' }]} />
        </div>
        <Table
          rowKey="id"
          columns={columns}
          dataSource={diagnosticsQuery.data?.items ?? []}
          loading={diagnosticsQuery.isLoading}
          pagination={{ current: page, pageSize: 20, total: diagnosticsQuery.data?.total ?? 0, onChange: setPage, showSizeChanger: false }}
          locale={{ emptyText: <Empty description="暂无诊断记录" /> }}
        />
      </main>
      <Drawer title="诊断详情" open={Boolean(selected)} onClose={() => setSelected(null)} width={560}>
        {selected && (
          <Space direction="vertical" size="middle" style={{ width: '100%' }}>
            <div><Typography.Text type="secondary">线路</Typography.Text><br /><Typography.Text strong>{selected.lineName}</Typography.Text></div>
            <div><Typography.Text type="secondary">事件</Typography.Text><br /><Typography.Text>{selected.kind === 'check' ? '连通性检测' : selected.action}</Typography.Text></div>
            <div><Typography.Text type="secondary">结果</Typography.Text><br /><Tag color={selected.level === 'error' ? 'red' : selected.level === 'warning' ? 'gold' : 'blue'}>{selected.message}</Tag></div>
            {selected.kind === 'check' ? (
              <Table
                size="small"
                rowKey={(item) => `${item.name}-${item.message}`}
                pagination={false}
                dataSource={selected.items ?? []}
                columns={[
                  { title: '检测项目', dataIndex: 'name', width: 130 },
                  { title: '结果', width: 76, render: (_, item) => <Tag color={item.status === 'fail' ? 'red' : item.status === 'warn' ? 'gold' : 'green'}>{item.status === 'fail' ? '失败' : item.status === 'warn' ? '警告' : '通过'}</Tag> },
                  { title: '详细结果', dataIndex: 'message' },
                ]}
              />
            ) : (
              <div><Typography.Text type="secondary">执行详情</Typography.Text><br /><Typography.Text>{selected.detail || selected.message}</Typography.Text></div>
            )}
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
  const [shareLineId, setShareLineId] = useState<number | null>(null);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [validityLine, setValidityLine] = useState<LineProfile | null>(null);
  const [selectedLineIds, setSelectedLineIds] = useState<Key[]>([]);
  const [columnWidths, setColumnWidths] = useState<Record<LineColumnKey, number>>(() => {
    try {
      const stored = localStorage.getItem(lineColumnWidthsStorageKey);
      if (!stored) return lineColumnDefaults;
      const parsed = JSON.parse(stored) as Partial<Record<LineColumnKey, number>>;
      return {
        status: Math.max(lineColumnMinimums.status, parsed.status ?? lineColumnDefaults.status),
        name: Math.max(lineColumnMinimums.name, parsed.name ?? lineColumnDefaults.name),
        validity: Math.max(lineColumnMinimums.validity, parsed.validity ?? lineColumnDefaults.validity),
        speed: Math.max(lineColumnMinimums.speed, parsed.speed ?? lineColumnDefaults.speed),
        outboundLatency: Math.max(lineColumnMinimums.outboundLatency, parsed.outboundLatency ?? lineColumnDefaults.outboundLatency),
        traffic: Math.max(lineColumnMinimums.traffic, parsed.traffic ?? lineColumnDefaults.traffic),
        actions: Math.max(lineColumnMinimums.actions, parsed.actions ?? lineColumnDefaults.actions),
      };
    } catch {
      return lineColumnDefaults;
    }
  });
  const isDeployPicker = location.pathname === '/lines/deploy';
	const isDeployForm = location.pathname === '/lines/deploy/cloudflare' || location.pathname === '/lines/deploy/bunny' || location.pathname === '/lines/deploy/reality';
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
  const visibleLines = useMemo(() => lines.filter((line) => line.type !== 'bunny_ws_tls'), [lines]);
  const { data: lineMetrics = [] } = useQuery({
    queryKey: keys.lines.metrics(),
    queryFn: fetchLineMetrics,
	refetchInterval: 30000,
  });
  const { data: lineDetail, isLoading: isLineLoading } = useQuery({
    queryKey: keys.lines.detail(lineId),
    queryFn: () => fetchLine(lineId),
    enabled: lineId > 0,
  });
  const { data: lineDetailMetrics } = useQuery({
    queryKey: keys.lines.metric(lineId),
    queryFn: () => fetchSingleLineMetrics(lineId),
    enabled: lineId > 0,
	refetchInterval: 30000,
  });
  const [liveLineTraffic, setLiveLineTraffic] = useState<Map<number, LineTrafficSnapshot>>(() => new Map());
  useWebSocket({
    line_metrics: (payload) => {
      if (!Array.isArray(payload)) return;
      const next = new Map<number, LineTrafficSnapshot>();
      for (const value of payload) {
        if (!value || typeof value !== 'object') continue;
        const snapshot = value as Partial<LineTrafficSnapshot>;
        const snapshotLineId = snapshot.lineId;
        if (typeof snapshotLineId !== 'number' || !Number.isInteger(snapshotLineId) || snapshotLineId <= 0) continue;
        next.set(snapshotLineId, {
          lineId: snapshotLineId,
          inboundSpeedUp: Number(snapshot.inboundSpeedUp) || 0,
          inboundSpeedDown: Number(snapshot.inboundSpeedDown) || 0,
          outboundSpeedUp: Number(snapshot.outboundSpeedUp) || 0,
          outboundSpeedDown: Number(snapshot.outboundSpeedDown) || 0,
          collectedAt: Number(snapshot.collectedAt) || 0,
        });
      }
      setLiveLineTraffic(next);
    },
  });
  const metricsByLineId = useMemo(
    () => {
      const metrics = new Map(lineMetrics.map((item) => [item.lineId, item]));
      for (const snapshot of liveLineTraffic.values()) {
        const current = metrics.get(snapshot.lineId);
        if (!current) continue;
        metrics.set(snapshot.lineId, { ...current, ...snapshot });
      }
      return metrics;
    },
    [lineMetrics, liveLineTraffic],
  );
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
    mutationFn: async ({ values, certificateFile, privateKeyFile }: LineFormSubmission): Promise<LineApplyResult> => {
      const saved = await createLine(buildPayload(currentType, values, true));
      if (certificateFile && privateKeyFile) {
        await uploadOriginCertificate(saved.id, certificateFile, privateKeyFile);
      }
      if (saved.status === 'scheduled') return { line: saved, scheduled: true };
      return applyLine(saved.id);
    },
    onSuccess: async ({ line, errorMessage, scheduled }) => {
      await queryClient.invalidateQueries({ queryKey: keys.lines.root() });
      queryClient.setQueryData(keys.lines.detail(line.id), line);
      if (errorMessage) {
        message.error(errorMessage);
        navigate(`/lines/${line.id}`);
        return;
      }
      if (scheduled) {
        message.success('线路已保存，将在开始时间自动启用');
        navigate('/lines');
        return;
      }
      message.success('线路已保存并应用，等待检测');
      navigate('/lines');
    },
    onError: (error) => message.error(error instanceof Error ? error.message : '线路保存失败'),
  });

  const updateMutation = useMutation({
    mutationFn: async ({ values, certificateFile, privateKeyFile }: LineFormSubmission): Promise<LineApplyResult> => {
      const saved = await updateLine(lineId, buildPayload(detailType, values));
      if (certificateFile && privateKeyFile) {
        await uploadOriginCertificate(saved.id, certificateFile, privateKeyFile);
      }
      if (saved.status === 'scheduled') return { line: saved, scheduled: true };
      return applyLine(saved.id);
    },
    onSuccess: async ({ line, errorMessage, scheduled }) => {
      await queryClient.invalidateQueries({ queryKey: keys.lines.root() });
      queryClient.setQueryData(keys.lines.detail(line.id), line);
      if (errorMessage) {
        message.error(errorMessage);
        return;
      }
      if (scheduled) {
        message.success('待启用线路已更新，开始时间到达后自动部署');
        navigate(`/lines/${line.id}`);
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
      await queryClient.invalidateQueries({ queryKey: ['line-diagnostics'] });
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

  const validityMutation = useMutation({
    mutationFn: async ({ line, validUntil }: { line: LineProfile; validUntil: number }) => (
      lineRequiresManualRenewal(line) ? renewLine(line.id, validUntil) : updateLineValidity(line.id, validUntil)
    ),
    onSuccess: async (line, variables) => {
      await queryClient.invalidateQueries({ queryKey: keys.lines.root() });
      queryClient.setQueryData(keys.lines.detail(line.id), line);
      setValidityLine(null);
      message.success(lineRequiresManualRenewal(variables.line) ? '线路已续期并重新启用' : variables.validUntil === 0 ? '线路已设为长期有效，现有连接未中断' : '有效期已延长，现有连接未中断');
    },
    onError: (error) => message.error(error instanceof Error ? error.message : '有效期更新失败'),
  });

  const allChecksMutation = useMutation({
    mutationFn: () => Promise.all(lines.map((line) => checkLine(line.id))),
    onSuccess: async (results) => {
      await queryClient.invalidateQueries({ queryKey: keys.lines.root() });
      await queryClient.invalidateQueries({ queryKey: ['line-diagnostics'] });
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

  const resetClashSubscriptionMutation = useMutation({
    mutationFn: resetLineClashSubscription,
    onSuccess: (result) => {
      setShareData((current) => current ? {
        ...current,
        clashSubscription: result,
        clashSubscriptionError: undefined,
      } : current);
      message.success('旧订阅地址已失效，新的订阅地址已生成');
    },
    onError: (error) => message.error(error instanceof Error ? error.message : '重置订阅地址失败'),
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
      const width = Math.min(1600, Math.max(lineColumnMinimums[key], startWidth + moveEvent.clientX - startX));
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
    setShareLineId(id);
    shareMutation.mutate(id);
  };

  const openValidity = (line: LineProfile) => {
    setValidityLine(line);
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
      title: columnTitle('有效期', 'validity'),
      width: columnWidths.validity,
      render: (_, row) => <span className="line-validity-cell">{formatLineValidity(row)}</span>,
    },
    {
      title: columnTitle('实时吞吐', 'speed'),
      width: columnWidths.speed,
      render: (_, row) => {
        const metrics = metricsByLineId.get(row.id);
        return <LineSpeedCell up={metrics?.inboundSpeedUp} down={metrics?.inboundSpeedDown} />;
      },
    },
    {
      title: columnTitle('出站延迟', 'outboundLatency'),
      width: columnWidths.outboundLatency,
      render: (_, row) => formatLatency(metricsByLineId.get(row.id)?.outboundLatencyMs ?? row.lastOutboundLatencyMs),
    },
    {
      title: columnTitle('累计流量', 'traffic'),
      width: columnWidths.traffic,
      render: (_, row) => SizeFormatter.sizeFormat(metricsByLineId.get(row.id)?.totalTraffic),
    },
    {
      title: columnTitle('操作', 'actions'),
      width: columnWidths.actions,
      render: (_, row) => (
        <Space wrap size={[4, 4]}>
          {!lineRequiresManualRenewal(row) && <Button size="small" icon={<CheckCircleOutlined />} loading={checkMutation.isPending} onClick={() => runCheck(row.id)}>检测</Button>}
          {!lineRequiresManualRenewal(row) && <Button size="small" icon={<QrcodeOutlined />} loading={shareMutation.isPending} onClick={() => openShare(row.id)}>分享</Button>}
          <Button size="small" danger={lineRequiresManualRenewal(row)} loading={validityMutation.isPending} onClick={() => openValidity(row)}>
            {lineRequiresManualRenewal(row) ? '续期启用' : '延长有效期'}
          </Button>
          <Button size="small" onClick={() => navigate(`/lines/${row.id}`)}>
            详情
          </Button>
          <Dropdown
            trigger={['click']}
            menu={{
              items: [
                { key: 'details', label: '查看详情', onClick: () => navigate(`/lines/${row.id}`) },
                ...(!lineRequiresManualRenewal(row) ? [{ key: 'edit', label: '编辑', onClick: () => navigate(`/lines/${row.id}/edit`) }] : []),
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
                showValidity={false}
                onSubmit={(submission) => updateMutation.mutate(submission)}
              />
            )}
          </main>
          <LineShareModal
            open={shareOpen}
            data={shareData}
            lineId={shareLineId}
            resetting={resetClashSubscriptionMutation.isPending}
            onClose={() => setShareOpen(false)}
            onResetSubscription={() => shareLineId ? resetClashSubscriptionMutation.mutateAsync(shareLineId) : Promise.reject(new Error('未选择线路'))}
          />
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
              {lineDetail && <Button danger={lineRequiresManualRenewal(lineDetail)} loading={validityMutation.isPending} onClick={() => openValidity(lineDetail)}>{lineRequiresManualRenewal(lineDetail) ? '续期并重新启用' : '延长有效期'}</Button>}
              {lineDetail && !lineRequiresManualRenewal(lineDetail) && <Button icon={<EditOutlined />} onClick={() => navigate(`/lines/${lineId}/edit`)}>编辑</Button>}
              {lineDetail && !lineRequiresManualRenewal(lineDetail) && <Button icon={<CheckCircleOutlined />} loading={checkMutation.isPending} onClick={() => runCheck(lineId)}>检测</Button>}
              {lineDetail && !lineRequiresManualRenewal(lineDetail) && <Button icon={<QrcodeOutlined />} loading={shareMutation.isPending} onClick={() => openShare(lineId)}>分享</Button>}
              {lineDetail && !lineRequiresManualRenewal(lineDetail) && <Button type="primary" icon={<SaveOutlined />} loading={applyMutation.isPending} onClick={() => applyMutation.mutate(lineId)}>重新应用</Button>}
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
              <LineRuntimeOverview line={lineDetail} metrics={lineDetailMetrics} />
              <LineChainDiagram line={lineDetail} />
            </>
          )}
        </main>
        <Drawer title="高级部署信息" open={advancedOpen} onClose={() => setAdvancedOpen(false)} width={720}>
          {lineDetail && <LinePlanPanel line={lineDetail} />}
        </Drawer>
		<LineShareModal
			open={shareOpen}
			data={shareData}
			lineId={shareLineId}
			resetting={resetClashSubscriptionMutation.isPending}
			onClose={() => setShareOpen(false)}
			onResetSubscription={() => shareLineId ? resetClashSubscriptionMutation.mutateAsync(shareLineId) : Promise.reject(new Error('未选择线路'))}
		/>
		<LineValidityModal line={validityLine} open={Boolean(validityLine)} saving={validityMutation.isPending} onClose={() => setValidityLine(null)} onSubmit={(validUntil) => validityLine && validityMutation.mutate({ line: validityLine, validUntil })} />
      </LinePageShell>
    );
  }

	if (isDeployPicker) {
		return <LinePageShell><LineTypePicker onSelect={(type) => navigate(type === 'reality_direct' ? '/lines/deploy/reality' : type === 'bunny_ws_tls' ? '/lines/deploy/bunny' : '/lines/deploy/cloudflare')} /></LinePageShell>;
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
          showValidity
          onSubmit={(submission) => createMutation.mutate(submission)}
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
          dataSource={visibleLines}
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
		<LineShareModal
			open={shareOpen}
			data={shareData}
			lineId={shareLineId}
			resetting={resetClashSubscriptionMutation.isPending}
			onClose={() => setShareOpen(false)}
			onResetSubscription={() => shareLineId ? resetClashSubscriptionMutation.mutateAsync(shareLineId) : Promise.reject(new Error('未选择线路'))}
		/>
	  <LineValidityModal line={validityLine} open={Boolean(validityLine)} saving={validityMutation.isPending} onClose={() => setValidityLine(null)} onSubmit={(validUntil) => validityLine && validityMutation.mutate({ line: validityLine, validUntil })} />
    </LinePageShell>
  );
}


