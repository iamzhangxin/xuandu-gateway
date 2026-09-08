import { defineConfig } from '@umijs/max';
import routes from './routes';
export default defineConfig({
 esbuildMinifyIIFE: true,
 title: '玄渡 · xuandu', favicons: ['/xuandu.jpg'], hash: true, history: { type: 'hash' }, publicPath: '/',
 routes, model: {}, initialState: {}, layout: { locale: false },
 locale: { default: 'zh-CN', antd: true, baseNavigator: false },
 antd: { appConfig: {}, configProvider: { theme: { token: {
  colorPrimary: '#2563eb', borderRadius: 8, colorBgLayout: '#f5f7fb',
  fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", sans-serif',
 } } } },
 mock: false, fastRefresh: true,
 proxy: { '/admin/': { target: 'http://127.0.0.1:9090', changeOrigin: true } },
});
