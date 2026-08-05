import { useCallback, useEffect, useState } from 'react';
import { Button, Descriptions, Divider, Empty, Form, Input, Modal, Select, Space, Spin, Switch, Upload, message } from 'antd';
import type { UploadFile } from 'antd';

import { ClipboardManager, HttpUtil, IntlUtil } from '@/utils';
import '../central-access/CentralAccessPage.css';

interface ApiMsg<T = unknown> { success?: boolean; msg?: string; obj?: T; }
interface CentralManagementStatus { enabled: boolean; domain: string; port: number; basePath: string; certificateSha256: string; certificateExpiresAt: number; panelBoundToLoopback: boolean; appliedAt: number; lastError: string; }
interface CentralAccessTokenRow { id: number; name: string; enabled: boolean; createdAt: number; lastUsedAt: number; lastUsedIp: string; }

const cloudflarePorts = [443, 2053, 2083, 2087, 2096, 8443];
const UNIX_MILLISECONDS_THRESHOLD = 100_000_000_000;

function displayTime(value: number): string {
  return value ? IntlUtil.formatDate(value < UNIX_MILLISECONDS_THRESHOLD ? value * 1000 : value) : '-';
}

export default function CentralManagementTab() {
  const [form] = Form.useForm<{ domain: string; port: number }>();
  const [messageApi, messageContextHolder] = message.useMessage();
  const [status, setStatus] = useState<CentralManagementStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [applying, setApplying] = useState(false);
  const [certificateFiles, setCertificateFiles] = useState<UploadFile[]>([]);
  const [privateKeyFiles, setPrivateKeyFiles] = useState<UploadFile[]>([]);
  const [tokens, setTokens] = useState<CentralAccessTokenRow[]>([]);
  const [tokensLoading, setTokensLoading] = useState(true);
  const [tokenModalOpen, setTokenModalOpen] = useState(false);
  const [tokenName, setTokenName] = useState('rp-console');
  const [creatingToken, setCreatingToken] = useState(false);
  const [createdToken, setCreatedToken] = useState<string | null>(null);

  const loadStatus = useCallback(async () => {
    setLoading(true);
    try {
      const response = await HttpUtil.get('/panel/api/setting/centralManagement') as ApiMsg<CentralManagementStatus>;
      if (!response.success || !response.obj) throw new Error(response.msg || '无法读取总站接入状态');
      setStatus(response.obj);
      form.setFieldsValue({ domain: response.obj.domain, port: response.obj.port || 2083 });
    } catch (error) {
      messageApi.error(error instanceof Error ? error.message : '无法读取总站接入状态');
    } finally { setLoading(false); }
  }, [form, messageApi]);

  const loadTokens = useCallback(async () => {
    setTokensLoading(true);
    try {
      const response = await HttpUtil.get('/panel/api/setting/centralAccessTokens') as ApiMsg<CentralAccessTokenRow[]>;
      if (response.success) setTokens(Array.isArray(response.obj) ? response.obj : []);
    } finally { setTokensLoading(false); }
  }, []);

  useEffect(() => { void loadStatus(); }, [loadStatus]);
  useEffect(() => { void loadTokens(); }, [loadTokens]);

  async function apply() {
    const values = await form.validateFields();
    const certificate = certificateFiles[0]?.originFileObj;
    const privateKey = privateKeyFiles[0]?.originFileObj;
    if (!certificate || !privateKey) { messageApi.error('请同时选择源站证书和私钥文件'); return; }
    const data = new FormData();
    data.append('domain', values.domain.trim());
    data.append('port', String(values.port));
    data.append('certificate', certificate);
    data.append('privateKey', privateKey);
    setApplying(true);
    try {
      const response = await HttpUtil.post<CentralManagementStatus>('/panel/api/setting/centralManagement/apply', data, { silent: true }) as ApiMsg<CentralManagementStatus>;
      if (!response.success || !response.obj) throw new Error(response.msg || '总站接入应用失败');
      setStatus(response.obj); setCertificateFiles([]); setPrivateKeyFiles([]); messageApi.success('总站接入已应用');
    } catch (error) {
      messageApi.error(error instanceof Error ? error.message : '总站接入应用失败');
    } finally { setApplying(false); }
  }

  function disable() {
    Modal.confirm({ title: '停用总站接入', content: '将移除本站的独立 HTTPS 管理入口。现有线路不会受到影响。', okText: '停用', okType: 'danger', cancelText: '取消', onOk: async () => {
      const response = await HttpUtil.post<CentralManagementStatus>('/panel/api/setting/centralManagement/disable', {}, { silent: true }) as ApiMsg<CentralManagementStatus>;
      if (!response.success || !response.obj) throw new Error(response.msg || '停用失败');
      setStatus(response.obj); messageApi.success('总站接入已停用');
    }});
  }

  async function createToken() {
    const name = tokenName.trim();
    if (!name) { messageApi.error('令牌名称不能为空'); return; }
    setCreatingToken(true);
    try {
      const response = await HttpUtil.post<{ token?: string }>('/panel/api/setting/centralAccessTokens/create', { name }, { silent: true }) as ApiMsg<{ token?: string }>;
      if (!response.success || !response.obj?.token) throw new Error(response.msg || '生成令牌失败');
      setTokenModalOpen(false); setCreatedToken(response.obj.token); await loadTokens();
    } catch (error) { messageApi.error(error instanceof Error ? error.message : '生成令牌失败');
    } finally { setCreatingToken(false); }
  }

  async function toggleToken(row: CentralAccessTokenRow) {
    const response = await HttpUtil.post(`/panel/api/setting/centralAccessTokens/setEnabled/${row.id}`, { enabled: !row.enabled }, { silent: true }) as ApiMsg;
    if (!response.success) { messageApi.error(response.msg || 'Token update failed'); return; }
    setTokens((current) => current.map((item) => item.id === row.id ? { ...item, enabled: !row.enabled } : item));
  }

  function deleteToken(row: CentralAccessTokenRow) {
    Modal.confirm({ title: `Delete token "${row.name}"`, content: 'RP Console using this token will immediately lose access to this node.', okText: 'Delete', okType: 'danger', cancelText: 'Cancel', onOk: async () => {
      const response = await HttpUtil.post(`/panel/api/setting/centralAccessTokens/delete/${row.id}`, {}, { silent: true }) as ApiMsg;
      if (!response.success) throw new Error(response.msg || 'Token delete failed');
      await loadTokens();
    }});
  }

  async function copyCreatedToken() {
    if (!createdToken) return;
    if (await ClipboardManager.copyText(createdToken)) messageApi.success('Copied'); else messageApi.error('Copy failed');
  }

  return <>
    {messageContextHolder}
    <Spin spinning={loading}>
      <Form form={form} layout="vertical" requiredMark="optional" style={{ maxWidth: 760 }}>
        <Form.Item label="管理域名" name="domain" rules={[{ required: true, message: '请输入 Cloudflare 已代理的管理子域名' }]}><Input placeholder="rp2.wakeup-ai.top" autoComplete="off" /></Form.Item>
        <Form.Item label="HTTPS 端口" name="port" rules={[{ required: true, message: '请选择端口' }]}><Select options={cloudflarePorts.map((port) => ({ value: port, label: String(port) }))} /></Form.Item>
        <Form.Item label="随机管理路径"><Input readOnly value={status?.basePath || ''} /></Form.Item>
        <Form.Item label="Cloudflare 源站证书"><Upload accept=".crt,.cer,.pem" beforeUpload={() => false} maxCount={1} fileList={certificateFiles} onChange={({ fileList }) => setCertificateFiles(fileList.slice(-1))}><Button>选择证书文件</Button></Upload></Form.Item>
        <Form.Item label="Cloudflare 源站私钥"><Upload accept=".key,.pem" beforeUpload={() => false} maxCount={1} fileList={privateKeyFiles} onChange={({ fileList }) => setPrivateKeyFiles(fileList.slice(-1))}><Button>选择私钥文件</Button></Upload></Form.Item>
        <Space wrap><Button type="primary" loading={applying} onClick={() => { void apply(); }}>保存并应用</Button>{status?.enabled && <Button danger onClick={disable}>停用入口</Button>}</Space>
      </Form>
      <Divider />
      <Descriptions size="small" column={1} bordered>
        <Descriptions.Item label="运行状态">{status?.enabled ? '已启用' : '未启用'}</Descriptions.Item>
		<Descriptions.Item label="面板监听">{status?.panelBoundToLoopback ? '仅本机回环' : '公网监听'}</Descriptions.Item>
        <Descriptions.Item label="生效时间">{displayTime(status?.appliedAt || 0)}</Descriptions.Item>
        <Descriptions.Item label="证书到期">{displayTime(status?.certificateExpiresAt || 0)}</Descriptions.Item>
        <Descriptions.Item label="证书 SHA-256">{status?.certificateSha256 || '-'}</Descriptions.Item>
        {status?.lastError && <Descriptions.Item label="最近错误">{status.lastError}</Descriptions.Item>}
      </Descriptions>
      <Divider />
      <div className="api-token-header"><p className="api-token-hint">RP Console 只读令牌</p><Button type="primary" size="small" onClick={() => { setTokenName('rp-console'); setTokenModalOpen(true); }}>生成令牌</Button></div>
      <Spin spinning={tokensLoading}>
        {!tokens.length && !tokensLoading && <Empty description="尚未生成 RP Console 令牌" />}
        {tokens.map((row) => <div key={row.id} className={`api-token-row${row.enabled ? '' : ' disabled'}`}><div className="api-token-row-head"><div className="api-token-name-wrap"><span className="api-token-name">{row.name}</span><span className="api-token-created">Created {displayTime(row.createdAt)}{row.lastUsedAt ? ` · Last used ${displayTime(row.lastUsedAt)}` : ' · Unused'}{row.lastUsedIp ? ` · ${row.lastUsedIp}` : ''}</span></div><Space><Switch size="small" checked={row.enabled} onChange={() => { void toggleToken(row); }} /><Button size="small" danger type="text" onClick={() => deleteToken(row)}>Delete</Button></Space></div></div>)}
      </Spin>
    </Spin>
    <Modal open={tokenModalOpen} title="Generate RP Console read-only token" okText="Generate" cancelText="Cancel" confirmLoading={creatingToken} onOk={() => { void createToken(); }} onCancel={() => setTokenModalOpen(false)}><Form layout="vertical"><Form.Item label="Token name" required><Input value={tokenName} maxLength={64} onChange={(event) => setTokenName(event.target.value)} onPressEnter={() => { void createToken(); }} /></Form.Item></Form></Modal>
    <Modal open={Boolean(createdToken)} title="RP Console token created" okText="Done" cancelButtonProps={{ style: { display: 'none' } }} onOk={() => setCreatedToken(null)} onCancel={() => setCreatedToken(null)}><p>Copy this token now. It cannot be displayed again.</p><Input.TextArea readOnly value={createdToken || ''} autoSize={{ minRows: 2 }} /><Button type="primary" style={{ marginTop: 12 }} onClick={() => { void copyCreatedToken(); }}>Copy token</Button></Modal>
  </>;
}
