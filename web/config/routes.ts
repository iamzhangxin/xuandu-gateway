export default [
 { path: '/mcp', name: 'MCP 服务', component: './mcp' },
 { path: '/docs', name: '接口文档', component: './apps/docs' },
 { path: '/docs/:name', name: '接口文档', component: './apps/docs' },
 { path: '/apps', name: '应用管理', component: './apps' },
 { path: '/apps/:name', name: '应用详情', component: './apps/detail' },
 { path: '/', redirect: '/apps' },
 { path: '/*', component: './404', layout: false },
];
