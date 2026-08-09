import { useEffect, useState } from 'react';
import { api, ApiError, Page } from '../api/client';

interface Material {
  id: number;
  name: string;
  unit: string;
  category: string;
  enabled: boolean;
}

export default function Materials({ role }: { role?: string }) {
  const [items, setItems] = useState<Material[]>([]);
  const [err, setErr] = useState('');
  const [name, setName] = useState('');
  const [unit, setUnit] = useState('');
  const canWrite = role === 'MANAGER' || role === 'OWNER';

  async function refresh() {
    try {
      const data = await api.get<Page<Material>>('/api/v1/admin/materials');
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
      await api.post('/api/v1/admin/materials', { name, unit, category: '' });
      setName('');
      setUnit('');
      refresh();
    } catch (e) {
      setErr((e as ApiError).message);
    }
  }

  return (
    <div>
      <h2>物料</h2>
      {err && <div className="error-box">{err}</div>}
      {canWrite && (
        <div className="card row">
          <input placeholder="名称" value={name} onChange={(e) => setName(e.target.value)} style={{ maxWidth: 160 }} />
          <input placeholder="单位" value={unit} onChange={(e) => setUnit(e.target.value)} style={{ maxWidth: 100 }} />
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
              <th>单位</th>
              <th>分类</th>
              <th>状态</th>
            </tr>
          </thead>
          <tbody>
            {items.map((m) => (
              <tr key={m.id}>
                <td>{m.name}</td>
                <td>{m.unit}</td>
                <td>{m.category}</td>
                <td>{m.enabled ? <span className="tag">启用</span> : <span className="tag danger">停用</span>}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
