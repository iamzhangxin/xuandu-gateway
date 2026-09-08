export type McpServer = { id: string; name: string; app: string; endpoints: string[]; header: string; bearer: boolean; enabled: boolean };
export type McpTool = { id: string; name: string; description: string; serverId: string; operationId: string };
export type Grant = { serverId: string; toolIds: string[] };
export type McpKey = { id: string; name: string; enabled: boolean; grants: Grant[] };
export type Catalog = { version: number; servers: McpServer[]; tools: McpTool[]; keys: McpKey[]; listenerEnabled: boolean; listenerError?: string; statuses: { serverId: string; status: string; error?: string; revision: string; toolCount: number }[] };
export async function mcpApi<T>(path = '', body?: unknown): Promise<T> {
 const response = await fetch(`/admin/mcp${path}`, { method: body ? 'POST' : 'GET', headers: { 'Content-Type': 'application/json' }, ...(body ? { body: JSON.stringify(body) } : {}) });
 const data = await response.json(); if (!response.ok) throw new Error(data.message || 'MCP 操作失败'); return data;
}
