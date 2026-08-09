import { useEffect, useState } from 'react';
import { api, ApiError, Category, Page } from '../api/client';

export default function Categories({ role }: { role?: string }) {
  const [items, setItems] = useState<Category[]>([]);
  const [err, setErr] = useState('');
  const [name, setName] = useState('');
  const [showDeleted, setShowDeleted] = useState(false);
  const canWrite = role === 'MANAGER' || role === 'OWNER';

  async function refresh() {
    try {
      const data = await api.get<Page<Category>>(`/api/v1/admin/categories${showDeleted ? '?deleted=1' : ''}`);
      setItems(data.items || []);
    } catch (e) {
      setErr((e as ApiError).message);
    }
  }
  useEffect(() => {
    refresh();
  }, [showDeleted]);

  async function create() {
    if (!name.trim()) return;
    try {
      await api.post('/api/v1/admin/categories', { name: name.trim(), enabled: true, sort_order: 0 });
      setName('');
      refresh();
    } catch (e) {
      setErr((e as ApiError).message);
    }
  }

  async function remove(id: number) {
    // 30 天软删除（同批次连带菜品，PRD §7.1）。
    if (!confirm('删除分类？分类及子菜品进入 30 天回收站。')) return;
    try {
      await api.del(`/api/v1/admin/categories/${id}`);
      refresh();
    } catch (e) {
      setErr((e as ApiError).message);
    }
  }

  async function restore(id: number) {
    try {
      await api.post(`/api/v1/admin/categories/${id}/restore`, {});
      refresh();
    } catch (e) {
      setErr((e as ApiError).message);
    }
  }

  return (
    <div>
      <h2>分类</h2>
      {err && <div className="error-box">{err}</div>}
      <div className="card">
        <div className="row">
          <input placeholder="新分类名称" value={name} onChange={(e) => setName(e.target.value)} />
          {canWrite && (
            <button className="primary" onClick={create}>
              新建
            </button>
          )}
          <label className="muted">
            <input type="checkbox" style={{ width: 'auto' }} checked={showDeleted} onChange={(e) => setShowDeleted(e.target.checked)} /> 显示回收站
          </label>
        </div>
      </div>
      <div className="card">
        <table>
          <thead>
            <tr>
              <th>名称</th>
              <th>排序</th>
              <th>状态</th>
              {canWrite && <th>操作</th>}
            </tr>
          </thead>
          <tbody>
            {items.map((c) => (
              <tr key={c.id}>
                <td>{c.name}</td>
                <td>{c.sort_order}</td>
                <td>
                  {c.deleted_at ? <span className="tag warn">回收站</span> : c.enabled ? <span className="tag">启用</span> : <span className="tag danger">停用</span>}
                </td>
                {canWrite && (
                  <td>
                    {c.deleted_at ? (
                      <button onClick={() => restore(c.id)}>恢复</button>
                    ) : (
                      <button className="danger" onClick={() => remove(c.id)}>
                        删除
                      </button>
                    )}
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
