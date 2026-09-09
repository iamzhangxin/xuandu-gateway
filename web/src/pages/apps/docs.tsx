import { DownloadOutlined, ReloadOutlined, SearchOutlined } from '@ant-design/icons';
import { Alert, Button, Collapse, Empty, Input, Select, Space, Spin, Table, Tag, Tree, Typography } from 'antd';
import { useEffect, useState } from 'react';
import { history, useParams } from '@umijs/max';
import { api, listApps, type Application } from '@/services/gateway';

type Schema = { example?: unknown; pattern?: string; $ref?: string; title?: string; type?: string; format?: string; description?: string; properties?: Record<string, Schema>; required?: string[]; items?: Schema; additionalProperties?: Schema; enum?: unknown[]; allOf?: Schema[]; nullable?: boolean };
type Parameter = { name: string; in: string; required: boolean; description?: string; schema: Schema };
type Content = { content?: Record<string, { schema: Schema }>; description?: string };
type Operation = { 'x-xuandu-auth'?: string; tags?: string[]; operationId: string; summary: string; description?: string; parameters?: Parameter[]; requestBody?: Content; responses: Record<string, Content>; 'x-idl-file': string; 'x-rpc-method': string };
type Document = { info: { title: string; version: string; description?: string }; paths: Record<string, Record<string, Operation>>; components: { schemas: Record<string, Schema> } };
function resolve(schema: Schema, doc: Document, seen = new Set<string>()): Schema {
 if (schema.$ref) {
  if (seen.has(schema.$ref)) return { type: 'object', description: '递归引用', title: schema.$ref.split('.').pop() };
  seen.add(schema.$ref);
  return resolve(doc.components.schemas[schema.$ref.replace('#/components/schemas/', '')] || {}, doc, seen);
 }
 if (schema.allOf) return Object.assign({}, ...schema.allOf.map(s => resolve(s, doc, new Set(seen))), { description: schema.description });
 return schema;
}
function typeName(schema: Schema, doc: Document): string {
 const s = resolve(schema, doc);
 return (s.title || s.type || 'any') + (s.format ? ` (${s.format})` : '') + (s.type === 'array' ? ` <${resolve(s.items || {}, doc).title || s.items?.type || 'object'}>` : '');
}
function Model({ schema, doc, depth = 0 }: { schema: Schema; doc: Document; depth?: number }) {
 const s = resolve(schema, doc);
 if (depth > 6) return <Typography.Text type="secondary">嵌套结构请查看下载的 OpenAPI 文件</Typography.Text>;
 if (s.items) return <Model schema={s.items} doc={doc} depth={depth + 1} />;
 if (!s.properties) return <Typography.Paragraph style={{ whiteSpace: 'pre-wrap' }}>{typeName(schema, doc)} {s.description} {s.enum && `可选值：${JSON.stringify(s.enum)}`}</Typography.Paragraph>;
 return <Table size="small" pagination={false} rowKey="name" dataSource={Object.entries(s.properties).map(([name, field]) => ({ name, field, required: s.required?.includes(name) }))}
  expandable={{ rowExpandable: row => { const f = resolve(row.field, doc); return !!(f.properties || f.items || f.additionalProperties); }, expandedRowRender: row => { const f = resolve(row.field, doc); return <Model schema={f.items || f.additionalProperties || row.field} doc={doc} depth={depth + 1} />; } }}
  columns={[{ title: '字段', dataIndex: 'name', render: name => <Typography.Text code>{name}</Typography.Text> }, { title: '类型', render: (_, row) => typeName(row.field, doc) }, { title: '必填', render: (_, row) => row.required ? '是' : '否' }, { title: '说明', render: (_, row) => { const f = resolve(row.field, doc); return <span style={{ whiteSpace: 'pre-wrap' }}>{f.description || '—'}{f.enum && ` · ${JSON.stringify(f.enum)}`}</span>; } }]} />;
}
function example(schema: Schema, doc: Document, depth = 0): unknown {
 const s = resolve(schema, doc);
 if (s.example !== undefined) return s.example;
 if (s.pattern === '^-?[0-9]+$') return '0';
 if (s.enum?.length) return s.enum[0];
 if (depth > 5) return null;
 if (s.properties) return Object.fromEntries(Object.entries(s.properties).map(([k, v]) => [k, example(v, doc, depth + 1)]));
 if (s.items) return [example(s.items, doc, depth + 1)];
 if (s.additionalProperties) return { key: example(s.additionalProperties, doc, depth + 1) };
 if (s.type === 'boolean') return false;
 if (s.type === 'number' || s.type === 'integer') return 0;
 return s.type === 'object' ? {} : 'string';
}
function Example({ value }: { value: unknown }) { return <pre className="docs-example">{JSON.stringify(value, null, 2)}</pre>; }
export default function APIDocs() {
 const { name = '' } = useParams<{ name: string }>();
 const [apps, setApps] = useState<Application[]>([]); const [doc, setDoc] = useState<Document>();
 const [error, setError] = useState(''); const [catalogError, setCatalogError] = useState('');
 const [loading, setLoading] = useState(false); const [query, setQuery] = useState('');
 const [selected, setSelected] = useState(''); const [refresh, setRefresh] = useState(0);
 useEffect(() => { let active = true; setCatalogError('');
  listApps().then(a => { if (!active) return; setApps(a); if (!name && a.length) history.replace(`/docs/${encodeURIComponent(a[0].name)}`); }).catch(e => { if (active) setCatalogError(e.message); });
  return () => { active = false; };
 }, [name, refresh]);
 useEffect(() => { let active = true; setDoc(undefined); setSelected(''); setError(''); setQuery('');
  if (!name) return; setLoading(true);
  api<Document>(`/${encodeURIComponent(name)}/openapi.json`).then(d => { if (active) setDoc(d); }).catch(e => { if (active) setError(e.message); }).finally(() => { if (active) setLoading(false); });
  return () => { active = false; };
 }, [name, refresh]);
 const download = () => { if (!doc) return; const url = URL.createObjectURL(new Blob([JSON.stringify(doc, null, 2)], { type: 'application/json' })); const a = document.createElement('a'); a.href = url; a.download = `${name}.openapi.json`; a.click(); URL.revokeObjectURL(url); };
 const entries = doc ? Object.entries(doc.paths).flatMap(([path, methods]) => Object.entries(methods).map(([method, op]) => ({ key: `${method} ${path}`, path, method, op }))) : [];
 const filtered = entries.filter(({ path, method, op }) => `${path} ${method} ${op.summary} ${op.tags?.join(' ')} ${op['x-rpc-method']} ${op['x-idl-file']}`.toLowerCase().includes(query.toLowerCase()));
 const groups = [...new Set(filtered.map(e => (e.op.tags?.[0] || '未分组')))];
 const entry = filtered.find(e => e.key === selected) || filtered[0];
 const colors: Record<string, string> = { get: 'green', post: 'blue', put: 'orange', delete: 'red' };
 return <div className="docs-page">
  <div className="docs-toolbar"><Space><Button onClick={() => history.push('/apps')}>返回应用管理</Button><Typography.Title level={4} style={{ margin: 0 }}>接口文档</Typography.Title></Space><Space wrap><Button icon={<ReloadOutlined />} loading={loading} onClick={() => setRefresh(v => v + 1)}>刷新</Button><Button icon={<DownloadOutlined />} disabled={!doc} onClick={download}>下载 OpenAPI</Button></Space></div>
  <div className="docs-workspace"><aside className="docs-navigation">
   <Typography.Text strong>应用</Typography.Text>
   <Select aria-label="选择文档应用" showSearch optionFilterProp="label" value={name || undefined} placeholder="选择应用" style={{ width: '100%', margin: '12px 0' }} options={apps.map(a => ({ value: a.name, label: a.name }))} onChange={v => history.push(`/docs/${encodeURIComponent(v)}`)} />
   <Input prefix={<SearchOutlined />} placeholder="搜索接口、路径或说明" allowClear value={query} onChange={e => setQuery(e.target.value)} />
   <div className="docs-nav-caption">HTTP 接口 <span>{entries.length}</span></div>
   <Tree key={`${name}:${query}:${doc?.info.version}`} blockNode defaultExpandAll selectedKeys={entry ? [entry.key] : []} onSelect={keys => { if (keys.length) setSelected(String(keys[0])); }} treeData={groups.map(group => ({ key: `service:${group}`, selectable: false, title: <strong>{group} <span className="muted">({filtered.filter(e => (e.op.tags?.[0] || '未分组') === group).length})</span></strong>, children: filtered.filter(e => (e.op.tags?.[0] || '未分组') === group).map(e => ({ key: e.key, title: <span className="docs-tree-item" title={`${e.method.toUpperCase()} ${e.path} · ${e.op.summary}`}><Tag color={colors[e.method]}>{e.method.toUpperCase()}</Tag><span>{e.op.summary || e.path}</span></span> })) }))} />
   {!loading && !filtered.length && <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无接口" />}
  </aside><main className="docs-content" key={entry?.key}>
   {name && <Alert type="info" title={`应用域名：${apps.find(a => a.name === name)?.domain || '待配置'} · 请求头 X-App-Code: ${name}`} showIcon style={{ marginBottom: 16 }} />}
   {(error || catalogError) && <Alert type="error" title="文档加载失败" description={error || catalogError} showIcon />}
   {loading && <Spin />}{!loading && !entry && !error && <Empty description={query ? '没有匹配的接口' : '选择应用查看接口文档'} />}
   {doc && entry && <>
    <div className="docs-operation-header"><Typography.Text type="secondary">{name} / {(entry.op.tags?.[0] || '未分组')}</Typography.Text><Typography.Title level={3}>{entry.op.summary || entry.op['x-rpc-method']}</Typography.Title><Space wrap><Tag color={entry.op['x-xuandu-auth'] === 'required' ? 'orange' : 'default'}>{entry.op['x-xuandu-auth'] === 'required' ? '需要登录' : '允许匿名'}</Tag><Tag color={colors[entry.method]}>{entry.method.toUpperCase()}</Tag><Typography.Text code copyable>{entry.path}</Typography.Text></Space><div className="docs-metadata">RPC：{entry.op.tags?.[0]}.{entry.op['x-rpc-method']}<br />IDL：{entry.op['x-idl-file']}<br />版本：{doc.info.version}</div></div>
    <section className="docs-section"><h3>接口说明</h3><Typography.Paragraph style={{ whiteSpace: 'pre-wrap' }}>{entry.op.description || 'IDL 未提供接口注释'}</Typography.Paragraph></section>
    <section className="docs-section"><h3>请求参数</h3><Typography.Paragraph type="secondary">登录要求与应用身份由网关校验；其余字段标记来自 IDL，业务校验由下游负责。</Typography.Paragraph>
     {['path', 'query', 'header', 'cookie'].filter(location => entry.op.parameters?.some(p => p.in === location)).map(location => <div key={location}><h4>{location.toUpperCase()}</h4><Table<Parameter> size="small" pagination={false} rowKey="name" scroll={{ x: 560 }} dataSource={entry.op.parameters?.filter(p => p.in === location)} columns={[{ title: '字段名', dataIndex: 'name' }, { title: '必填', render: (_, p) => p.required ? '是' : '否' }, { title: '类型', render: (_, p) => typeName(p.schema, doc) }, { title: '说明', dataIndex: 'description' }]} /></div>)}
     {entry.op.requestBody?.content?.['application/json'] && <><h4>Body · application/json</h4><Model schema={entry.op.requestBody.content['application/json'].schema} doc={doc} /></>}
    </section>
    <Collapse ghost items={[{ key: 'request', label: '请求示例（根据类型生成）', children: <Example value={{ method: entry.method.toUpperCase(), path: entry.path, ...Object.fromEntries(['path', 'query', 'header', 'cookie'].filter(location => entry.op.parameters?.some(p => p.in === location)).map(location => [location === 'path' ? 'pathParameters' : location, Object.fromEntries((entry.op.parameters || []).filter(p => p.in === location).map(p => [p.name, example(p.schema, doc)]))])), ...(entry.op.requestBody?.content?.['application/json'] ? { body: example(entry.op.requestBody.content['application/json'].schema, doc) } : {}) }} /> }]} />
    {Object.entries(entry.op.responses).map(([status, response]) => <section className="docs-section" key={status}><h3>{status === 'default' ? '失败响应' : `响应参数 · HTTP ${status}`}</h3><Typography.Paragraph type="secondary">{response.description}</Typography.Paragraph>{response.content?.['application/json'] && <><Model schema={response.content['application/json'].schema} doc={doc} /><Collapse ghost items={[{ key: status, label: '响应示例（根据类型生成）', children: <Example value={example(response.content['application/json'].schema, doc)} /> }]} /></>}</section>)}
   </>}
  </main></div>
 </div>;
}
