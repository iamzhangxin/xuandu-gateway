import { App, Form, Input, Modal, Tag, Typography } from 'antd';
import { useEffect, useState } from 'react';
import { saveApp, type Application, type FormValues } from '@/services/gateway';
export function StateTag({ state }: { state: string }) {
 const styles: Record<string, [string, string]> = {
  ready: ['success', '正常'], degraded: ['warning', '降级'], disabled: ['default', '已停用'],
 };
 const [color, label] = styles[state] || ['processing', '待加载'];
 return <Tag color={color} className="state-tag"><span className="state-dot" />{label}</Tag>;
}
export function Digest({ value }: { value?: string }) {
 return value ? <Typography.Text className="digest" copyable={{ text: value }}>{value}</Typography.Text> : <span className="muted">—</span>;
}
export function ContractModal({ open, editing, onClose, onSaved }: {
 open: boolean; editing?: Application; onClose: () => void; onSaved: () => void;
}) {
 const [form] = Form.useForm<FormValues>();
 const [busy, setBusy] = useState(false);
 const { message } = App.useApp();
 useEffect(() => { if (open) { form.resetFields(); form.setFieldsValue({
  name: editing?.name || '', serviceName: editing?.serviceName || '', rpcTimeout: editing?.rpcTimeout || '3s', url: '',
 }); } }, [open, editing, form]);
 const submit = async () => {
  let values: FormValues;
  try { values = await form.validateFields(); } catch { return; }
  setBusy(true);
  try { const result = await saveApp(values, editing);
   message.success(editing ? ('changed' in result && !result.changed ? '契约没有变化' : '契约已更新') : '应用已创建');
   onSaved(); onClose();
  } catch (error) { message.error((error as Error).message); } finally { setBusy(false); }
 };
 return <Modal title={editing ? `更新契约 · ${editing.name}` : '新增应用'} open={open}
  onCancel={onClose} onOk={submit} confirmLoading={busy} maskClosable={!busy}
  cancelButtonProps={{ disabled: busy }} okText={editing ? '校验并更新' : '创建应用'} cancelText="取消" width={560}>
  <p className="modal-intro">上传包含多个 IDL 的 ZIP 下载链接，校验通过后发布路由。</p>
  <Form form={form} layout="vertical" requiredMark={false}>
   <Form.Item name="name" label="应用名称" rules={[{ required: true, message: '请输入应用名称' }, { pattern: /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$/, message: '使用字母、数字、横线或下划线' }]}>
    <Input disabled={!!editing} placeholder="例如 product-service" />
   </Form.Item>
   <div className="form-row"><Form.Item name="serviceName" label="Consul 服务" rules={[{ required: true, message: '请输入服务名称' }]}>
    <Input placeholder="例如 product-service" />
   </Form.Item><Form.Item name="rpcTimeout" label="RPC 超时" rules={[{ required: true, message: '请输入超时时间' }]}><Input placeholder="3s" /></Form.Item></div>
   <Form.Item name="url" label="ZIP 下载链接" extra={editing ? '留空保留已保存的契约；导入新版本时填写一次性链接，链接不会保存。' : 'ZIP 内仅包含 idl/ 目录下的 .thrift 文件。'}
    rules={[{ required: !editing || editing.idl.type !== 'zip', message: '请输入 ZIP 下载链接' }, { validator: (_, value) => {
      if (!value?.trim()) return Promise.resolve();
      try { const u = new URL(value.trim()); if (['http:', 'https:'].includes(u.protocol)) return Promise.resolve(); } catch { /* show validation below */ }
      return Promise.reject(new Error('请输入有效的 HTTP(S) 链接'));
    } }]}><Input.TextArea rows={3} placeholder="https://…/contract.zip" autoComplete="off" /></Form.Item>
  </Form>
 </Modal>;
}
