import { useEffect, useState } from 'react';
import { api, ApiError, Page, centsToYuan } from '../api/client';

interface Dish {
  id: number;
  category_id: number;
  name: string;
  enabled: boolean;
  manually_sold_out: boolean;
  packaging_fee_cents: number;
  deleted_at?: string;
}

export default function Dishes({ role }: { role?: string }) {
  const [items, setItems] = useState<Dish[]>([]);
  const [err, setErr] = useState('');
  const [showDeleted, setShowDeleted] = useState(false);
  const [adjustSku, setAdjustSku] = useState<number | null>(null);
  const [delta, setDelta] = useState('');
  const [reason, setReason] = useState('');
  const canWrite = role === 'MANAGER' || role === 'OWNER';

  async function refresh() {
    try {
      const data = await api.get<Page<Dish>>(`/api/v1/admin/dishes${showDeleted ? '?deleted=1' : ''}`);
      setItems(data.items || []);
    } catch (e) {
      setErr((e as ApiError).message);
    }
  }
  useEffect(() => {
    refresh();
  }, [showDeleted]);

  async function remove(id: number) {
    if (!confirm('删除菜品？进入 30 天回收站。')) return;
    try {
      await api.del(`/api/v1/admin/dishes/${id}`);
      refresh();
    } catch (e) {
      setErr((e as ApiError).message);
    }
  }

  async function restore(id: number) {
    try {
      await api.post(`/api/v1/admin/dishes/${id}/restore`, {});
      refresh();
    } catch (e) {
      setErr((e as ApiError).message);
    }
  }

  async function adjust() {
    if (!adjustSku) return;
    const d = parseInt(delta, 10);
    if (isNaN(d) || d === 0 || !reason.trim()) {
      setErr('调整数量与原因必填');
      return;
    }
    try {
      const today = new Date().toISOString().slice(0, 10);
      await api.post(`/api/v1/admin/dishes/${adjustSku}/stock-adjustments`, {
        business_date: today,
        delta: d,
        reason: reason.trim(),
      });
      setAdjustSku(null);
      setDelta('');
      setReason('');
      setErr('');
    } catch (e) {
      const apiErr = e as ApiError;
      setErr(apiErr.code === 'STATE_CONFLICT' ? '调整会违反库存不变量' : apiErr.message);
    }
  }

  return (
    <div>
      <h2>菜品 / 库存</h2>
      {err && <div className="error-box">{err}</div>}
      <div className="card">
        <label className="muted">
          <input type="checkbox" style={{ width: 'auto' }} checked={showDeleted} onChange={(e) => setShowDeleted(e.target.checked)} /> 显示回收站
        </label>
        <table>
          <thead>
            <tr>
              <th>名称</th>
              <th>包装费</th>
              <th>状态</th>
              {canWrite && <th>操作</th>}
            </tr>
          </thead>
          <tbody>
            {items.map((d) => (
              <tr key={d.id}>
                <td>{d.name}</td>
                <td>{centsToYuan(d.packaging_fee_cents)}</td>
                <td>
                  {d.deleted_at ? (
                    <span className="tag warn">回收站</span>
                  ) : d.manually_sold_out ? (
                    <span className="tag danger">售罄</span>
                  ) : d.enabled ? (
                    <span className="tag">上架</span>
                  ) : (
                    <span className="tag danger">下架</span>
                  )}
                </td>
                {canWrite && (
                  <td className="col">
                    {d.deleted_at ? (
                      <button onClick={() => restore(d.id)}>恢复</button>
                    ) : (
                      <>
                        <button onClick={() => setAdjustSku(d.id)}>库存调整</button>
                        <button className="danger" onClick={() => remove(d.id)}>
                          删除
                        </button>
                      </>
                    )}
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {adjustSku && (
        <div className="card">
          <h3>库存调整（SKU {adjustSku}）</h3>
          <div className="col" style={{ maxWidth: 320 }}>
            <input placeholder="调整数量（正数增加，负数扣减）" value={delta} onChange={(e) => setDelta(e.target.value)} type="number" />
            <input placeholder="调整原因（必填）" value={reason} onChange={(e) => setReason(e.target.value)} />
            <div className="row">
              <button className="primary" onClick={adjust}>
                确认（需 Idempotency-Key）
              </button>
              <button onClick={() => setAdjustSku(null)}>取消</button>
            </div>
            <div className="muted" style={{ fontSize: 12 }}>
              注：后端要求 Idempotency-Key 头，本演示前端由请求自动生成。
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
