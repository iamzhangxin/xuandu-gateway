import { PlusOutlined, ReloadOutlined, SearchOutlined, MoreOutlined, AppstoreOutlined, CheckCircleOutlined, BranchesOutlined } from '@ant-design/icons';
import { PageContainer } from '@ant-design/pro-components';
import { Link } from '@umijs/max';
import { App, Button, Card, Dropdown, Input, Space, Table, Typography } from 'antd';
import { useCallback, useEffect, useState } from 'react';
import { ContractModal, Digest, StateTag } from '@/components/Gateway';
import { listApps, deleteApp, revision, updatedAt, type Application } from '@/services/gateway';
export default function Applications() {
 const { message, modal } = App.useApp();
 const [apps, setApps] = useState<Application[]>([]);
 const [loading, setLoading] = useState(true);
 const [query, setQuery] = useState('');
 const [open, setOpen] = useState(false);
 const [editing, setEditing] = useState<Application>();
 const load = useCallback(async () => { setLoading(true); try { setApps(await listApps()); }
  catch (e) { message.error((e as Error).message); } finally { setLoading(false); } }, [message]);
 useEffect(() => { void load(); }, [load]);
 const remove = (row: Application) => modal.confirm({ title: `删除应用 ${row.name}？`, content: '删除后，该应用的 HTTP 路由将下线。', okText: '删除应用', cancelText: '取消', okButtonProps: { danger: true },
  onOk: async () => { try { await deleteApp(row.name); message.success('应用已删除'); await load(); } catch (e) { message.error((e as Error).message); throw e; } } });
 const visible = apps.filter(a => `${a.name} ${a.serviceName}`.toLowerCase().includes(query.toLowerCase()));
 return <PageContainer title="应用管理" subTitle="管理应用契约与 HTTP 路由" breadcrumbRender={false} extra={<Space><Link to="/mcp"><Button>MCP 服务</Button></Link><Link to="/docs"><Button>接口文档</Button></Link></Space>}>
  <div className="summary-grid">
   <Card><span className="summary-icon"><AppstoreOutlined /></span><div><div className="muted">应用总数</div><strong>{apps.length}</strong></div></Card>
   <Card><span className="summary-icon green"><CheckCircleOutlined /></span><div><div className="muted">运行正常</div><strong>{apps.filter(a => a.runtimeStatus === 'ready').length}</strong></div></Card>
   <Card><span className="summary-icon violet"><BranchesOutlined /></span><div><div className="muted">已发布路由</div><strong>{apps.reduce((sum, a) => sum + (a.routeCount || 0), 0)}</strong></div></Card>
  </div>
  <Card className="table-card" styles={{ body: { padding: 0 } }}>
   <div className="table-toolbar"><div><Typography.Text strong>应用列表</Typography.Text><span className="count-label">{apps.length} 个应用</span></div>
    <Space wrap><Input prefix={<SearchOutlined />} placeholder="搜索应用或服务" value={query} onChange={e => setQuery(e.target.value)} allowClear className="search-input" />
     <Button icon={<ReloadOutlined />} onClick={load} loading={loading}>刷新</Button>
     <Button type="primary" icon={<PlusOutlined />} onClick={() => { setEditing(undefined); setOpen(true); }}>新增应用</Button></Space>
   </div>
   <Table<Application> rowKey="name" dataSource={visible} loading={loading} scroll={{ x: 1100 }}
    pagination={{ defaultPageSize: 10, showSizeChanger: true, showTotal: n => `共 ${n} 个应用` }} locale={{ emptyText: '暂无应用，点击「新增应用」发布第一份契约' }} columns={[
     { title: '应用', dataIndex: 'name', width: 180, render: (name: string) => <Link className="app-link" to={`/apps/${encodeURIComponent(name)}`}>{name}</Link> },
     { title: '服务', dataIndex: 'serviceName', width: 165, render: (value: string) => <span className="service-name">{value}</span> },
     { title: '版本 · SHA-256', key: 'version', width: 290, render: (_, row) => <Digest value={revision(row)} /> },
     { title: '状态', dataIndex: 'runtimeStatus', width: 100, render: (state: string) => <StateTag state={state} /> },
     { title: '路由数', dataIndex: 'routeCount', width: 80, align: 'right', render: (n: number) => <span className="route-number">{n || 0}</span> },
     { title: '最近更新', dataIndex: 'lastUpdateTime', width: 180, render: (value: string) => <span className="date-cell">{updatedAt(value)}</span> },
     { title: '操作', key: 'actions', width: 180, fixed: 'right', render: (_, row) => <Space>
       <Link to={`/apps/${encodeURIComponent(row.name)}`}>路由</Link>
       <Link to={`/docs/${encodeURIComponent(row.name)}`}>文档</Link>
       <Dropdown menu={{ items: [{ key: 'update', label: '更新契约' }, { key: 'delete', label: '删除应用', danger: true }],
        onClick: ({ key }) => { if (key === 'update') { setEditing(row); setOpen(true); } else { remove(row); } } }} trigger={['click']}>
        <Button type="text" size="small" icon={<MoreOutlined />} aria-label={`更多操作 ${row.name}`} />
       </Dropdown></Space> },
    ]} />
  </Card>
  <ContractModal open={open} editing={editing} onClose={() => setOpen(false)} onSaved={() => { void load(); }} />
 </PageContainer>;
}
