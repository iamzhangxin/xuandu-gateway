import type { RunTimeLayoutConfig } from '@umijs/max';
import { Tag } from 'antd';
export async function getInitialState() { return {}; }
export const layout: RunTimeLayoutConfig = () => ({
 title: '玄渡', logo: <img className="brand-mark" src="/xuandu.jpg" alt="玄渡 xuandu" />,
 layout: 'mix', navTheme: 'light', fixedHeader: true,
 contentWidth: 'Fluid', menuRender: false, breadcrumbRender: false, avatarProps: undefined,
 actionsRender: () => [<Tag key="label" bordered={false}>管理控制台</Tag>],
 footerRender: false,
 token: { header: { colorBgHeader: '#fff', heightLayoutHeader: 64 } },
});
