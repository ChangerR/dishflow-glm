import { useEffect, useState } from 'react';
import { api, ApiError, BoardColumn, Order, centsToYuan } from '../api/client';

const STATE_LABEL: Record<string, string> = {
  PAID: '待接单',
  ACCEPTED: '制作中',
  PREPARING: '制作中',
  READY: '待取餐',
};
// 状态机：PAID→ACCEPTED→PREPARING→READY→COMPLETED（PRD §6.2）。
const NEXT_ACTION: Record<string, { to: string; label: string } | null> = {
  PAID: { to: 'ACCEPTED', label: '接单' },
  ACCEPTED: { to: 'PREPARING', label: '开始制作' },
  PREPARING: { to: 'READY', label: '出餐' },
  READY: { to: 'COMPLETED', label: '完成' },
  COMPLETED: null,
};

export default function Board(_props: { role?: string }) {
  const [columns, setColumns] = useState<BoardColumn[]>([]);
  const [err, setErr] = useState('');
  const [loading, setLoading] = useState(false);

  async function refresh() {
    setLoading(true);
    setErr('');
    try {
      const data = await api.get<{ columns: BoardColumn[] }>('/api/v1/admin/orders/board');
      setColumns(data.columns);
    } catch (e) {
      setErr((e as ApiError).message || '加载失败');
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    refresh();
    // 每 3 秒轮询（PRD §6.1）。
    const t = setInterval(refresh, 3000);
    return () => clearInterval(t);
  }, []);

  async function transition(order: Order) {
    const action = NEXT_ACTION[order.fulfillment_state];
    if (!action) return;
    try {
      await api.post(`/api/v1/admin/orders/${order.id}/transitions`, {
        to: action.to,
        expected_version: order.version, // 乐观锁（PRD §6.1）
      });
      refresh();
    } catch (e) {
      const apiErr = e as ApiError;
      setErr(apiErr.code === 'STATE_CONFLICT' ? '状态已变更或冲突，已刷新' : apiErr.message);
      refresh();
    }
  }

  return (
    <div>
      <div className="topbar">
        <h2>订单工作台</h2>
        <button onClick={refresh} disabled={loading}>
          {loading ? '刷新中…' : '刷新'}
        </button>
      </div>
      {err && <div className="error-box">{err}</div>}
      <div className="grid4">
        {columns.map((col) => (
          <div className="kanban-col" key={col.state}>
            <h3>
              {STATE_LABEL[col.state] || col.state}（{(col.orders || []).length}）
            </h3>
            {(col.orders || []).map((o) => {
              const action = NEXT_ACTION[o.fulfillment_state];
              return (
                <div className="kanban-card" key={o.id}>
                  <div>
                    <strong>#{o.pickup_no ?? '-'}</strong>
                    {o.pickup_type === 'SCHEDULED' && o.scheduled_for && (
                      <span className="tag warn" style={{ marginLeft: 6 }}>
                        预约 {new Date(o.scheduled_for).toLocaleString('zh-CN')}
                      </span>
                    )}
                  </div>
                  <div className="muted">
                    {o.scenario === 'DINE_IN' ? `堂食 ${o.table_label || ''}` : '自取'} · {centsToYuan(o.payable_cents)} 元
                  </div>
                  {action && (
                    <button className="primary" style={{ marginTop: 6, width: '100%' }} onClick={() => transition(o)}>
                      {action.label}
                    </button>
                  )}
                </div>
              );
            })}
          </div>
        ))}
      </div>
    </div>
  );
}
