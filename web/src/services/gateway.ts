export type Route = { httpMethod: string; path: string; rpcService: string; rpcMethod: string; idlPath: string };
export type Application = {
 name: string; serviceName: string; enabled: boolean; rpcTimeout: string;
 idl: { type: string; resolvedRevision: string };
 currentRevision: string; targetRevision: string; runtimeStatus: string; routeCount: number;
 lastUpdateTime: string; lastUpdateResult: string;
};
export type Detail = Application & { routes: Route[] };
export type FormValues = { name: string; serviceName: string; rpcTimeout: string; url?: string };
export async function api<T>(path: string, method = 'GET', body?: unknown): Promise<T> {
 const response = await fetch(`/admin/apps${path}`, { method,
  headers: { 'Content-Type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body) });
 const data = await response.json();
 if (!response.ok) throw new Error(data.message || data.code || '操作失败');
 return data;
}
export const listApps = () => api<Application[]>('');
export const getApp = (name: string) => api<Detail>(`/${encodeURIComponent(name)}`);
export const deleteApp = (name: string) => api(`/${encodeURIComponent(name)}`, 'DELETE');
export function saveApp(values: FormValues, editing?: Application) {
 const source = values.url?.trim() ? { type: 'zip', url: values.url.trim() } : undefined;
 return editing
 ? api<{ changed: boolean }>(`/${encodeURIComponent(editing.name)}/update`, 'POST', {
   serviceName: values.serviceName, rpcTimeout: values.rpcTimeout, ...(source ? { idl: source } : {}),
 })
 : api<Application>('', 'POST', { name: values.name, serviceName: values.serviceName,
   rpcTimeout: values.rpcTimeout, idl: source });
}
export const revision = (app: Application) => app.currentRevision || app.idl.resolvedRevision;
export function updatedAt(value: string) {
 if (!value || value.startsWith('0001-')) return '尚未更新';
 return new Date(value).toLocaleString('zh-CN', { hour12: false });
}
