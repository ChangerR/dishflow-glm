import { useState } from 'react';
import { api, ApiError } from '../api/client';

export default function Members({ role }: { role?: string }) {
  // P6 后台会员列表/详情接口在 PRD §16.3 列出（/admin/customer-members）。
  // 当前后端骨架未暴露列表端点；本页提供积分人工调整入口（MANAGER+，PRD §8.1）。
  const [customerId, setCustomerId] = useState('');
  const [delta, setDelta] = useState('');
  const [reason, setReason] = useState('');
  const [err, setErr] = useState('');
  const [msg, setMsg] = useState('');
  const canWrite = role === 'MANAGER' || role === 'OWNER';

  async function adjust() {
    const cid = parseInt(customerId, 10);
    const d = parseInt(delta, 10);
    if (!cid || isNaN(d) || d === 0 || !reason.trim()) {
      setErr('顾客 ID、调整数量、原因必填');
      return;
    }
    try {
      const r = await api.post<{ balance_after: number }>(`/api/v1/admin/customer-members/${cid}/points-adjustments`, {
        delta: d,
        reason: reason.trim(),
      });
      setMsg(`调整成功，余额 ${r.balance_after}`);
      setErr('');
    } catch (e) {
      const apiErr = e as ApiError;
      setErr(apiErr.code === 'INSUFFICIENT_POINTS' ? '积分不足' : apiErr.message);
      setMsg('');
    }
  }

  return (
    <div>
      <h2>会员 / 积分</h2>
      {err && <div className="error-box">{err}</div>}
      {msg && <div className="tag" style={{ display: 'inline-block', padding: '8px 12px' }}>{msg}</div>}
      {canWrite && (
        <div className="card col" style={{ maxWidth: 360 }}>
          <h3>积分人工调整</h3>
          <input placeholder="顾客 ID" value={customerId} onChange={(e) => setCustomerId(e.target.value)} />
          <input placeholder="调整数量（正/负，非零）" value={delta} onChange={(e) => setDelta(e.target.value)} type="number" />
          <input placeholder="原因（必填）" value={reason} onChange={(e) => setReason(e.target.value)} />
          <button className="primary" onClick={adjust}>
            调整（幂等）
          </button>
        </div>
      )}
    </div>
  );
}
