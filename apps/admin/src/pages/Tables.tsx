import { useEffect, useState } from 'react';
import { api, ApiError, Page } from '../api/client';

interface TableItem {
  id: number;
  store_id: number;
  table_no: string;
  area: string;
  enabled: boolean;
  table_token: string;
}

export default function Tables({ role }: { role?: string }) {
  const [items, setItems] = useState<TableItem[]>([]);
  const [err, setErr] = useState('');
  const [tableNo, setTableNo] = useState('');
  const [area, setArea] = useState('');
  const canWrite = role === 'MANAGER' || role === 'OWNER';

  async function refresh() {
    try {
      const data = await api.get<Page<TableItem>>('/api/v1/admin/tables');
      setItems(data.items || []);
    } catch (e) {
      setErr((e as ApiError).message);
    }
  }
  useEffect(() => {
    refresh();
  }, []);

  async function create() {
    try {
      await api.post('/api/v1/admin/tables', { table_no: tableNo, area });
      setTableNo('');
      setArea('');
      refresh();
    } catch (e) {
      setErr((e as ApiError).message);
    }
  }

  async function rotate(id: number) {
    // 换码：新 token，旧码立即失效（PRD §10.1）。
    if (!confirm('换码会生成新桌码，旧码立即失效。继续？')) return;
    try {
      await api.post(`/api/v1/admin/tables/${id}/rotate-token`, {});
      refresh();
    } catch (e) {
      setErr((e as ApiError).message);
    }
  }

  return (
    <div>
      <h2>桌台</h2>
      {err && <div className="error-box">{err}</div>}
      {canWrite && (
        <div className="card row">
          <input placeholder="桌号" value={tableNo} onChange={(e) => setTableNo(e.target.value)} style={{ maxWidth: 140 }} />
          <input placeholder="区域" value={area} onChange={(e) => setArea(e.target.value)} style={{ maxWidth: 140 }} />
          <button className="primary" onClick={create}>
            新建
          </button>
        </div>
      )}
      <div className="card">
        <table>
          <thead>
            <tr>
              <th>桌号</th>
              <th>区域</th>
              <th>token</th>
              <th>状态</th>
              {canWrite && <th>操作</th>}
            </tr>
          </thead>
          <tbody>
            {items.map((t) => (
              <tr key={t.id}>
                <td>{t.table_no}</td>
                <td>{t.area}</td>
                <td className="muted" style={{ fontFamily: 'monospace' }}>
                  {(t.table_token || '').slice(0, 12)}…
                </td>
                <td>{t.enabled ? <span className="tag">启用</span> : <span className="tag danger">停用</span>}</td>
                {canWrite && (
                  <td>
                    <button onClick={() => rotate(t.id)}>换码</button>
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
