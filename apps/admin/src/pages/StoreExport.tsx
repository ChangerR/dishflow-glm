import { useState } from 'react';
import { api, ApiError } from '../api/client';

export default function StoreExport({ role }: { role?: string }) {
  const [err, setErr] = useState('');
  const [msg, setMsg] = useState('');
  const isOwner = role === 'OWNER';

  async function doExport() {
    try {
      const data = await api.get<Record<string, unknown>>('/api/v1/admin/store/export');
      // 不含密钥/订单/会员（PRD §11）。
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = 'dishflow.store-export.json';
      a.click();
      URL.revokeObjectURL(url);
      setMsg('已导出');
      setErr('');
    } catch (e) {
      setErr((e as ApiError).message);
    }
  }

  async function onImport(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    if (!file) return;
    // 导入属于不可撤销高风险操作，UI 二次确认（PRD §11）。
    if (!confirm('导入将用备份菜单替换当前菜单（旧菜单进回收站），不可撤销。确认继续？')) return;
    try {
      const text = await file.text();
      await api.post('/api/v1/admin/store/import', JSON.parse(text));
      setMsg('已导入');
      setErr('');
    } catch (e) {
      const apiErr = e as ApiError;
      setErr(apiErr.code === 'WECHAT_APPID_CONFLICT' ? 'AppID 与其它门店冲突' : apiErr.message);
    }
  }

  return (
    <div>
      <h2>备份导入导出</h2>
      {err && <div className="error-box">{err}</div>}
      {msg && <div className="tag" style={{ display: 'inline-block', padding: '8px 12px' }}>{msg}</div>}
      <div className="card col" style={{ maxWidth: 400 }}>
        <p className="muted">导出基础信息/菜单/小程序配置（不含密钥、订单、会员）。店长和店主可导出。</p>
        <button className="primary" onClick={doExport}>
          导出
        </button>
        {isOwner && (
          <>
            <hr />
            <p className="muted">仅店主可导入。菜单策略为 replace，旧菜单进入回收站。</p>
            <input type="file" accept="application/json" onChange={onImport} />
          </>
        )}
      </div>
    </div>
  );
}
