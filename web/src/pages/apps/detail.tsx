import { ReloadOutlined, UploadOutlined, SearchOutlined } from '@ant-design/icons';
import { PageContainer } from '@ant-design/pro-components';
import { history, useParams } from '@umijs/max';
import { Alert, App, Button, Card, Descriptions, Input, Result, Select, Skeleton, Space, Table, Tag, Typography } from 'antd';
import { useCallback, useEffect, useState } from 'react';
import { ContractModal, Digest, StateTag } from '@/components/Gateway';
import { getApp, revision, updatedAt, type Detail, type Route } from '@/services/gateway';
const methodColors: Record<string,string> = { GET: 'blue', POST: 'green', PUT: 'orange', DELETE: 'red' };
export default function AppDetail() {
 const { name = '' } = useParams<{ name: string }>();
 const { message } = App.useApp();
 const [data, setData] = useState<Detail>();
 const [loading, setLoading] = useState(true);
 const [error, setError] = useState('');
 const [query, setQuery] = useState('');
 const [method, setMethod] = useState<string>();
 const [open, setOpen] = useState(false);
 const load = useCallback(async () => { setLoading(true); setError(''); try { setData(await getApp(name)); }
 catch (e) { setError((e as Error).message); message.error((e as Error).message); } finally { setLoading(false); } }, [name, message]);
 useEffect(() => { void load(); }, [load]);
 if (!data && loading) return <Card><Skeleton active paragraph={{ rows: 8 }} /></Card>;
 if (!data) return <Result status="warning" title="无法加载应用" subTitle={error} extra={<Button onClick={load}>重试</Button>} />;
 const routes = (data.routes || []).filter(r => (!method || method === r.httpMethod) &&
  `${r.path} ${r.rpcService} ${r.rpcMethod} ${r.idlPath}`.toLowerCase().includes(query.toLowerCase()));
 return <PageContainer title={data.name} subTitle="应用详情" onBack={() => history.push('/apps')}
  breadcrumbRender={false}
  extra={[<Button key="docs" onClick={() => history.push(`/docs/${encodeURIComponent(name)}`)}>接口文档</Button>,<Button key="refresh" icon={<ReloadOutlined />} onClick={load} loading={loading}>刷新</Button>,
   <Button key="update" type="primary" icon={<UploadOutlined />} onClick={() => setOpen(true)}>更新应用</Button>]}>
  {error && <Alert type="error" title="刷新失败，当前显示上次加载的数据" description={error} showIcon style={{ marginBottom: 16 }} />}
  {!data.domain && <Alert type="warning" title="请更新应用并配置域名，配置完成后才能调用 HTTP 接口，无需重新上传契约。" showIcon style={{ marginBottom: 16 }} />}
  {data.domain && data.runtimeStatus === 'degraded' && <Alert type="warning" title={data.currentRevision ? "最近一次加载未成功，以下路由来自当前可用版本" : "契约加载失败，当前没有可用路由；请重新导入 ZIP 契约"} showIcon style={{ marginBottom: 16 }} />}

  <Card title="应用概览" className="overview-card"><Descriptions column={{ xs: 1, sm: 2, lg: 4 }} items={[
   { key: 'name', label: '应用', children: data.name }, { key: 'service', label: '服务', children: data.serviceName },
   { key: 'domain', label: '域名', children: data.domain || '待配置' },
   { key: 'code', label: 'X-App-Code', children: <Typography.Text copyable>{data.name}</Typography.Text> },
   { key: 'state', label: '状态', children: <StateTag state={data.runtimeStatus} /> }, { key: 'count', label: '路由数', children: data.routes?.length || 0 },
   { key: 'version', label: '版本 · SHA-256', span: 2, children: <Digest value={revision(data)} /> },
   { key: 'timeout', label: 'RPC 超时', children: data.rpcTimeout }, { key: 'updated', label: '最近更新', children: updatedAt(data.lastUpdateTime) },
  ]} /></Card>
  <Card className="table-card" styles={{ body: { padding: 0 } }}><div className="table-toolbar"><div><Typography.Text strong>路由信息</Typography.Text><span className="count-label">{data.routes?.length || 0} 条路由</span></div>
   <Space wrap><Select allowClear placeholder="全部方法" value={method} onChange={setMethod} style={{ width: 130 }} options={['GET','POST','PUT','DELETE'].map(value => ({ value, label: value }))} />
    <Input prefix={<SearchOutlined />} placeholder="搜索路径、RPC 或 IDL" value={query} onChange={e => setQuery(e.target.value)} allowClear className="search-input" /></Space></div>
   <Table<Route> rowKey={r => `${r.httpMethod} ${r.path}`} dataSource={routes} loading={loading} scroll={{ x: 900 }}
    pagination={{ defaultPageSize: 20, showSizeChanger: true, showTotal: n => `共 ${n} 条路由` }} locale={{ emptyText: '没有匹配的路由' }} columns={[
     { title: 'HTTP 方法', dataIndex: 'httpMethod', width: 120, render: (value: string) => <Tag color={methodColors[value]} className="method-tag">{value}</Tag> },
     { title: 'HTTP 路径', dataIndex: 'path', render: (value: string) => <Typography.Text className="path-cell" copyable>{value}</Typography.Text> },
     { title: 'RPC Service', dataIndex: 'rpcService', width: 180 },
     { title: 'RPC Method', dataIndex: 'rpcMethod', width: 150, render: (value: string) => <span className="path-cell">{value}</span> },
     { title: 'IDL 文件', dataIndex: 'idlPath', width: 220, render: (value: string) => <span className="idl-file">{value || '—'}</span> },
    ]} />
  </Card>

  <ContractModal open={open} editing={data} onClose={() => setOpen(false)} onSaved={() => { void load(); }} />
 </PageContainer>;
}
