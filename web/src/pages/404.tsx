import { Link } from '@umijs/max';
import { Button, Result } from 'antd';
export default function NotFound() { return <Result status="404" title="404" subTitle="页面不存在" extra={<Link to="/apps"><Button type="primary">返回应用列表</Button></Link>} />; }
