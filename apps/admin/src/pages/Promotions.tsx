import { useEffect, useState } from 'react';
import { api, ApiError, Page, centsToYuan } from '../api/client';

interface Promotion {
  id: number;
  name: string;
  threshold_cents: number;
  discount_cents: number;
  starts_at: string;
  ends_at: string;
}

export default function Promotions({ role }: { role?: string }) {
  const [items, setItems] = useState<Promotion[]>([]);
  const [err, setErr] = useState('');
  const [name, setName] = useState('');
  const [threshold, setThreshold] = useState('');
  const [discount, setDiscount] = useState('');
  const canWrite = role === 'MANAGER' || role === 'OWNER';

  async function refresh() {
    try {
      const data = await api.get<Page<Promotion>>('/api/v1/admin/promotions');
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
      const now = new Date();
      await api.post('/api/v1/admin/promotions', {
        name,
        threshold_cents: parseInt(threshold, 10) || 0,
        discount_cents: parseInt(discount, 10) || 0,
        starts_at: now.toISOString(),
        ends_at: new Date(now.getTime() + 30 * 86400000).toISOString(),
      });
      setName('');
      setThreshold('');
      setDiscount('');
      refresh();
    } catch (e) {
      setErr((e as ApiError).message);
    }
  }

  return (
    <div>
      <h2>满减</h2>
      {err && <div className="error-box">{err}</div>}
      {canWrite && (
        <div className="card row">
          <input placeholder="名称" value={name} onChange={(e) => setName(e.target.value)} style={{ maxWidth: 160 }} />
          <input placeholder="门槛（分）" value={threshold} onChange={(e) => setThreshold(e.target.value)} style={{ maxWidth: 120 }} />
          <input placeholder="减免（分）" value={discount} onChange={(e) => setDiscount(e.target.value)} style={{ maxWidth: 120 }} />
          <button className="primary" onClick={create}>
            新建
          </button>
        </div>
      )}
      <div className="card">
        <table>
          <thead>
            <tr>
              <th>名称</th>
              <th>门槛</th>
              <th>减免</th>
              <th>有效期</th>
            </tr>
          </thead>
          <tbody>
            {items.map((p) => (
              <tr key={p.id}>
                <td>{p.name}</td>
                <td>{centsToYuan(p.threshold_cents)}</td>
                <td>{centsToYuan(p.discount_cents)}</td>
                <td className="muted">
                  {new Date(p.starts_at).toLocaleDateString()} ~ {new Date(p.ends_at).toLocaleDateString()}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
