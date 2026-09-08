import { ApiOutlined, KeyOutlined, PlusOutlined, ReloadOutlined, SearchOutlined, DeleteOutlined } from '@ant-design/icons';
import { PageContainer } from '@ant-design/pro-components';
import { Link } from '@umijs/max';
import { Alert, App, Button, Card, Form, Input, Modal, Select, Space, Switch, Table, Tabs, Tag, Typography } from 'antd';
import { useCallback, useEffect, useState } from 'react';
import { api, listApps, type Application } from '@/services/gateway';
import { mcpApi, type Catalog, type McpServer, type McpTool, type McpKey, type Grant } from '@/services/mcp';
type Kind = 'server' | 'tool' | 'key';
type Operation = { operationId: string; summary?: string; description?: string; tags?: string[]; 'x-rpc-method'?: string };
type OpenAPI = { paths: Record<string, Record<string, Operation>> };
export default function McpPage() {
 const { message, modal } = App.useApp();
 const [data, setData] = useState<Catalog>(); const [apps, setApps] = useState<Application[]>([]);
 const [loading, setLoading] = useState(false); const [error, setError] = useState(''); const [query, setQuery] = useState(''); const [tab, setTab] = useState('servers');
 const [kind, setKind] = useState<Kind>(); const [editing, setEditing] = useState<McpServer | McpTool | McpKey>();
 const [busy, setBusy] = useState(false); const [secret, setSecret] = useState(''); const [schema, setSchema] = useState<string>();
 const [authForm] = Form.useForm(); const [authServer, setAuthServer] = useState<McpServer>();
 const [form] = Form.useForm(); const selectedApp = Form.useWatch('app', form); const [operations, setOperations] = useState<{ value: string; label: string; op: Operation }[]>([]); const [opsLoading, setOpsLoading] = useState(false); const [opsError, setOpsError] = useState('');
 const load = useCallback(async () => { setLoading(true); setError(''); try { const [catalog, appList] = await Promise.all([mcpApi<Catalog>(), listApps()]); setData(catalog); setApps(appList); } catch (e) { setError((e as Error).message); } finally { setLoading(false); } }, []);
 useEffect(() => { void load(); }, [load]);
 useEffect(() => { if (kind !== 'tool' || !selectedApp) { setOperations([]); return; } let active = true; setOpsLoading(true); setOpsError(''); setOperations([]);
  api<OpenAPI>(`/${encodeURIComponent(selectedApp)}/openapi.json`).then(doc => { if (active) setOperations(Object.entries(doc.paths).flatMap(([path, methods]) => Object.entries(methods).map(([method, op]) => ({ value: op.operationId, label: `${method.toUpperCase()} ${path} · ${op.summary || op['x-rpc-method']}`, op })))); }).catch(e => { if (active) setOpsError(e.message); }).finally(() => { if (active) setOpsLoading(false); }); return () => { active = false; };
 }, [kind, selectedApp]);
 const open = (next: Kind, row?: McpServer | McpTool | McpKey) => { setKind(next); setEditing(row); form.resetFields(); const values: Record<string, unknown> = { enabled: true, header: 'X-MCP-Key', bearer: false, grants: [], ...row };
  if (next === 'server') values.endpointText = row ? (row as McpServer).endpoints.join('\n') : '';
  if (next === 'tool' && row) values.app = data?.servers.find(s => s.id === (row as McpTool).serverId)?.app;
  form.setFieldsValue(values);
 };
 const submit = async () => { if (!data || !kind) return; let values; try { values = await form.validateFields(); } catch { return; } setBusy(true);
  const payload = kind === 'server' ? { name: values.name, app: values.app, endpoints: values.endpointText.split('\n').map((v: string) => v.trim()).filter(Boolean), header: values.header, bearer: !!values.bearer, enabled: !!values.enabled } : kind === 'tool' ? { name: values.name, description: values.description || '', serverId: values.serverId, operationId: values.operationId } : { name: values.name, enabled: !!values.enabled, grants: (values.grants || []).map((g: Grant) => ({ serverId: g.serverId, toolIds: g.toolIds || [] })) };
  try { const result = await mcpApi<{ secret?: string }>('', { version: data.version, kind, id: editing?.id || '', [kind]: payload }); setKind(undefined); message.success('已保存'); if (result.secret) setSecret(result.secret); await load(); } catch (e) { message.error((e as Error).message); } finally { setBusy(false); }
 };
 const remove = (type: Kind, row: McpServer | McpTool | McpKey) => modal.confirm({ title: `删除 ${row.name}？`, content: type === 'server' ? '将同时移除该服务的工具和 Key 授权。原应用 HTTP 接口不受影响。' : '对应 MCP 调用权限将被移除。', okText: '删除', cancelText: '取消', okButtonProps: { danger: true }, onOk: async () => { try { await mcpApi('', { version: data?.version, kind: type, id: row.id, delete: true }); await load(); } catch (e) { message.error((e as Error).message); throw e; } } });
 const editAccess = (server: McpServer) => { setAuthServer(server); authForm.setFieldsValue({ access: data?.keys.flatMap(k => k.grants.filter(g => g.serverId === server.id).map(g => ({ keyId: k.id, toolIds: g.toolIds }))) || [] }); };
 const saveAccess = async () => { let values; try { values = await authForm.validateFields(); } catch { return; } setBusy(true); try { await mcpApi('', { version: data?.version, kind: 'access', id: authServer?.id, access: (values.access || []).map((g: { keyId: string; toolIds?: string[] }) => ({ keyId: g.keyId, toolIds: g.toolIds || [] })) }); setAuthServer(undefined); message.success('授权已更新'); await load(); } catch (e) { message.error((e as Error).message); } finally { setBusy(false); } };
 const rotate = (row: McpKey) => modal.confirm({ title: `轮换 ${row.name} 的 Key？`, content: '原 Key 将失效，需要向调用方提供新的 Key。', okText: '轮换', cancelText: '取消', onOk: async () => { try { const result = await mcpApi<{ secret: string }>('', { version: data?.version, kind: 'key', id: row.id, rotate: true, key: row }); setSecret(result.secret); await load(); } catch (e) { message.error((e as Error).message); throw e; } } });
 const inspect = async (id: string) => { try { const value = await mcpApi<unknown>(`/servers/${encodeURIComponent(id)}/tools`); setSchema(JSON.stringify(value, null, 2)); } catch (e) { message.error((e as Error).message); } };
 const match = (value: string) => value.toLowerCase().includes(query.toLowerCase());
 const serverName = (id: string) => data?.servers.find(s => s.id === id)?.name || id;
 const status = (id: string) => { const s = data?.statuses.find(v => v.serverId === id); const names: Record<string, string> = { ready: '就绪', disabled: '已停用', unavailable: '不可用', listener_off: '监听未开启' }; return <Space direction="vertical" size={2}><Tag color={s?.status === 'ready' ? 'green' : s?.status === 'unavailable' ? 'red' : 'default'}>{names[s?.status || ''] || '加载中'}</Tag>{s?.error && <Typography.Text type="danger" style={{ fontSize: 12 }}>{s.error}</Typography.Text>}</Space>; };
 return <PageContainer title="MCP 服务" subTitle="管理服务入口、工具和调用方授权" breadcrumbRender={false} extra={<Space><Link to="/apps"><Button>应用</Button></Link><Link to="/docs"><Button>接口文档</Button></Link></Space>}>
  <Alert type="info" title="MCP 入口仅接受所属应用的域名，需携带授权 Key，无需 X-App-Code。代理须保留 Host。" showIcon style={{ marginBottom: 16 }} />
  {error && <Alert type="error" title="加载失败" description={error} showIcon style={{ marginBottom: 16 }} />}
  {data?.listenerError && <Alert type="error" title={data.listenerError} showIcon style={{ marginBottom: 16 }} />}
  {data && !data.listenerEnabled && <Alert type="info" title="MCP 监听尚未开启" description="在启动配置中设置 mcp.enabled: true 后重启网关，默认监听 8081。" showIcon style={{ marginBottom: 16 }} />}
  <Card className="table-card" styles={{ body: { padding: 0 } }}>
   <div className="table-toolbar"><Space><ApiOutlined /><Typography.Text strong>MCP 管理</Typography.Text></Space><Space wrap>
    <Input prefix={<SearchOutlined />} placeholder="搜索名称、应用或入口" value={query} onChange={e => setQuery(e.target.value)} allowClear className="search-input" />
    <Button icon={<ReloadOutlined />} loading={loading} onClick={load}>刷新</Button><Button onClick={() => open('server')}>新增服务</Button><Button icon={<KeyOutlined />} onClick={() => { setTab('keys'); open('key'); }}>新增 Key</Button><Button type="primary" icon={<PlusOutlined />} onClick={() => open('tool')}>新增工具</Button>
   </Space></div>
   <Tabs activeKey={tab} onChange={setTab} tabBarStyle={{ padding: '0 24px' }} items={[
    { key: 'servers', label: `MCP 服务 (${data?.servers.length || 0})`, children: <Table<McpServer> rowKey="id" loading={loading} dataSource={data?.servers.filter(s => match(`${s.name} ${s.app} ${s.endpoints.join(' ')}`))} scroll={{ x: 1000 }} columns={[
     { title: '服务名称', dataIndex: 'name', render: (v, row) => <Button type="link" onClick={() => open('server', row)}>{v}</Button> }, { title: '应用', dataIndex: 'app' },
     { title: '应用域名', render: (_, row) => apps.find(a => a.name === row.app)?.domain || '待配置' },
     { title: '访问路径', dataIndex: 'endpoints', render: (v: string[]) => <Space direction="vertical">{v.map(url => <Typography.Text key={url} copyable className="path-cell">{url}</Typography.Text>)}</Space> },
     { title: '认证请求头', render: (_, s) => <span className="path-cell">{s.header}{s.bearer ? ' · Bearer' : ''}</span> }, { title: '工具数', render: (_, s) => data?.tools.filter(t => t.serverId === s.id).length }, { title: '状态', render: (_, s) => status(s.id) },
     { title: '操作', width: 220, render: (_, s) => <Space><Button type="link" size="small" onClick={() => inspect(s.id)}>工具定义</Button><Button type="link" size="small" onClick={() => editAccess(s)}>Key 授权</Button><Button type="text" danger icon={<DeleteOutlined />} aria-label={`删除服务 ${s.name}`} onClick={() => remove('server', s)} /></Space> },
    ]} locale={{ emptyText: '创建 MCP 服务后，为它添加工具和 Key 授权' }} /> },
    { key: 'tools', label: `工具 (${data?.tools.length || 0})`, children: <Table<McpTool> rowKey="id" loading={loading} dataSource={data?.tools.filter(t => match(`${t.name} ${t.description} ${serverName(t.serverId)}`))} columns={[
     { title: '工具名称', dataIndex: 'name', render: v => <Typography.Text code>{v}</Typography.Text> }, { title: '说明', dataIndex: 'description' }, { title: 'MCP 服务', dataIndex: 'serverId', render: serverName }, { title: '操作', width: 150, render: (_, t) => <Space><Button type="link" onClick={() => open('tool', t)}>编辑</Button><Button type="text" danger icon={<DeleteOutlined />} aria-label={`删除工具 ${t.name}`} onClick={() => remove('tool', t)} /></Space> },
    ]} locale={{ emptyText: '点击右上角「新增工具」，从已发布的 OpenAPI 接口创建' }} /> },
    { key: 'keys', label: `全局 Key (${data?.keys.length || 0})`, children: <Table<McpKey> rowKey="id" loading={loading} dataSource={data?.keys.filter(k => match(k.name))} columns={[
     { title: '调用方名称', dataIndex: 'name' }, { title: '状态', dataIndex: 'enabled', render: v => <Tag color={v ? 'green' : 'default'}>{v ? '启用' : '停用'}</Tag> },
     { title: '授权范围', render: (_, k) => <Space direction="vertical">{k.grants.map(g => <span key={g.serverId}>{serverName(g.serverId)} · {g.toolIds.length} 个工具</span>)}{!k.grants.length && <Typography.Text type="secondary">未授权</Typography.Text>}</Space> },
     { title: '操作', width: 200, render: (_, k) => <Space><Button type="link" onClick={() => open('key', k)}>编辑授权</Button><Button type="link" onClick={() => rotate(k)}>轮换</Button><Button type="text" danger icon={<DeleteOutlined />} aria-label={`删除 Key ${k.name}`} onClick={() => remove('key', k)} /></Space> },
    ]} locale={{ emptyText: 'Key 全局管理，按 MCP 服务和工具分别授权' }} /> },
   ]} />
  </Card>
  <Modal title={`${editing ? '编辑' : '新增'}${kind === 'server' ? ' MCP 服务' : kind === 'tool' ? '工具' : '调用方 Key'}`} open={!!kind} onCancel={() => setKind(undefined)} onOk={submit} confirmLoading={busy} okText="保存" cancelText="取消" width={680} destroyOnHidden>
   <Form form={form} layout="vertical" preserve={false} style={{ marginTop: 24 }}>
    <Form.Item name="name" label={kind === 'tool' ? '工具名称' : kind === 'key' ? '调用方名称' : '服务名称'} rules={[{ required: true, message: '请输入名称' }, ...(kind === 'tool' ? [{ pattern: /^[A-Za-z][A-Za-z0-9_-]{0,63}$/, message: '以字母开头，使用字母、数字、横线或下划线，最多 64 位' }] : [])]}><Input placeholder={kind === 'tool' ? '例如 get_product' : kind === 'key' ? '例如商城助手' : '例如商品 MCP'} /></Form.Item>
    {(kind === 'server' || kind === 'tool') && <Form.Item name="app" label="应用" rules={[{ required: true, message: '选择应用' }]}><Select showSearch optionFilterProp="label" options={apps.map(a => ({ value: a.name, label: a.name }))} onChange={() => { if (kind === 'tool') form.setFieldsValue({ operationId: undefined, serverId: undefined }); }} /></Form.Item>}
    {kind === 'server' && <>
     <Form.Item name="endpointText" label="MCP 访问路径" extra="每行一个路径，例如 /product/mcp。多个路径共享同一服务身份、工具和授权；不同服务不能使用相同路径。客户端使用所属应用域名加此路径连接，代理需保留 Host。" rules={[{ required: true, message: '请输入访问路径' }]}><Input.TextArea rows={3} placeholder="/product/mcp" /></Form.Item>
     <Space align="start"><Form.Item name="header" label="Key 请求头名称" rules={[{ required: true }]}><Input placeholder="X-MCP-Key" onChange={e => { if (e.target.value.toLowerCase() === 'authorization') form.setFieldValue('bearer', true); }} /></Form.Item><Form.Item name="bearer" label="使用 Bearer 前缀" valuePropName="checked"><Switch /></Form.Item></Space>
     <Form.Item name="enabled" label="启用 MCP 服务" valuePropName="checked"><Switch /></Form.Item>
    </>}
    {kind === 'tool' && <>
     {opsError && <Alert type="error" title="接口加载失败" description={opsError} />}
     <Form.Item name="operationId" label="OpenAPI 接口" rules={[{ required: true, message: '选择接口' }]}><Select showSearch optionFilterProp="label" loading={opsLoading} disabled={!selectedApp} options={operations} onChange={id => { const op = operations.find(o => o.value === id)?.op; if (op) { if (!form.getFieldValue('name')) form.setFieldValue('name', `${op.tags?.[0] || 'Tool'}_${op['x-rpc-method'] || 'Call'}`); form.setFieldValue('description', op.description || op.summary || ''); } }} /></Form.Item>
     <Form.Item name="serverId" label="挂载的 MCP 服务" rules={[{ required: true, message: '选择 MCP 服务' }]}><Select options={data?.servers.filter(s => s.app === selectedApp).map(s => ({ value: s.id, label: s.name }))} notFoundContent="请先为该应用新增 MCP 服务" /></Form.Item>
     <Form.Item name="description" label="工具说明"><Input.TextArea rows={3} maxLength={4000} /></Form.Item>
     <Typography.Paragraph type="secondary">参数和返回结构由接口自动生成。新增工具不会自动授权给已有 Key，请到 Key 授权中勾选。</Typography.Paragraph>
    </>}
    {kind === 'key' && <>
     <Form.Item name="enabled" label="启用 Key" valuePropName="checked"><Switch /></Form.Item>
     <Form.List name="grants">{(fields, { add, remove: removeGrant }) => <>{fields.map(field => <Card size="small" key={field.key} style={{ marginBottom: 12 }} extra={<Button type="text" danger onClick={() => removeGrant(field.name)}>移除授权</Button>}>
      <Form.Item name={[field.name, 'serverId']} label="允许接入的 MCP 服务" rules={[{ required: true }]}><Select options={data?.servers.map(s => ({ value: s.id, label: s.name }))} onChange={() => form.setFieldValue(['grants', field.name, 'toolIds'], [])} /></Form.Item>
      <Form.Item noStyle shouldUpdate>{() => { const id = form.getFieldValue(['grants', field.name, 'serverId']); return <Form.Item name={[field.name, 'toolIds']} label="允许调用的工具"><Select mode="multiple" placeholder="选择工具；留空仅能连接，不可调用任何工具" options={data?.tools.filter(t => t.serverId === id).map(t => ({ value: t.id, label: t.name }))} /></Form.Item>; }}</Form.Item>
     </Card>)}<Button block type="dashed" onClick={() => add({ toolIds: [] })}>添加服务授权</Button></>}</Form.List>
     <Typography.Paragraph type="secondary" style={{ marginTop: 16 }}>Key 只在创建或轮换成功后显示一次。同一个 Key 可以分别授权多个 MCP 服务。</Typography.Paragraph>
    </>}
   </Form>
  </Modal>
  <Modal title={`Key 授权 · ${authServer?.name || ''}`} open={!!authServer} onCancel={() => setAuthServer(undefined)} onOk={saveAccess} confirmLoading={busy} okText="保存授权" cancelText="取消" width={640}>
   <Form form={authForm} layout="vertical" style={{ marginTop: 20 }}><Form.List name="access">{(fields, { add, remove: removeAccess }) => <>{fields.map(field => <Card key={field.key} size="small" style={{ marginBottom: 12 }} extra={<Button type="text" danger onClick={() => removeAccess(field.name)}>移除</Button>}>
    <Form.Item name={[field.name, 'keyId']} label="调用方 Key" rules={[{ required: true, message: '请选择调用方 Key' }]}><Select options={data?.keys.map(k => ({ value: k.id, label: `${k.name}${k.enabled ? '' : '（已停用）'}` }))} /></Form.Item>
    <Form.Item name={[field.name, 'toolIds']} label="允许调用的工具"><Select mode="multiple" placeholder="留空仅能连接，不可调用任何工具" options={data?.tools.filter(t => t.serverId === authServer?.id).map(t => ({ value: t.id, label: t.name }))} /></Form.Item>
   </Card>)}<Button block type="dashed" onClick={() => add({ toolIds: [] })}>添加调用方</Button></>}</Form.List></Form>
  </Modal>
  <Modal title="保存调用方 Key" open={!!secret} onCancel={() => setSecret('')} onOk={() => setSecret('')} okText="我已保存" cancelButtonProps={{ style: { display: 'none' } }} maskClosable={false}>
   <Alert type="warning" title="该 Key 仅显示一次，请妥善保存" showIcon style={{ margin: '20px 0' }} /><Typography.Paragraph copyable={{ text: secret }} style={{ overflowWrap: 'anywhere', fontFamily: 'monospace' }}>{secret}</Typography.Paragraph>
  </Modal>
  <Modal title="MCP 工具定义" open={schema !== undefined} onCancel={() => setSchema(undefined)} footer={null} width={850}><pre className="docs-example" style={{ maxHeight: '65vh' }}>{schema}</pre></Modal>
 </PageContainer>;
}
